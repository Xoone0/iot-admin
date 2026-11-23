package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type permFunc func(context.Context, *http.Request) bool

func (f permFunc) Allow(ctx context.Context, r *http.Request) bool { return f(ctx, r) }

func TestWithAuthPreservesLegacyNilBehavior(t *testing.T) {
	called := false
	h := WithAuth(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/_meta", nil))
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("legacy WithAuth(nil) changed behavior: code=%d called=%v", rec.Code, called)
	}
}

func TestWithFailClosedAuthDeniesNilAndUnauthorized(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/energy-analysis/usage", nil)
	req.Header.Set("X-User-ID", "spoofed-admin")
	req.Header.Set("X-Project-ID", "p1")
	WithFailClosedAuth(nil, next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || called || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("WithFailClosedAuth(nil) did not fail closed: code=%d called=%v cc=%q",
			rec.Code, called, rec.Header().Get("Cache-Control"))
	}

	rec = httptest.NewRecorder()
	WithFailClosedAuth(DenyAll{}, next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("DenyAll did not deny: code=%d called=%v", rec.Code, called)
	}

	rec = httptest.NewRecorder()
	WithFailClosedAuth(permFunc(func(context.Context, *http.Request) bool { return true }), next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("explicit allow failed: code=%d called=%v", rec.Code, called)
	}
}

func TestIsLoopbackAddrAndWithLoopbackOnly(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"127.0.0.2:9000", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{":8080", false},
		{"0.0.0.0:8080", false},
		{"[::]:8080", false},
		{"192.168.1.10:8080", false},
		{"", false},
	} {
		if got := IsLoopbackAddr(tc.addr); got != tc.want {
			t.Fatalf("IsLoopbackAddr(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}

	called := false
	h := WithLoopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	// Remote non-loopback peer is rejected even if X-Forwarded-For / X-Real-IP spoof 127.0.0.1.
	remoteReq := httptest.NewRequest(http.MethodGet, "/admin/v1/gateways/gw1/credential", nil)
	remoteReq.RemoteAddr = "192.168.10.25:51234"
	remoteReq.Header.Set("X-Forwarded-For", "127.0.0.1")
	remoteReq.Header.Set("X-Real-IP", "127.0.0.1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, remoteReq)
	if rec.Code != http.StatusForbidden || called || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("non-loopback request was not blocked: code=%d called=%v cc=%q", rec.Code, called, rec.Header().Get("Cache-Control"))
	}

	// Direct loopback peer passes.
	localReq := httptest.NewRequest(http.MethodGet, "/admin/v1/_meta", nil)
	localReq.RemoteAddr = "127.0.0.1:51234"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, localReq)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("loopback request was blocked: code=%d called=%v", rec.Code, called)
	}

	// IPv6 loopback peer passes via LoopbackOnlyPermission.
	ipv6Req := httptest.NewRequest(http.MethodGet, "/admin/v1/_meta", nil)
	ipv6Req.RemoteAddr = "[::1]:51234"
	if !(LoopbackOnlyPermission{}).Allow(ipv6Req.Context(), ipv6Req) {
		t.Fatal("IPv6 loopback peer was denied by LoopbackOnlyPermission")
	}
	if (LoopbackOnlyPermission{}).Allow(remoteReq.Context(), remoteReq) {
		t.Fatal("non-loopback peer was allowed by LoopbackOnlyPermission")
	}

	// Same-host reverse proxy originating from 127.0.0.1 with forwarding headers is rejected.
	for _, hdr := range []struct{ k, v string }{
		{"X-Forwarded-For", "203.0.113.10"},
		{"X-Real-IP", "203.0.113.10"},
		{"Forwarded", "for=203.0.113.10;proto=https"},
	} {
		called = false
		proxiedReq := httptest.NewRequest(http.MethodGet, "/admin/v1/gateways/gw1/credential", nil)
		proxiedReq.RemoteAddr = "127.0.0.1:54321"
		proxiedReq.Header.Set(hdr.k, hdr.v)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, proxiedReq)
		if rec.Code != http.StatusForbidden || called || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("same-host proxied request with %s was not rejected: code=%d called=%v", hdr.k, rec.Code, called)
		}
		if (LoopbackOnlyPermission{}).Allow(proxiedReq.Context(), proxiedReq) {
			t.Fatalf("LoopbackOnlyPermission allowed same-host proxied request with %s", hdr.k)
		}
	}

	// Typed nil Permission fails closed in WithFailClosedAuth without panicking.
	var nilPerm permFunc
	var nilHandler http.HandlerFunc
	rec = httptest.NewRecorder()
	WithFailClosedAuth(nilPerm, h).ServeHTTP(rec, localReq)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("typed nil Permission did not fail closed: code=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	WithFailClosedAuth(permFunc(func(context.Context, *http.Request) bool { return true }), nilHandler).ServeHTTP(rec, localReq)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("typed nil next handler did not fail closed: code=%d", rec.Code)
	}
}
