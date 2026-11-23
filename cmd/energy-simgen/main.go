// Command energy-simgen deterministically generates the unified 3-building,
// 5-energy simulation dataset into the confirmed local development PostgreSQL
// database (127.0.0.1/localhost + iot_preview). It NEVER prints credentials
// and refuses to run against any non-local host or non-iot_preview database.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/simgen"
)

func main() {
	cfg := simgen.DefaultConfig()
	flag.Int64Var(&cfg.Seed, "seed", cfg.Seed, "fixed deterministic random seed")
	flag.StringVar(&cfg.Version, "version", cfg.Version, "simulation dataset version tag")
	flag.BoolVar(&cfg.Reset, "reset", false, "explicitly reset previous simulation rows before regenerating (default false for safety)")
	flag.Parse()

	dsn := strings.TrimSpace(os.Getenv("IOT_LOCAL_DEV_RW_DSN"))
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "IOT_LOCAL_DEV_RW_DSN is required (pointing to local 127.0.0.1:55432/iot_preview)")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Parse DSN without echoing raw DSN or password in error logs.
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid IOT_LOCAL_DEV_RW_DSN format (credentials redacted)")
		os.Exit(2)
	}
	if pcfg.ConnConfig.Host != "127.0.0.1" && pcfg.ConnConfig.Host != "localhost" && pcfg.ConnConfig.Host != "::1" {
		fmt.Fprintf(os.Stderr, "simgen SAFETY GUARD: refusing non-local host %q\n", pcfg.ConnConfig.Host)
		os.Exit(2)
	}
	if pcfg.ConnConfig.Database != "iot_preview" {
		fmt.Fprintf(os.Stderr, "simgen SAFETY GUARD: refusing database %q (expected iot_preview)\n", pcfg.ConnConfig.Database)
		os.Exit(2)
	}

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect local dev db failed (credentials redacted)")
		os.Exit(1)
	}
	defer pool.Close()

	sum, err := simgen.Generate(ctx, pool, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "simgen failed: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(sum)
}
