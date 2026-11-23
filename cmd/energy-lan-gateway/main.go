// Command energy-lan-gateway provides a dedicated, fail-closed LAN reverse proxy
// (default port 5177) for trusted Private-LAN simulated-data demonstrations.
//
// Security boundaries:
// - Binds ONLY to a private RFC1918 IPv4 address (never 0.0.0.0 or public IPs).
// - Keeps PostgreSQL (127.0.0.1:55432), Go API (127.0.0.1:18090), and Vite
//   (127.0.0.1:5176) strictly loopback-only.
// - Explicitly blocks /admin/v1/dev-auth/* and non-energy admin routes (403)
//   so remote LAN clients can NEVER impersonate localhost dev auto-login.
// - Requires passcode authentication (/lan-auth/login) mapped to a server-side
//   HttpOnly session cookie before proxying any /admin/v1/energy-analysis/* API
//   or CSV export.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const sessionCookieName = "iot_lan_session"

type lanSession struct {
	BearerToken string
	Subject     string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]lanSession
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]lanSession)}
}

func (s *sessionStore) put(sess lanSession) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for k, v := range s.sessions {
		if now.After(v.ExpiresAt) {
			delete(s.sessions, k)
		}
	}
	s.sessions[id] = sess
	return id, nil
}

func (s *sessionStore) get(id string) (lanSession, bool) {
	if id == "" {
		return lanSession{}, false
	}
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok || time.Now().UTC().After(sess.ExpiresAt) {
		if ok {
			s.delete(id)
		}
		return lanSession{}, false
	}
	return sess, true
}

func (s *sessionStore) delete(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

type loginRateLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

func newLoginRateLimiter() *loginRateLimiter {
	return &loginRateLimiter{failures: make(map[string][]time.Time)}
}

func (l *loginRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-5 * time.Minute)
	var kept []time.Time
	for _, ts := range l.failures[ip] {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	l.failures[ip] = kept
	return len(kept) < 10
}

func (l *loginRateLimiter) recordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[ip] = append(l.failures[ip], time.Now())
}

func isPrivateLANIPv4(host string) bool {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	if ip4 == nil || ip4.IsLoopback() || ip4.IsUnspecified() {
		return false
	}
	// RFC1918 private LAN ranges only: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16.
	if ip4[0] == 10 {
		return true
	}
	if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
		return true
	}
	if ip4[0] == 192 && ip4[1] == 168 {
		return true
	}
	return false
}

func loadPasscode() (string, error) {
	if raw := strings.TrimSpace(os.Getenv("IOT_LAN_PASSCODE")); raw != "" {
		if len(raw) < 8 {
			return "", errors.New("IOT_LAN_PASSCODE must be at least 8 characters")
		}
		return raw, nil
	}
	if path := strings.TrimSpace(os.Getenv("IOT_LAN_PASSCODE_FILE")); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read IOT_LAN_PASSCODE_FILE: %w", err)
		}
		raw := strings.TrimSpace(string(b))
		if len(raw) < 8 {
			return "", errors.New("passcode in IOT_LAN_PASSCODE_FILE must be at least 8 characters")
		}
		return raw, nil
	}
	return "", errors.New("IOT_LAN_PASSCODE or IOT_LAN_PASSCODE_FILE is required")
}

