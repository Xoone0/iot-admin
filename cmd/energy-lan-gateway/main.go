// Command energy-lan-gateway provides a dedicated, subnet-restricted, READ-ONLY
// passwordless reverse-proxy entrypoint (default port 5177) for trusted private-LAN
// demonstration of the Building Energy Management Workbench (F01-F25 simulated data).
//
// Security boundaries (defense-in-depth):
//  1. Refuses to bind to 0.0.0.0 or any non-RFC1918 IP address.
//  2. Enforces client source IP whitelist (default 192.168.100.0/24; configurable via
//     IOT_LAN_ALLOWED_CIDRS, rejecting non-RFC1918 CIDRs and rejecting outside clients with 403).
//  3. Strictly READ-ONLY: only GET/HEAD requests to /admin/v1/energy-analysis/* are proxied;
//     ALL write verbs (POST/PUT/PATCH/DELETE) are rejected with 403 at the gateway AND the
//     server-held backend token is scoped strictly to the read-only viewer role
//     (ReadUsage + ExportUsage + DownloadReport, ZERO ManageBindings).
//  4. Blocks /admin/v1/dev-auth/* and all non-energy /admin/* routes with 403.
//  5. Never exposes PostgreSQL (55432), Go API (18090), or Vite dev server (5176) directly.
//  6. Pure Go stdlib implementation (no net/http/httputil dependency).
package main

import (
	"bufio"
	"context"
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

const (
	energyAPIPrefix       = "/admin/v1/energy-analysis"
	defaultAllowedSubnets = "192.168.100.0/24"
)

type readonlyTokenCache struct {
	mu        sync.Mutex
	token     string
	subject   string
	expiresAt time.Time
}

func (c *readonlyTokenCache) get(ctx context.Context, backendBase string) (string, string, error) {
	c.mu.Lock()
	if c.token != "" && time.Now().UTC().Add(5*time.Minute).Before(c.expiresAt) {
		tok, sub := c.token, c.subject
		c.mu.Unlock()
		return tok, sub, nil
	}
	c.mu.Unlock()

	tok, sub, exp, err := fetchReadOnlyBackendToken(ctx, backendBase)
	if err != nil {
		return "", "", err
	}
	c.mu.Lock()
	c.token = tok
	c.subject = sub
	c.expiresAt = exp
	c.mu.Unlock()
	return tok, sub, nil
}

func (c *readonlyTokenCache) invalidate() {
	c.mu.Lock()
	c.token = ""
	c.expiresAt = time.Time{}
	c.mu.Unlock()
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

func parseAllowedCIDRs(raw string) ([]*net.IPNet, []string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultAllowedSubnets
	}
	var nets []*net.IPNet
	var labels []string
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		ip, ipNet, err := net.ParseCIDR(p)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid CIDR %q: %w", p, err)
		}
		ip4 := ip.To4()
		if ip4 == nil || !isPrivateLANIPv4(ip4.String()) {
			return nil, nil, fmt.Errorf("SAFETY GUARD: refusing non-private-LAN CIDR %q", p)
		}
		ones, _ := ipNet.Mask.Size()
		if ones < 16 {
			return nil, nil, fmt.Errorf("SAFETY GUARD: CIDR %q is too broad (minimum /16 required)", p)
		}
		nets = append(nets, ipNet)
		labels = append(labels, ipNet.String())
	}
	if len(nets) == 0 {
		return nil, nil, errors.New("at least one private LAN CIDR is required")
	}
	return nets, labels, nil
}

