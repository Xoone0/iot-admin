package pgadapter

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/authguard"
)

// TestClassifyChannelCandidateP0Rules verifies P0 metering channel identification:
//  1. Electrical/process monitoring channels (voltage, current, power, PF, frequency, temp) are excluded.
//  2. Unknown numeric channels stay ChannelUnconfigured with empty SuggestedEnergy (never defaulted to Electricity).
//  3. Cumulative / interval / instantaneous semantics are explicitly distinguished.
func TestClassifyChannelCandidateP0Rules(t *testing.T) {
	cases := []struct {
		name           string
		domain, dName  string
		tCode, tName   string
		code, itemName string
		unit           string
		wantStatus     ChannelStatus
		wantEnergy     buildingenergy.EnergyType
		wantKind       buildingenergy.MeasurementKind
	}{
		{"voltage_unit", "elec", "用电", "meter", "电表", "ua", "A相电压", "V", ChannelExcludedNonEnergy, "", buildingenergy.Instantaneous},
		{"current_code", "elec", "用电", "meter", "电表", "phase_current_a", "A相电流", "A", ChannelExcludedNonEnergy, "", buildingenergy.Instantaneous},
		{"active_power", "elec", "用电", "meter", "电表", "active_power", "有功功率", "kW", ChannelExcludedNonEnergy, "", buildingenergy.Instantaneous},
		{"power_factor", "elec", "用电", "meter", "电表", "power_factor", "功率因数", "", ChannelExcludedNonEnergy, "", buildingenergy.Instantaneous},
		{"frequency", "elec", "用电", "meter", "电表", "grid_freq", "电网频率", "Hz", ChannelExcludedNonEnergy, "", buildingenergy.Instantaneous},
		{"unknown_number_no_default_elec", "generic", "综合", "sensor", "传感器", "metric_x", "自定义数值", "pcs", ChannelUnconfigured, "", ""},
		{"elec_total_kwh", "demo_electric", "用电", "meter", "电表", "total_usage", "累计用电", "kWh", ChannelUnconfigured, buildingenergy.Electricity, buildingenergy.Cumulative},
		{"water_interval", "demo_water", "用水", "water_meter", "水表", "interval_volume", "时段用水量", "m³", ChannelUnconfigured, buildingenergy.Water, buildingenergy.Interval},
		{"gas_nm3", "demo_gas", "燃气", "gas_meter", "燃气表", "total_gas", "累计燃气", "Nm³", ChannelUnconfigured, buildingenergy.Gas, buildingenergy.Cumulative},
		{"heat_gj", "demo_heat", "供热", "heat_meter", "热量表", "total_usage", "累计热量", "GJ", ChannelUnconfigured, buildingenergy.Heat, buildingenergy.Cumulative},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyChannelCandidate(tc.domain, tc.dName, tc.tCode, tc.tName, tc.code, tc.itemName, tc.unit)
			if got.Status != tc.wantStatus || got.SuggestedEnergy != tc.wantEnergy || got.SuggestedKind != tc.wantKind {
				t.Fatalf("got %+v, want status=%q energy=%q kind=%q", got, tc.wantStatus, tc.wantEnergy, tc.wantKind)
			}
		})
	}
}

