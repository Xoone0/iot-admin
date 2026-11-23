package admin

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
)

func isNilInterface(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

// Permission 是后续权限/白名单体系的接入点。本期不实现权限逻辑，接口与调用链先留好。
type Permission interface {
	Allow(ctx context.Context, r *http.Request) bool
}

// AllowAll 是既有 IoT 管理接口使用的实现：直接放行。
type AllowAll struct{}

func (AllowAll) Allow(context.Context, *http.Request) bool { return true }

// DenyAll 是默认拒绝实现：未接入可信上游身份/授权适配器时始终拒绝访问。
type DenyAll struct{}

func (DenyAll) Allow(context.Context, *http.Request) bool { return false }

// WithAuth 把 Permission 串到 handler 前；为兼容既有 IoT 管理路由，p 为 nil 时保持 AllowAll。
// 正式建筑能耗接口不得调用 WithAuth(nil)，必须使用 WithFailClosedAuth 或 authguard.Guard。
func WithAuth(p Permission, next http.Handler) http.Handler {
	if isNilInterface(p) {
		p = AllowAll{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.Allow(r.Context(), r) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if isNilInterface(next) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WithFailClosedAuth 为需要默认拒绝语义的新增正式业务路由提供 fail-closed 边界：
// p 为 nil（含 typed nil）时回退到 DenyAll，永远不回退到 AllowAll，且响应禁缓存。
func WithFailClosedAuth(p Permission, next http.Handler) http.Handler {
	if isNilInterface(p) {
		p = DenyAll{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !p.Allow(r.Context(), r) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if isNilInterface(next) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// IsLoopbackAddr reports whether a listen or peer address (host:port or host)
// targets only the local loopback interface (127.0.0.0/8, ::1, or localhost).
// Wildcard binds such as ":8080", "0.0.0.0:8080", or "[::]:8080" return false.
func IsLoopbackAddr(addr string) bool {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return false
	}
	host := trimmed
	if h, _, err := net.SplitHostPort(trimmed); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return ip.Unmap().IsLoopback()
}

// isLoopbackPeer checks ONLY the direct TCP RemoteAddr, never trusting
// client-supplied X-Forwarded-For / X-Real-IP headers.
func isLoopbackPeer(remoteAddr string) bool {
	trimmed := strings.TrimSpace(remoteAddr)
	if trimmed == "" {
		return false
	}
	host := trimmed
	if h, _, err := net.SplitHostPort(trimmed); err == nil {
		host = h
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	return ip.Unmap().IsLoopback()
}

// hasProxyForwardHeaders reports whether standard reverse-proxy forwarding
// headers are present, indicating a same-host proxy may be relaying an external
// client over a 127.0.0.1/::1 loopback TCP connection.
func hasProxyForwardHeaders(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Real-IP"} {
		if strings.TrimSpace(r.Header.Get(header)) != "" {
			return true
		}
	}
	return false
}

// LoopbackOnlyPermission restricts access to direct (non-proxied) loopback TCP peers
// and optionally delegates to Next. It rejects non-loopback peers AND requests carrying
// proxy forwarding headers (Forwarded / X-Forwarded-For / X-Real-IP) to guard against
// accidental same-host reverse-proxy passthrough. Note: a same-host reverse proxy that
// strips all forwarding headers cannot be distinguished at the TCP layer and MUST
// enforce SSO + mTLS + route ACL at the proxy layer.
type LoopbackOnlyPermission struct {
	Next Permission
}

func (p LoopbackOnlyPermission) Allow(ctx context.Context, r *http.Request) bool {
	if r == nil || !isLoopbackPeer(r.RemoteAddr) || hasProxyForwardHeaders(r) {
		return false
	}
	if !isNilInterface(p.Next) {
		return p.Next.Allow(ctx, r)
	}
	return true
}

// WithLoopbackOnly isolates legacy unauthenticated /admin/v1/* routes to direct
// loopback callers when the server must bind a non-loopback address for device ingest.
// It rejects both non-loopback peers and same-host reverse-proxy forwarded requests.
func WithLoopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackPeer(r.RemoteAddr) {
			w.Header().Set("Cache-Control", "no-store")
			writeError(w, http.StatusForbidden, "legacy_admin_loopback_only: 旧技术管理接口仅允许本机直连回环访问")
			return
		}
		if hasProxyForwardHeaders(r) {
			w.Header().Set("Cache-Control", "no-store")
			writeError(w, http.StatusForbidden, "legacy_admin_proxy_rejected: 检测到本机回环反代转发头；旧管理面若经反代暴露必须由网关强制 SSO、mTLS 与路由 ACL")
			return
		}
		if isNilInterface(next) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		next.ServeHTTP(w, r)
	})
}