func isAllowedClientIP(remoteAddr string, allowed []*net.IPNet) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, n := range allowed {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func fetchReadOnlyBackendToken(ctx context.Context, backendBase string) (token string, subject string, expiresAt time.Time, err error) {
	endpoint := strings.TrimRight(backendBase, "/") + "/admin/v1/dev-auth/session?role=readonly"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", "", time.Time{}, err
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", "", time.Time{}, fmt.Errorf("backend readonly-session HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Token     string `json:"token"`
		Subject   string `json:"subject"`
		Role      string `json:"role"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", time.Time{}, err
	}
	if payload.Token == "" {
		return "", "", time.Time{}, errors.New("backend returned empty token")
	}
	exp := time.Now().UTC().Add(3 * time.Hour)
	if parsed, pErr := time.Parse(time.RFC3339, payload.ExpiresAt); pErr == nil {
		exp = parsed
	}
	if payload.Subject == "" {
		payload.Subject = "local-viewer"
	}
	return payload.Token, payload.Subject, exp, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
		http.Error(w, "websocket hijack unsupported", http.StatusInternalServerError)
		return
	}
	upConn, err := net.DialTimeout("tcp", target.Host, 5*time.Second)
	if err != nil {
		http.Error(w, "upstream websocket unavailable", http.StatusBadGateway)
		return
	}
	defer upConn.Close()

	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()

	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	outReq.URL.Scheme = target.Scheme
	outReq.URL.Host = target.Host
	outReq.Host = target.Host
	if err := outReq.Write(upConn); err != nil {
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if clientBuf != nil && clientBuf.Reader.Buffered() > 0 {
			_, _ = io.CopyN(upConn, clientBuf, int64(clientBuf.Reader.Buffered()))
		}
		_, _ = io.Copy(upConn, clientConn)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, upConn)
	}()
	wg.Wait()
}

func newStdProxy(target *url.URL, extraHeaders func(r *http.Request) http.Header) http.Handler {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          64,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
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
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "build upstream request failed"})
			return
		}
		outReq.ContentLength = r.ContentLength
		outReq.Host = target.Host

		for k, vals := range r.Header {
			if hopByHopHeaders[http.CanonicalHeaderKey(k)] {
				continue
			}
			for _, v := range vals {
				outReq.Header.Add(k, v)
			}
		}
		outReq.Header.Del("Cookie")
		outReq.Header.Del("Authorization")
		if host, _, splitErr := net.SplitHostPort(r.RemoteAddr); splitErr == nil && host != "" {
			outReq.Header.Set("X-Forwarded-For", host)
		}
		outReq.Header.Set("X-Forwarded-Host", r.Host)
		outReq.Header.Set("X-Forwarded-Proto", "http")
		if extraHeaders != nil {
			for k, vals := range extraHeaders(r) {
				outReq.Header.Del(k)
				for _, v := range vals {
					outReq.Header.Add(k, v)
				}
			}
		}

		resp, err := transport.RoundTrip(outReq)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "上游本地预览服务暂不可用"})
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
		if fl, ok := w.(http.Flusher); ok && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			reader := bufio.NewReader(resp.Body)
			buf := make([]byte, 4096)
			for {
				n, rErr := reader.Read(buf)
				if n > 0 {
					_, _ = w.Write(buf[:n])
					fl.Flush()
				}
				if rErr != nil {
					break
				}
			}
			return
		}
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
		return fmt.Errorf("SAFETY GUARD: refusing non-private-LAN listen address %q", listen)
	}

	allowedNets, allowedLabels, err := parseAllowedCIDRs(os.Getenv("IOT_LAN_ALLOWED_CIDRS"))
	if err != nil {
		return err
	}

	frontendRaw := strings.TrimSpace(os.Getenv("IOT_LAN_FRONTEND_URL"))
	if frontendRaw == "" {
		frontendRaw = "http://127.0.0.1:5176"
	}
	backendRaw := strings.TrimSpace(os.Getenv("IOT_LAN_BACKEND_URL"))
	if backendRaw == "" {
		backendRaw = "http://127.0.0.1:18090"
	}
	frontendURL, err := url.Parse(frontendRaw)
	if err != nil || (frontendURL.Hostname() != "127.0.0.1" && frontendURL.Hostname() != "localhost") {
		return fmt.Errorf("SAFETY GUARD: frontend upstream must be loopback, got %q", frontendRaw)
	}
	backendURL, err := url.Parse(backendRaw)
	if err != nil || (backendURL.Hostname() != "127.0.0.1" && backendURL.Hostname() != "localhost") {
		return fmt.Errorf("SAFETY GUARD: backend upstream must be loopback, got %q", backendRaw)
	}

	tokenCache := &readonlyTokenCache{}
	frontendProxy := newStdProxy(frontendURL, nil)
	backendProxy := newStdProxy(backendURL, func(r *http.Request) http.Header {
		h := make(http.Header)
		if tok, ok := r.Context().Value(ctxTokenKey{}).(string); ok && tok != "" {
			h.Set("Authorization", "Bearer "+tok)
		}
		return h
	})

	allowedSubnetSummary := strings.Join(allowedLabels, ", ")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Strict client subnet check (never open to public internet or other subnets).
		if !isAllowedClientIP(r.RemoteAddr, allowedNets) {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": "来源地址不在受信任局域网网段内，已拒绝访问",
			})
			return
		}

		p := r.URL.Path

		// 2. Security headers on every response.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")

		// 3. Gateway health & read-only status metadata.
		if p == "/lan-healthz" {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":           true,
				"mode":         "lan_readonly_preview",
				"listen":       listen,
				"allowedCidrs": allowedLabels,
				"readOnly":     true,
			})
			return
		}

		if p == "/lan-auth/status" {
			writeJSON(w, http.StatusOK, map[string]any{
				"lanGateway":    true,
				"mode":          "lan_readonly_preview",
				"authenticated": true,
				"readOnly":      true,
				"allowedSubnet": allowedSubnetSummary,
				"subject":       "local-viewer",
			})
			return
		}

		// Legacy login/logout endpoints return clear status instead of opening write auth.
		if p == "/lan-auth/login" || p == "/lan-auth/logout" {
			writeJSON(w, http.StatusOK, map[string]any{
				"lanGateway":    true,
				"mode":          "lan_readonly_preview",
				"authenticated": true,
				"readOnly":      true,
				"message":       "当前局域网入口已切换为可信网段免口令只读预览模式",
			})
			return
		}

		// 4. Hard-block dev-auth & any non-energy admin endpoints.
		if strings.HasPrefix(p, "/admin/v1/dev-auth") {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": "局域网入口禁止直接访问开发自动登录接口",
			})
			return
		}
		if strings.HasPrefix(p, "/admin/") && !strings.HasPrefix(p, energyAPIPrefix) {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": "局域网演示入口仅开放建筑能耗只读分析接口，管理与设备接口已隔离",
			})
			return
		}

		// 5. Energy analysis API: STRICTLY READ-ONLY (GET/HEAD only) + server-side local-viewer JWT.
		if strings.HasPrefix(p, energyAPIPrefix) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				writeJSON(w, http.StatusForbidden, map[string]any{
					"error": "局域网免口令演示模式仅开放只读预览与报表导出；配置修改、规则编辑、项目创建、基线锁定和考核确认请在主机 127.0.0.1:5176 开发模式或正式授权登录后操作",
				})
				return
			}
			tok, _, tokErr := tokenCache.get(r.Context(), backendRaw)
			if tokErr != nil {
				tokenCache.invalidate()
				log.Error("fetch readonly backend token failed", "err", tokErr)
				writeJSON(w, http.StatusBadGateway, map[string]any{
					"error": "无法获取局域网只读预览凭据，请检查主机后端 127.0.0.1:18090 是否运行",
				})
				return
			}
			reqWithTok := r.WithContext(context.WithValue(r.Context(), ctxTokenKey{}, tok))
			backendProxy.ServeHTTP(w, reqWithTok)
			return
		}

		// 6. Root redirect directly to /ui/ energy workbench.
		if p == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}

		// 7. Proxy frontend Vite UI assets & SPA routes.
		frontendProxy.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("energy-lan-gateway listening (passwordless read-only preview)",
			"addr", listen,
			"allowed_cidrs", allowedSubnetSummary,
			"frontend", frontendRaw,
			"backend", backendRaw,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("lan gateway error", "err", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

type ctxTokenKey struct{}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("energy-lan-gateway exited", "err", err)
		os.Exit(1)
	}
}