func fetchBackendToken(ctx context.Context, backendBase string) (token string, subject string, expiresAt time.Time, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(backendBase, "/")+"/admin/v1/dev-auth/session", nil)
	if err != nil {
		return "", "", time.Time{}, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "", "", time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", "", time.Time{}, fmt.Errorf("backend token issue failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Token     string `json:"token"`
		Subject   string `json:"subject"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", time.Time{}, err
	}
	if payload.Token == "" {
		return "", "", time.Time{}, errors.New("backend returned empty session token")
	}
	exp, err := time.Parse(time.RFC3339, payload.ExpiresAt)
	if err != nil {
		exp = time.Now().UTC().Add(4 * time.Hour)
	}
	return payload.Token, payload.Subject, exp, nil
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("energy-lan-gateway exited", "err", err)
		os.Exit(1)
	}
}

var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

func proxyWebSocket(w http.ResponseWriter, r *http.Request, target *url.URL) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket hijack unsupported", http.StatusBadGateway)
		return
	}
	upConn, err := net.DialTimeout("tcp", target.Host, 5*time.Second)
	if err != nil {
		http.Error(w, "upstream websocket unavailable", http.StatusBadGateway)
		return
	}
	defer upConn.Close()

	clientConn, clientRW, err := hj.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()

	// Forward exact HTTP upgrade request to loopback Vite server with rewritten Host/Origin.
	outReq := r.Clone(r.Context())
	outReq.URL.Scheme = target.Scheme
	outReq.URL.Host = target.Host
	outReq.Host = target.Host
	outReq.RequestURI = ""
	outReq.Header = r.Header.Clone()
	if outReq.Header.Get("Origin") != "" {
		outReq.Header.Set("Origin", target.Scheme+"://"+target.Host)
	}
	if err := outReq.Write(upConn); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upConn, clientRW)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, upConn)
		done <- struct{}{}
	}()
	<-done
}

func newStdProxy(target *url.URL, rewriteOrigin bool) http.Handler {
	tr := &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        64,
		IdleConnTimeout:     60 * time.Second,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isWebSocketUpgrade(r) {
			proxyWebSocket(w, r, target)
			return
		}
		outURL := *r.URL
		outURL.Scheme = target.Scheme
		outURL.Host = target.Host

		outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), r.Body)
		if err != nil {
			http.Error(w, "bad proxy request", http.StatusBadGateway)
			return
		}
		for k, vals := range r.Header {
			if hopByHopHeaders[http.CanonicalHeaderKey(k)] {
				continue
			}
			for _, v := range vals {
				outReq.Header.Add(k, v)
			}
		}
		outReq.Host = target.Host
		if rewriteOrigin && outReq.Header.Get("Origin") != "" {
			outReq.Header.Set("Origin", target.Scheme+"://"+target.Host)
		}

		resp, err := tr.RoundTrip(outReq)
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for k, vals := range resp.Header {
			if hopByHopHeaders[http.CanonicalHeaderKey(k)] {
				continue
			}
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	listen := strings.TrimSpace(os.Getenv("IOT_LAN_LISTEN"))
	if listen == "" {
		return errors.New("IOT_LAN_LISTEN is required (e.g. 192.168.100.69:5177)")
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil || !isPrivateLANIPv4(host) {
		return fmt.Errorf("SAFETY GUARD: refusing non-private-LAN bind address %q (RFC1918 IPv4 only; never 0.0.0.0)", listen)
	}

	passcode, err := loadPasscode()
	if err != nil {
		return err
	}

	backendTargetStr := strings.TrimSpace(os.Getenv("IOT_LAN_BACKEND_URL"))
	if backendTargetStr == "" {
		backendTargetStr = "http://127.0.0.1:18090"
	}
	frontendTargetStr := strings.TrimSpace(os.Getenv("IOT_LAN_FRONTEND_URL"))
	if frontendTargetStr == "" {
		frontendTargetStr = "http://127.0.0.1:5176"
	}

	backendURL, err := url.Parse(backendTargetStr)
	if err != nil {
		return fmt.Errorf("invalid IOT_LAN_BACKEND_URL: %w", err)
	}
	frontendURL, err := url.Parse(frontendTargetStr)
	if err != nil {
		return fmt.Errorf("invalid IOT_LAN_FRONTEND_URL: %w", err)
	}

	apiProxy := newStdProxy(backendURL, false)
	uiProxy := newStdProxy(frontendURL, true)

	store := newSessionStore()
	limiter := newLoginRateLimiter()

	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "same-origin")

		p := r.URL.Path

		// 1. Health check for startup script
		if p == "/lan-healthz" && r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "lan_demo_gateway"})
			return
		}

		// 2. Root redirect to /ui/
		if p == "/" {
			http.Redirect(w, r, "/ui/", http.StatusTemporaryRedirect)
			return
		}

		// 3. LAN Demo Passcode Auth Endpoints
		if p == "/lan-auth/status" && r.Method == http.MethodGet {
			cookie, _ := r.Cookie(sessionCookieName)
			if cookie != nil {
				if sess, ok := store.get(cookie.Value); ok {
					writeJSON(w, http.StatusOK, map[string]any{
						"lanGateway":    true,
						"authenticated": true,
						"subject":       sess.Subject,
						"expiresAt":     sess.ExpiresAt.Format(time.RFC3339),
					})
					return
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"lanGateway":    true,
				"authenticated": false,
			})
			return
		}

		if p == "/lan-auth/login" && r.Method == http.MethodPost {
			remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)
			if !limiter.allow(remoteIP) {
				writeJSON(w, http.StatusTooManyRequests, map[string]any{
					"code":    1,
					"message": "口令尝试次数过多，请 5 分钟后重试",
				})
				return
			}
			var reqBody struct {
				Passcode string `json:"passcode"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&reqBody); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"code": 1, "message": "无效登录请求"})
				return
			}
			given := strings.TrimSpace(reqBody.Passcode)
			if len(given) != len(passcode) || subtle.ConstantTimeCompare([]byte(given), []byte(passcode)) != 1 {
				limiter.recordFailure(remoteIP)
				writeJSON(w, http.StatusUnauthorized, map[string]any{"code": 1, "message": "局域网演示口令错误"})
				return
			}
			tok, sub, exp, err := fetchBackendToken(r.Context(), backendTargetStr)
			if err != nil {
				log.Error("failed to obtain backend session token after LAN login", "err", err)
				writeJSON(w, http.StatusBadGateway, map[string]any{"code": 1, "message": "后端认证会话签发失败"})
				return
			}
			sessID, err := store.put(lanSession{
				BearerToken: tok,
				Subject:     sub,
				CreatedAt:   time.Now().UTC(),
				ExpiresAt:   exp,
			})
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"code": 1, "message": "会话创建失败"})
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookieName,
				Value:    sessID,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   int(time.Until(exp).Seconds()),
			})
			writeJSON(w, http.StatusOK, map[string]any{
				"code":          0,
				"authenticated": true,
				"subject":       sub,
				"expiresAt":     exp.Format(time.RFC3339),
			})
			return
		}

		if p == "/lan-auth/logout" && r.Method == http.MethodPost {
			if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie != nil {
				store.delete(cookie.Value)
			}
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookieName,
				Value:    "",
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   -1,
			})
			writeJSON(w, http.StatusOK, map[string]any{"code": 0, "authenticated": false})
			return
		}

		// 4. Hard Security Boundary for /admin/*
		if strings.HasPrefix(p, "/admin/") || p == "/admin" {
			// NEVER allow LAN clients to hit loopback dev-auth endpoint!
			if strings.HasPrefix(p, "/admin/v1/dev-auth") {
				writeJSON(w, http.StatusForbidden, map[string]any{
					"code":    1,
					"message": "局域网入口禁止调用本地开发自动登录接口，请通过局域网演示口令认证",
				})
				return
			}
			// NEVER expose general IoT/gateway admin APIs on the LAN demo gateway!
			if !strings.HasPrefix(p, "/admin/v1/energy-analysis") {
				writeJSON(w, http.StatusForbidden, map[string]any{
					"code":    1,
					"message": "局域网演示入口仅开放建筑能耗监测分析接口",
				})
				return
			}

			// Require valid LAN session cookie OR explicit caller-supplied Bearer token.
			authz := strings.TrimSpace(r.Header.Get("Authorization"))
			hasBearer := len(authz) > 7 && strings.EqualFold(authz[:7], "Bearer ")
			if !hasBearer {
				cookie, _ := r.Cookie(sessionCookieName)
				if cookie == nil {
					writeJSON(w, http.StatusUnauthorized, map[string]any{
						"code":    1,
						"message": "局域网访问未通过演示口令认证（默认拒绝）",
					})
					return
				}
				sess, ok := store.get(cookie.Value)
				if !ok {
					writeJSON(w, http.StatusUnauthorized, map[string]any{
						"code":    1,
						"message": "局域网演示会话已失效，请重新输入口令登录",
					})
					return
				}
				// Inject server-held signed Bearer token on the proxied loopback request.
				r2 := r.Clone(r.Context())
				r2.Header = r.Header.Clone()
				r2.Header.Set("Authorization", "Bearer "+sess.BearerToken)
				r2.Header.Del("Cookie")
				apiProxy.ServeHTTP(w, r2)
				return
			}

			apiProxy.ServeHTTP(w, r)
			return
		}

		// 5. Same-Origin Frontend & Vite HMR WebSocket Proxy -> 127.0.0.1:5176
		uiProxy.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("energy-lan-gateway listening (Private LAN demo only)",
			"listen", listen,
			"ui_url", fmt.Sprintf("http://%s/ui/", listen),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}