// TestLiveReadOnlyIOTDatabaseEndToEnd verifies Phase 1 + P0 against an existing
// read-only iot-admin PostgreSQL database when IOT_LIVE_READONLY_DSN is set.
// Standard offline `go test ./...` skips cleanly when the env var is unset.
func TestLiveReadOnlyIOTDatabaseEndToEnd(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("IOT_LIVE_READONLY_DSN"))
	if dsn == "" {
		t.Skip("IOT_LIVE_READONLY_DSN unset; skipping live read-only PostgreSQL smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse live readonly config: %v", err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect live readonly db: %v", err)
	}
	defer pool.Close()

	db := NewPGPool(pool)
	project := buildingenergy.ProjectKey{TenantID: "default", ProjectID: "iot-local"}
	meta, err := LoadIOTMetadata(ctx, db, project, "Asia/Shanghai", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LoadIOTMetadata failed: %v", err)
	}
	if len(meta.Buildings) == 0 || len(meta.Meters) == 0 {
		t.Fatalf("expected buildings and meters in live DB, got buildings=%d meters=%d", len(meta.Buildings), len(meta.Meters))
	}

	// Pick the first candidate electricity channel with >= 25 hourly snapshot rows.
	var picked *IOTMeterRecord
	for i := range meta.Meters {
		m := &meta.Meters[i]
		if m.ChannelStatus != ChannelUnconfigured {
			continue
		}
		if m.SuggestedEnergy == buildingenergy.Electricity && m.BuildingID != nil && m.HourRows >= 25 && m.MaxHourStart != nil {
			picked = m
			break
		}
	}
	if picked == nil {
		t.Fatalf("no candidate electricity meter with >= 25 hourly rows and building location found")
	}

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	maxLocal := picked.MaxHourStart.In(loc)
	dayEnd := time.Date(maxLocal.Year(), maxLocal.Month(), maxLocal.Day(), 0, 0, 0, 0, loc)
	dayStart := dayEnd.AddDate(0, 0, -1)
	asOf := maxLocal.Add(2 * time.Hour)

	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: BuildingObjectID(*picked.BuildingID)}
	point := buildingenergy.PointRef{
		Project: project,
		Key:     buildingenergy.PointKey{TerminalID: picked.TerminalID, ItemCode: picked.ItemCode},
	}

	subject := buildingenergy.Subject{Issuer: "live-readonly-smoke", ID: "operator-1"}
	rev := &mapRevocationChecker{revoked: map[string]bool{}}
	idSource := authguard.ContextIdentitySource{
		AllowedIssuers:   []string{"live-readonly-smoke"},
		ExpectedAudience: "energy-api",
		MaxCredentialTTL: 4 * time.Hour,
		Revocations:      rev,
		Now:              func() time.Time { return asOf },
	}
	policy := &authguard.ScopedPolicy{
		Revocations: rev,
		Grants: []authguard.Grant{{
			Subject: subject, Actions: []buildingenergy.Action{buildingenergy.ReadUsage},
			Project: project, Objects: []buildingenergy.ObjectKey{bldgObj}, Points: []buildingenergy.PointKey{point.Key},
			ValidRange: buildingenergy.Range{From: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		}},
	}
	authCtx := authguard.WithPrincipal(ctx, authguard.Principal{
		Subject: subject, Audience: "energy-api", IssuedAt: asOf.Add(-time.Minute), ExpiresAt: asOf.Add(time.Hour),
	})
	readCtx := WithPointReadBinding(authCtx, PointReadBinding{
		View: buildingenergy.Building, Generation: 1, Objects: []buildingenergy.ObjectKey{bldgObj},
	})

	// P0-9: Unconfigured point MUST be rejected by ReadMapping before calculation.
	unconfiguredCat := &IOTReadOnlyCatalog{Snapshot: meta}
	if _, err := unconfiguredCat.ReadMapping(ctx, point, 1); !errors.Is(err, buildingenergy.ErrMappingUnavailable) {
		t.Fatalf("expected ErrMappingUnavailable for unconfigured point, got %v", err)
	}

	// 1. Live snapshot_coarse calculation with explicit 1x profile.
	cat1x := &IOTReadOnlyCatalog{
		Snapshot: meta,
		ExplicitProfiles: map[buildingenergy.PointKey]ExplicitPointConfig{
			point.Key: {
				Energy:     buildingenergy.Electricity,
				Kind:       buildingenergy.Cumulative,
				Unit:       picked.Unit,
				Multiplier: 1.0,
				Cadence:    time.Hour,
			},
		},
	}
	snapSrc1x := &SnapshotCoarseMeteringSource{
		DB: db,
		Authorizer: &MappingPointAuthorizer{
			Identity: idSource, Policy: policy, Catalog: cat1x, Mappings: cat1x,
		},
	}
	win := buildingenergy.Window{
		Range: buildingenergy.Range{From: dayStart, To: dayEnd}, Timezone: "Asia/Shanghai",
		AsOf: asOf, Granularity: buildingenergy.Hour,
	}
	res1x, err := buildingenergy.CalculateFromSource(readCtx, snapSrc1x, point, win)
	if err != nil {
		t.Fatalf("live CalculateFromSource (1x) failed: %v", err)
	}
	if res1x.Usage == nil || *res1x.Usage <= 0 || res1x.Valid != 24 || res1x.Expected != 24 || len(res1x.Buckets) != 24 {
		t.Fatalf("unexpected live 1x calculation result: usage=%v valid=%d/%d buckets=%d", res1x.Usage, res1x.Valid, res1x.Expected, len(res1x.Buckets))
	}

	// 2. Verify explicit multiplier handling (2x produces exact 2 * 1x usage through M1-A).
	cat2x := &IOTReadOnlyCatalog{
		Snapshot: meta,
		ExplicitProfiles: map[buildingenergy.PointKey]ExplicitPointConfig{
			point.Key: {
				Energy:     buildingenergy.Electricity,
				Kind:       buildingenergy.Cumulative,
				Unit:       picked.Unit,
				Multiplier: 2.0,
				Cadence:    time.Hour,
			},
		},
	}
	snapSrc2x := &SnapshotCoarseMeteringSource{
		DB: db,
		Authorizer: &MappingPointAuthorizer{
			Identity: idSource, Policy: policy, Catalog: cat2x, Mappings: cat2x,
		},
	}
	res2x, err := buildingenergy.CalculateFromSource(readCtx, snapSrc2x, point, win)
	if err != nil || res2x.Usage == nil || math.Abs(*res2x.Usage-2*(*res1x.Usage)) > 1e-6 {
		t.Fatalf("live multiplier 2x mismatch: 1x=%v 2x=%v err=%v", *res1x.Usage, res2x.Usage, err)
	}

	// 3. Verify RawMeteringSource executes cleanly against live PostgreSQL raw_item schema.
	rawSrc := &RawMeteringSource{
		DB: db,
		Authorizer: &MappingPointAuthorizer{
			Identity: idSource, Policy: policy, Catalog: cat1x, Mappings: cat1x,
		},
	}
	rawData, err := rawSrc.Load(readCtx, point, buildingenergy.Range{From: dayStart, To: dayEnd}, asOf)
	if err != nil {
		t.Fatalf("live RawMeteringSource.Load schema verification failed: %v", err)
	}

	t.Logf("LIVE PHASE-1+P0 VERIFIED: building=%q (%s) meter=%s (%s) day=%s..%s 1x=%.3f %s 2x=%.3f %s valid=%d/%d raw_item_rows_in_day=%d first_source=%s",
		picked.BuildingName, bldgObj.ID, picked.TerminalID, picked.TerminalName,
		dayStart.Format("2006-01-02"), dayEnd.Format("2006-01-02"),
		*res1x.Usage, res1x.Unit, *res2x.Usage, res2x.Unit,
		res1x.Valid, res1x.Expected, len(rawData.Samples), res1x.Intervals[0].Sources[0].ID,
	)
}
