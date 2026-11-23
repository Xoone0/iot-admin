// Command energy-live-server runs the local loopback-only HTTP backend for the
// building-energy live preview (http://127.0.0.1:18090) backed by the existing
// local PostgreSQL 17.12 database (127.0.0.1:55432 / iot_preview) without
// requiring an MQTT broker and without resetting simulation data.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RenJieWeia/iot-admin/internal/admin"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/authguard"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/liveapi"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/migrations"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/pgadapter"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("energy-live-server exited", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	listen := strings.TrimSpace(os.Getenv("IOT_ENERGY_LIVE_LISTEN"))
	if listen == "" {
		listen = "127.0.0.1:18090"
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		return fmt.Errorf("refusing non-loopback listen address %q", listen)
	}

	dsn := strings.TrimSpace(os.Getenv("IOT_PG_DSN"))
	if dsn == "" {
		return errors.New("IOT_PG_DSN is required (pointing to local 127.0.0.1:55432/iot_preview)")
	}
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid IOT_PG_DSN format (credentials redacted)")
	}
	if pcfg.ConnConfig.Host != "127.0.0.1" && pcfg.ConnConfig.Host != "localhost" && pcfg.ConnConfig.Host != "::1" {
		return fmt.Errorf("SAFETY GUARD: refusing non-local PostgreSQL host %q", pcfg.ConnConfig.Host)
	}
	if pcfg.ConnConfig.Database != "iot_preview" {
		return fmt.Errorf("SAFETY GUARD: refusing non-preview database %q", pcfg.ConnConfig.Database)
	}

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return errors.New("connect local PostgreSQL failed (credentials redacted)")
	}
	defer pool.Close()

	// Apply additive-only incremental migrations under energy_domain (never resets data).
	if applied, err := migrations.Apply(ctx, pool); err != nil {
		return fmt.Errorf("apply incremental energy_domain migrations: %w", err)
	} else if len(applied) > 0 {
		log.Info("applied additive energy_domain migrations", "versions", applied)
	}

	// Build real HMAC + authguard.Guard (never bypasses auth!).
	cfg, ok := liveapi.LoadEnvAuthConfig()
	if !ok {
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(secretBytes); err != nil {
			return err
		}
		cfg = liveapi.EnvAuthConfig{
			Secret:        hex.EncodeToString(secretBytes),
			Issuers:       []string{"https://local-dev.iot-admin.internal"},
			Audience:      "iot-admin-building-energy",
			MaxTTL:        8 * time.Hour,
			TenantID:      "default",
			ProjectID:     "iot-local",
			ReadSubjects:  []string{"local-viewer"},
			AdminSubjects: []string{"local-admin"},
		}
	}
	rev := liveapi.NewMemoryRevocationStore()
	guard, _, err := liveapi.BuildGuard(cfg, rev, time.Now)
	if err != nil {
		return fmt.Errorf("build energy auth guard: %w", err)
	}

	svc := &liveapi.Service{
		DB:       pgadapter.NewPGPool(pool),
		Guard:    guard,
		Project:  buildingenergy.ProjectKey{TenantID: cfg.TenantID, ProjectID: cfg.ProjectID},
		Timezone: "Asia/Shanghai",
		Now:      time.Now,
	}
	energyHandler := liveapi.NewRouter(guard, svc)
	adminHandler := admin.WithAuth(nil, admin.New(pool).Handler())

	mux := http.NewServeMux()
	mux.Handle(authguard.FormalRoutePrefix, energyHandler)
	mux.Handle("/admin/v1/energy-analysis", energyHandler)
	mux.Handle("/admin/v1/", adminHandler)

	// Strictly loopback-only dev token issuer: issues a real server-signed HS256 JWT
	// verified by authguard.Guard on every subsequent /admin/v1/energy-analysis/* request.
	if os.Getenv("IOT_ENERGY_DEV_LOGIN") == "1" {
		mux.HandleFunc("POST /admin/v1/dev-auth/session", func(w http.ResponseWriter, r *http.Request) {
			remoteHost, _, _ := net.SplitHostPort(r.RemoteAddr)
			if remoteHost != "127.0.0.1" && remoteHost != "::1" {
				http.Error(w, "loopback only", http.StatusForbidden)
				return
			}
			now := time.Now().UTC()
			sub := "local-admin"
			if len(cfg.AdminSubjects) > 0 {
				sub = cfg.AdminSubjects[0]
			}
			iss := cfg.Issuers[0]
			tok, err := liveapi.SignHMACToken(liveapi.TokenClaims{
				Issuer:    iss,
				Subject:   sub,
				SessionID: fmt.Sprintf("dev-sess-%d", now.UnixNano()),
				Audience:  cfg.Audience,
				IssuedAt:  now.Add(-time.Minute).Unix(),
				ExpiresAt: now.Add(4 * time.Hour).Unix(),
			}, []byte(cfg.Secret))
			if err != nil {
				http.Error(w, "token sign failed", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":     tok,
				"subject":   sub,
				"issuer":    iss,
				"expiresAt": now.Add(4 * time.Hour).Format(time.RFC3339),
			})
		})
	}

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"mode":"local_energy_live"}`))
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("energy-live-server listening", "addr", listen, "db", "127.0.0.1:55432/iot_preview")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "err", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
