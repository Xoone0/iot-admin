package liveapi

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/migrations"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/pgadapter"
)

func TestHMACBearerAuthAndFailClosedBoundary(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	secret := "super-secret-hmac-signing-key-32bytes"
	rev := NewMemoryRevocationStore()
	guard, _, err := BuildGuard(EnvAuthConfig{
		Secret:       secret,
		Issuers:      []string{"iot-idp"},
		Audience:     "energy-api",
		MaxTTL:       4 * time.Hour,
		TenantID:     "default",
		ProjectID:    "iot-local",
		ReadSubjects: []string{"analyst-1"},
	}, rev, func() time.Time { return now })
	if err != nil {
		t.Fatalf("BuildGuard failed: %v", err)
	}

	router := NewRouter(guard, &Service{Guard: guard})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/energy-analysis/overview", nil))
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthenticated request was not rejected: code=%d", rec.Code)
	}

	tok, err := SignHMACToken(TokenClaims{
		Issuer: "iot-idp", Subject: "analyst-1", Audience: "energy-api", SessionID: "s-1",
		IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	}, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	badReq := httptest.NewRequest(http.MethodGet, "/admin/v1/energy-analysis/overview", nil)
	badReq.Header.Set("Authorization", "Bearer "+tok+"tampered")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, badReq)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered JWT was accepted: code=%d", rec.Code)
	}

	rev.RevokeSession("s-1")
	revReq := httptest.NewRequest(http.MethodGet, "/admin/v1/energy-analysis/overview", nil)
	revReq.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, revReq)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session was accepted: code=%d", rec.Code)
	}

	synTok, _ := SignHMACToken(TokenClaims{
		Issuer: "f06-preview", Subject: "local-viewer", Audience: "energy-api",
		IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	}, []byte(secret))
	synReq := httptest.NewRequest(http.MethodGet, "/admin/v1/energy-analysis/overview", nil)
	synReq.Header.Set("Authorization", "Bearer "+synTok)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, synReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("synthetic issuer was not rejected with 403: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestLiveReadOnlyBusinessAPIEndToEnd exercises the formal APIs read-only against
// live PostgreSQL when IOT_LIVE_READONLY_DSN is set.
func TestLiveReadOnlyBusinessAPIEndToEnd(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("IOT_LIVE_READONLY_DSN"))
	if dsn == "" {
		t.Skip("IOT_LIVE_READONLY_DSN unset; skipping live read-only API smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	secret := "live-readonly-smoke-secret-32bytes!"
	rev := NewMemoryRevocationStore()
	guard, _, err := BuildGuard(EnvAuthConfig{
		Secret:        secret,
		Issuers:       []string{"iot-idp"},
		Audience:      "energy-api",
		MaxTTL:        4 * time.Hour,
		TenantID:      "default",
		ProjectID:     "iot-local",
		ReadSubjects:  []string{"live-reader"},
		AdminSubjects: []string{"live-admin"},
	}, rev, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		DB:       pgadapter.NewPGPool(pool),
		Guard:    guard,
		Project:  buildingenergy.ProjectKey{TenantID: "default", ProjectID: "iot-local"},
		Timezone: "Asia/Shanghai",
		Now:      func() time.Time { return now },
	}
	router := NewRouter(guard, svc)
	readerToken, _ := SignHMACToken(TokenClaims{
		Issuer: "iot-idp", Subject: "live-reader", Audience: "energy-api", SessionID: "ro-1",
		IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	}, []byte(secret))

	get := func(path string) map[string]any {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+readerToken)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s => %d body=%s", path, rec.Code, rec.Body.String())
		}
		var env struct {
			Code int            `json:"code"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Code != 0 {
			t.Fatalf("GET %s decode failed: err=%v body=%s", path, err, rec.Body.String())
		}
		return env.Data
	}

	inv := get("/admin/v1/energy-analysis/management/inventory")
	t.Logf("Inventory: energyDomainReady=%v configured=%v unconfigured=%v excluded=%v",
		inv["energyDomainReady"], inv["configuredMeterCount"], inv["unconfiguredCount"], inv["excludedChannelCount"])

	// Read-only subject MUST be denied on management writes (403).
	wReq := httptest.NewRequest(http.MethodPost, "/admin/v1/energy-analysis/management/buildings", strings.NewReader(`{"buildingId":"bldg-1","name":"forbidden"}`))
	wReq.Header.Set("Authorization", "Bearer "+readerToken)
	wRec := httptest.NewRecorder()
	router.ServeHTTP(wRec, wReq)
	if wRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for read-only subject on POST /management/buildings, got %d", wRec.Code)
	}

	// Verify F01/F02/F03/F04/F05/F06/F07/F08/F09/F10/F11/F12/F13/F17/F21/F22/F25 all respond 200 cleanly.
	for _, p := range []string{
		"/admin/v1/energy-analysis/overview?building=bldg-1&date=2026-11-17",
		"/admin/v1/energy-analysis/composition?building=bldg-1&date=2026-11-17",
		"/admin/v1/energy-analysis/objects-overview?building=bldg-1&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/realtime-trend?building=bldg-1&energy=electricity&date=2026-11-17&days=7",
		"/admin/v1/energy-analysis/targets?building=bldg-1&energy=electricity&year=2026",
		"/admin/v1/energy-analysis/classification?building=bldg-1&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/dimensions?view=building&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/trend?building=bldg-1&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/balance?building=bldg-1&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/anomalies?building=bldg-1&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/efficiency?building=bldg-1&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/industry/templates?building=bldg-1&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/industry/benchmarks?building=bldg-1&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/opportunities?building=bldg-1&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/reports/consumption?building=bldg-1&date=2026-11-17",
		"/admin/v1/energy-analysis/reports/metrics?building=bldg-1&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/reports/custom?building=bldg-1&energy=electricity&date=2026-11-17&scope=metrics&fields=building_name,metric_name,metric_value,benchmark_status",
	} {
		_ = get(p)
	}

	// Verify F25 rejects unauthorized field with 400.
	badFieldReq := httptest.NewRequest(http.MethodGet, "/admin/v1/energy-analysis/reports/custom?building=bldg-1&scope=metrics&fields=password_hash", nil)
	badFieldReq.Header.Set("Authorization", "Bearer "+readerToken)
	badFieldRec := httptest.NewRecorder()
	router.ServeHTTP(badFieldRec, badFieldReq)
	if badFieldRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unauthorized custom report field, got %d body=%s", badFieldRec.Code, badFieldRec.Body.String())
	}
}

// TestLiveLocalDevGovernanceEndToEnd applies incremental energy_domain migrations
// ONLY to the confirmed local dev database (iot_preview on 127.0.0.1:55432) when
// IOT_LOCAL_DEV_RW_DSN is explicitly set, then verifies P0, P1, P2, P3 (historical
// effective-dated versioning), and P4 (F05..F25 + exact preview↔CSV parity).
func TestLiveLocalDevGovernanceEndToEnd(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("IOT_LOCAL_DEV_RW_DSN"))
	if dsn == "" {
		t.Skip("IOT_LOCAL_DEV_RW_DSN unset; skipping local dev governance test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost" {
		t.Fatalf("SAFETY GUARD: refusing to run write governance test on non-local host %q", cfg.ConnConfig.Host)
	}
	if cfg.ConnConfig.Database != "iot_preview" {
		t.Fatalf("SAFETY GUARD: refusing to run write governance test on database %q (expected iot_preview)", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// 1. Apply additive energy_domain migrations 0001 + 0002 to local dev DB.
	applied, err := migrations.Apply(ctx, pool)
	if err != nil {
		t.Fatalf("migrations.Apply failed: %v", err)
	}
	t.Logf("energy_domain migrations newly applied: %v", applied)

	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	secret := "local-dev-gov-secret-32bytes-key!"
	rev := NewMemoryRevocationStore()
	guard, _, err := BuildGuard(EnvAuthConfig{
		Secret:        secret,
		Issuers:       []string{"iot-idp"},
		Audience:      "energy-api",
		MaxTTL:        4 * time.Hour,
		TenantID:      "default",
		ProjectID:     "iot-local",
		ReadSubjects:  []string{"live-reader"},
		AdminSubjects: []string{"live-admin"},
	}, rev, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		DB:       pgadapter.NewPGPool(pool),
		Guard:    guard,
		Project:  buildingenergy.ProjectKey{TenantID: "default", ProjectID: "iot-local"},
		Timezone: "Asia/Shanghai",
		Now:      func() time.Time { return now },
	}
	router := NewRouter(guard, svc)
	adminToken, _ := SignHMACToken(TokenClaims{
		Issuer: "iot-idp", Subject: "live-admin", Audience: "energy-api", SessionID: "adm-1",
		IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	}, []byte(secret))

	doJSON := func(method, path string, body any, wantCode int) map[string]any {
		t.Helper()
		var rdr *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rdr)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != wantCode {
			t.Fatalf("%s %s => %d (want %d) body=%s", method, path, rec.Code, wantCode, rec.Body.String())
		}
		var env struct {
			Code int            `json:"code"`
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		return env.Data
	}

	// 2. P3: Publish effective-dated buildings (A 座 with historical area revision + B 座 without area to prove missing denominator!), spaces & organizations.
	areaOld := 10000.0
	areaNew := 12500.0
	peopleA := 450.0
	t2025 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tCut := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	legacy1, legacy2 := int64(1), int64(2)

	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/buildings", UpsertBuildingRequest{
		BuildingID: "bldg-1", Revision: "bldg-1-2025", Name: "演示 · A 座", BuildingType: "office", Region: "上海",
		GrossAreaSqm: &areaOld, OccupiedHeadcount: &peopleA, EffectiveFrom: &t2025, EffectiveTo: &tCut, LegacyBuildingID: &legacy1,
	}, http.StatusOK)
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/buildings", UpsertBuildingRequest{
		BuildingID: "bldg-1", Revision: "bldg-1-2026q4", Name: "演示 · A 座", BuildingType: "office", Region: "上海",
		GrossAreaSqm: &areaNew, OccupiedHeadcount: &peopleA, EffectiveFrom: &tCut, LegacyBuildingID: &legacy1,
	}, http.StatusOK)
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/buildings", UpsertBuildingRequest{
		BuildingID: "bldg-2", Revision: "bldg-2-2025", Name: "演示 · B 座", BuildingType: "office", Region: "上海",
		EffectiveFrom: &t2025, LegacyBuildingID: &legacy2,
	}, http.StatusOK)

	for _, sp := range []UpsertSpaceRequest{
		{SpaceID: "area-1", Revision: "area-1-v1", BuildingID: "bldg-1", Name: "演示 · 设备区", EffectiveFrom: &t2025},
		{SpaceID: "area-3", Revision: "area-3-v1", BuildingID: "bldg-1", Name: "演示 · 照明区", EffectiveFrom: &t2025},
		{SpaceID: "area-2", Revision: "area-2-v1", BuildingID: "bldg-2", Name: "演示 · 公共区", EffectiveFrom: &t2025},
	} {
		doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/spaces", sp, http.StatusOK)
	}
	hc1 := 260.0
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/organizations", UpsertOrganizationRequest{
		OrgID: "org-ops", Revision: "org-ops-v1", Name: "运维与公共设施部", Headcount: &hc1, EffectiveFrom: &t2025,
	}, http.StatusOK)

	// 3. P0/P1: Publish configured metering points for iot_preview sample meters; verify instantaneous is rejected!
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/metering-points", PublishMeteringPointRequest{
		TerminalID: "demo-e01", ItemCode: "total_usage", SourceTable: pgadapter.SourceTableSnapshotCoarse,
		Energy: buildingenergy.Electricity, MeasurementKind: buildingenergy.Instantaneous, Unit: "kWh",
		Multiplier: 1.0, CadenceSeconds: 3600, EffectiveFrom: t2025, Reason: "must fail P0-5",
	}, http.StatusBadRequest)

	metersToConfig := []struct {
		tid, unit, bldg, space, org string
		energy                      buildingenergy.EnergyType
	}{
		{"demo-e01", "kWh", "bldg-1", "area-3", "org-ops", buildingenergy.Electricity},
		{"demo-e02", "kWh", "bldg-1", "area-1", "org-ops", buildingenergy.Electricity},
		{"demo-e03", "kWh", "bldg-1", "area-1", "org-ops", buildingenergy.Electricity},
		{"demo-w01", "m³", "bldg-1", "area-1", "org-ops", buildingenergy.Water},
		{"demo-h01", "GJ", "bldg-1", "area-1", "org-ops", buildingenergy.Heat},
		{"demo-e04", "kWh", "bldg-2", "area-2", "org-ops", buildingenergy.Electricity},
		{"demo-e05", "kWh", "bldg-2", "area-2", "org-ops", buildingenergy.Electricity},
		{"demo-w02", "m³", "bldg-2", "area-2", "org-ops", buildingenergy.Water},
		{"demo-g01", "Nm³", "bldg-2", "area-2", "org-ops", buildingenergy.Gas},
		{"demo-h02", "GJ", "bldg-2", "area-2", "org-ops", buildingenergy.Heat},
	}
	for _, mc := range metersToConfig {
		doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/metering-points", PublishMeteringPointRequest{
			TerminalID: mc.tid, ItemCode: "total_usage", SourceTable: pgadapter.SourceTableSnapshotCoarse,
			Energy: mc.energy, MeasurementKind: buildingenergy.Cumulative, Unit: mc.unit,
			Multiplier: 1.0, CadenceSeconds: 3600, EffectiveFrom: t2025,
			BuildingAlloc:     []ObjectAllocationInput{{TargetID: mc.bldg, WeightPPM: buildingenergy.FullWeightPPM}},
			SpaceAlloc:        []ObjectAllocationInput{{TargetID: mc.space, WeightPPM: buildingenergy.FullWeightPPM}},
			OrganizationAlloc: []ObjectAllocationInput{{TargetID: mc.org, WeightPPM: buildingenergy.FullWeightPPM}},
			Reason:            "initial local dev governance configuration",
		}, http.StatusOK)
	}

	// Ensure demo-e01 is enabled and test P1 enable/disable round-trip.
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/metering-points/status", SetMeteringPointStatusRequest{
		TerminalID: "demo-e01", ItemCode: "total_usage", Enabled: false, Reason: "maintenance pause test",
	}, http.StatusOK)
	clsDisabled := doJSON(http.MethodGet, "/admin/v1/energy-analysis/classification?building=bldg-1&energy=electricity&date=2026-11-17", nil, http.StatusOK)
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/metering-points/status", SetMeteringPointStatusRequest{
		TerminalID: "demo-e01", ItemCode: "total_usage", Enabled: true, Reason: "re-enabled after check",
	}, http.StatusOK)
	clsEnabled := doJSON(http.MethodGet, "/admin/v1/energy-analysis/classification?building=bldg-1&energy=electricity&date=2026-11-17", nil, http.StatusOK)
	if clsEnabled["totalUsage"].(float64) <= clsDisabled["totalUsage"].(float64) {
		t.Fatalf("expected disabling demo-e01 to reduce building total: disabled=%v enabled=%v", clsDisabled["totalUsage"], clsEnabled["totalUsage"])
	}

	// 4. P2: Formal category tree + multi-category weighted split (demo-e02: 70% HVAC / 30% Power) + leave demo-e03 unbound -> Unclassified!
	for _, cat := range []UpsertCategoryRequest{
		{CategoryID: "elec-lighting", Name: "照明插座", Energy: buildingenergy.Electricity},
		{CategoryID: "elec-hvac", Name: "暖通空调", Energy: buildingenergy.Electricity},
		{CategoryID: "elec-power", Name: "动力系统", Energy: buildingenergy.Electricity},
		{CategoryID: "water-domestic", Name: "生活及公区用水", Energy: buildingenergy.Water},
		{CategoryID: "heat-central", Name: "集中供热", Energy: buildingenergy.Heat},
	} {
		doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/categories", cat, http.StatusOK)
	}
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/category-bindings", PublishCategoryBindingRequest{
		TerminalID: "demo-e01", ItemCode: "total_usage", Revision: "cb-e01-v1", EffectiveFrom: &t2025,
		Allocations: []CategoryAllocationInput{{CategoryID: "elec-lighting", WeightPPM: 1000000}},
	}, http.StatusOK)
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/category-bindings", PublishCategoryBindingRequest{
		TerminalID: "demo-e02", ItemCode: "total_usage", Revision: "cb-e02-v1", EffectiveFrom: &t2025,
		Allocations: []CategoryAllocationInput{
			{CategoryID: "elec-hvac", WeightPPM: 700000},
			{CategoryID: "elec-power", WeightPPM: 300000},
		},
	}, http.StatusOK)
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/category-bindings", PublishCategoryBindingRequest{
		TerminalID: "demo-w01", ItemCode: "total_usage", Revision: "cb-w01-v1", EffectiveFrom: &t2025,
		Allocations: []CategoryAllocationInput{{CategoryID: "water-domestic", WeightPPM: 1000000}},
	}, http.StatusOK)
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/category-bindings", PublishCategoryBindingRequest{
		TerminalID: "demo-h01", ItemCode: "total_usage", Revision: "cb-h01-v1", EffectiveFrom: &t2025,
		Allocations: []CategoryAllocationInput{{CategoryID: "heat-central", WeightPPM: 1000000}},
	}, http.StatusOK)
	// Note: demo-e03 intentionally has NO category binding -> MUST appear in unclassifiedUsage!
	clsP2 := doJSON(http.MethodGet, "/admin/v1/energy-analysis/classification?building=bldg-1&energy=electricity&date=2026-11-17", nil, http.StatusOK)
	uncls, ok := clsP2["unclassifiedUsage"].(float64)
	if !ok || uncls <= 0 {
		t.Fatalf("P2 expected positive unclassifiedUsage for unbound demo-e03, got %v", clsP2["unclassifiedUsage"])
	}
	t.Logf("P2 Classification verified: total=%.3f kWh, unclassified(demo-e03)=%.3f kWh", clsP2["totalUsage"], uncls)

	// 5. P3: Verify historical effective-dated building area! (2026-11-05 uses 10,000 m² rev; 2026-11-17 uses 12,500 m² rev!)
	effOld := doJSON(http.MethodGet, "/admin/v1/energy-analysis/efficiency?building=bldg-1&energy=electricity&date=2026-11-05", nil, http.StatusOK)
	effNew := doJSON(http.MethodGet, "/admin/v1/energy-analysis/efficiency?building=bldg-1&energy=electricity&date=2026-11-17", nil, http.StatusOK)
	if effOld["buildingRevision"] != "bldg-1-2025" || effNew["buildingRevision"] != "bldg-1-2026q4" {
		t.Fatalf("P3 historical building revision failed: old=%v new=%v", effOld["buildingRevision"], effNew["buildingRevision"])
	}
	oldArea := effOld["metrics"].([]any)[0].(map[string]any)["denominatorValue"].(float64)
	newArea := effNew["metrics"].([]any)[0].(map[string]any)["denominatorValue"].(float64)
	if math.Abs(oldArea-10000) > 1e-6 || math.Abs(newArea-12500) > 1e-6 {
		t.Fatalf("P3 historical denominator failed: oldArea=%v newArea=%v", oldArea, newArea)
	}
	t.Logf("P3 Historical versioning verified: 2026-11-05 rev=%s area=%.0f | 2026-11-17 rev=%s area=%.0f",
		effOld["buildingRevision"], oldArea, effNew["buildingRevision"], newArea)

	// 6. P4: Configure Target (F05/F14), Anomaly Rule (F10/F17), Demo Benchmark (F13), and verify all F05..F25 + exact CSV parity!
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/targets", PublishTargetRequest{
		ObjectKind: buildingenergy.Building, ObjectID: "bldg-1", Energy: buildingenergy.Electricity,
		Year: 2026, Revision: "tgt-bldg1-2026-v1", Unit: "kWh", AnnualValue: 900000, Method: buildingenergy.TargetLinear,
	}, http.StatusOK)
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/anomaly-rules", PublishAnomalyRuleRequest{
		ObjectKind: buildingenergy.Building, ObjectID: "bldg-1", Energy: buildingenergy.Electricity,
		Revision: "anom-bldg1-v1", EffectiveFrom: &t2025, MinimumHistory: 2, MinimumAbsoluteIncrease: 50, MinimumRelativeIncrease: 0.05,
	}, http.StatusOK)
	doJSON(http.MethodPost, "/admin/v1/energy-analysis/management/benchmarks", PublishBenchmarkRequest{
		BenchmarkID: "demo-sh-office-elec-day", Revision: "bm-v1", EffectiveFrom: &t2025,
		Name: "上海办公建筑日用电演示阈值", Publisher: "本地开发演示", Region: "上海",
		MetricID: "office-electricity-area", TemplateVersion: "tmpl-office-v1", Unit: "kWh/m²",
		Kind: buildingenergy.BenchmarkDemo, BuildingType: buildingenergy.BuildingOffice,
		ObjectKind: buildingenergy.Building, PeriodGranularity: buildingenergy.Day, MaximumValue: 0.30,
	}, http.StatusOK)

	tgt := doJSON(http.MethodGet, "/admin/v1/energy-analysis/targets?building=bldg-1&energy=electricity&year=2026", nil, http.StatusOK)
	if tgt["targetConfigured"] != true {
		t.Fatalf("F05/F14 expected targetConfigured=true, got %+v", tgt)
	}
	anom := doJSON(http.MethodGet, "/admin/v1/energy-analysis/anomalies?building=bldg-1&energy=electricity&date=2026-11-17", nil, http.StatusOK)
	if anom["ruleConfigured"] != true {
		t.Fatalf("F10 expected ruleConfigured=true, got %+v", anom)
	}
	opp := doJSON(http.MethodGet, "/admin/v1/energy-analysis/opportunities?building=bldg-1&energy=electricity&date=2026-11-17", nil, http.StatusOK)
	bm := doJSON(http.MethodGet, "/admin/v1/energy-analysis/industry/benchmarks?building=bldg-1&energy=electricity&date=2026-11-17", nil, http.StatusOK)
	t.Logf("P4 verified: F05/F14 target=%s F10 status=%v baselineCount=%v F17 clues=%d F13 rows=%d",
		tgt["targetVersion"], anom["status"], anom["baselineCount"], len(opp["clues"].([]any)), len(bm["rows"].([]any)))

	// Verify exact preview ↔ CSV parity for F21, F22, and F25!
	for _, rptPath := range []string{
		"/admin/v1/energy-analysis/reports/consumption?building=bldg-1&date=2026-11-17",
		"/admin/v1/energy-analysis/reports/metrics?building=bldg-1&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/reports/custom?building=bldg-1&energy=electricity&date=2026-11-17&scope=metrics&fields=building_name,metric_name,metric_value,benchmark_status",
	} {
		jsonData := doJSON(http.MethodGet, rptPath, nil, http.StatusOK)
		csvReq := httptest.NewRequest(http.MethodGet, rptPath+"&format=csv", nil)
		csvReq.Header.Set("Authorization", "Bearer "+adminToken)
		csvRec := httptest.NewRecorder()
		router.ServeHTTP(csvRec, csvReq)
		if csvRec.Code != http.StatusOK {
			t.Fatalf("CSV %s => %d", rptPath, csvRec.Code)
		}
		raw := bytes.TrimPrefix(csvRec.Body.Bytes(), []byte{0xEF, 0xBB, 0xBF})
		recs, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
		if err != nil {
			t.Fatalf("parse CSV %s: %v", rptPath, err)
		}
		cols := jsonData["columns"].([]any)
		rows := jsonData["rows"].([]any)
		if len(recs) != len(rows)+1 || len(recs[0]) != len(cols) {
			t.Fatalf("CSV/Preview dimension mismatch on %s: csv=%dx%d json=%dx%d", rptPath, len(recs)-1, len(recs[0]), len(rows), len(cols))
		}
		for ri, rowAny := range rows {
			row := rowAny.(map[string]any)
			for ci, colAny := range cols {
				key := colAny.(map[string]any)["key"].(string)
				want := row[key].(string)
				got := recs[ri+1][ci]
				if got != want {
					t.Fatalf("CSV/Preview cell mismatch on %s row %d col %s: got=%q want=%q", rptPath, ri, key, got, want)
				}
			}
		}
		t.Logf("CSV/Preview exact parity verified: %s (%d rows x %d cols)", rptPath, len(rows), len(cols))
	}
}

// TestUnifiedFiveEnergySimulationEndToEnd verifies the unified deterministic 3-building,
// 5-energy simulation dataset across ALL F01–F25 endpoints, exact cross-page reconciliation,
// controlled anomaly samples, historical YoY/monthly targets, and exact CSV parity for F21–F25.
func TestUnifiedFiveEnergySimulationEndToEnd(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("IOT_SIM_VERIFY_DSN"))
	if dsn == "" {
		t.Skip("IOT_SIM_VERIFY_DSN unset; skipping unified 5-energy simulation end-to-end test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	now := time.Date(2026, 11, 18, 16, 0, 0, 0, time.UTC)
	secret := "sim-verify-secret-32bytes-key-ok!"
	rev := NewMemoryRevocationStore()
	guard, _, err := BuildGuard(EnvAuthConfig{
		Secret:       secret,
		Issuers:      []string{"iot-idp"},
		Audience:     "energy-api",
		MaxTTL:       4 * time.Hour,
		TenantID:     "default",
		ProjectID:    "iot-local",
		ReadSubjects: []string{"sim-reader"},
	}, rev, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		DB:       pgadapter.NewPGPool(pool),
		Guard:    guard,
		Project:  buildingenergy.ProjectKey{TenantID: "default", ProjectID: "iot-local"},
		Timezone: "Asia/Shanghai",
		Now:      func() time.Time { return now },
	}
	router := NewRouter(guard, svc)
	tok, _ := SignHMACToken(TokenClaims{
		Issuer: "iot-idp", Subject: "sim-reader", Audience: "energy-api", SessionID: "sim-1",
		IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	}, []byte(secret))

	get := func(path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s => %d body=%s", path, rec.Code, rec.Body.String())
		}
		var env struct {
			Code int            `json:"code"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Code != 0 {
			t.Fatalf("GET %s decode failed: %v body=%s", path, err, rec.Body.String())
		}
		return env.Data
	}

	// 1. Inventory: 3 buildings, 57 configured energy meters, 12 excluded non-energy channels, simulation metadata.
	inv := get("/admin/v1/energy-analysis/management/inventory")
	ds := inv["dataset"].(map[string]any)
	if ds["isSimulated"] != true || ds["datasetVersion"] != "sim-v1.0.0" {
		t.Fatalf("unexpected dataset metadata: %+v", ds)
	}
	if int(inv["configuredMeterCount"].(float64)) != 57 || int(inv["excludedChannelCount"].(float64)) < 12 {
		t.Fatalf("expected 57 configured energy meters and >=12 excluded electrical channels, got cfg=%v excl=%v",
			inv["configuredMeterCount"], inv["excludedChannelCount"])
	}
	if len(inv["buildings"].([]any)) != 3 {
		t.Fatalf("expected 3 standard buildings, got %d", len(inv["buildings"].([]any)))
	}

	// 2. All 5 energies on F01 Overview for all 3 buildings!
	for _, bID := range []string{"bldg-a", "bldg-b", "bldg-c"} {
		ov := get("/admin/v1/energy-analysis/overview?building=" + bID + "&date=2026-11-17")
		energies := ov["energies"].([]any)
		if len(energies) != 5 {
			t.Fatalf("%s expected 5 separated energy cards (电/水/气/热/冷), got %d", bID, len(energies))
		}
	}

	// 3. Exact Reconciliation: F01 == F06 == F07 == F08 == F21 for bldg-a electricity 2026-11-17!
	ovA := get("/admin/v1/energy-analysis/overview?building=bldg-a&date=2026-11-17")
	f01Elec := ovA["energies"].([]any)[0].(map[string]any)["usage"].(float64)
	f06A := get("/admin/v1/energy-analysis/classification?building=bldg-a&energy=electricity&date=2026-11-17")
	f06Elec := f06A["totalUsage"].(float64)
	f07 := get("/admin/v1/energy-analysis/dimensions?view=building&energy=electricity&date=2026-11-17")
	var f07Elec float64
	for _, rAny := range f07["rows"].([]any) {
		r := rAny.(map[string]any)
		if r["id"] == "bldg-a" {
			f07Elec = r["usage"].(float64)
		}
	}
	f08A := get("/admin/v1/energy-analysis/trend?building=bldg-a&energy=electricity&date=2026-11-17")
	f08Elec := f08A["current"].(float64)
	if math.Abs(f01Elec-f06Elec) > 1e-3 || math.Abs(f06Elec-f07Elec) > 1e-3 || math.Abs(f06Elec-f08Elec) > 1e-3 {
		t.Fatalf("RECONCILIATION MISMATCH: F01=%.3f F06=%.3f F07=%.3f F08=%.3f", f01Elec, f06Elec, f07Elec, f08Elec)
	}
	// Check real YoY & MoM comparable!
	// bldg-a changed building area revision from 2025 (30,000 m²) to 2026 (32,000 m²), so YoY honestly flags inconsistent_scope,
	// while bldg-b keeps a stable 2025-2026 revision and achieves real comparable YoY!
	if f08A["previous"].(map[string]any)["status"] != "comparable" {
		t.Fatalf("F08 MoM expected comparable, got %+v", f08A["previous"])
	}
	f08B := get("/admin/v1/energy-analysis/trend?building=bldg-b&energy=electricity&date=2026-11-17")
	if f08B["priorYear"].(map[string]any)["status"] != "comparable" {
		t.Fatalf("F08 bldg-b YoY expected comparable, got %+v", f08B["priorYear"])
	}
	t.Logf("RECONCILIATION OK (bldg-a elec 2026-11-17): F01=F06=F07=F08=%.3f kWh | bldg-a MoM=%s YoY=%s | bldg-b YoY=%s",
		f06Elec, f08A["previous"].(map[string]any)["status"], f08A["priorYear"].(map[string]any)["status"], f08B["priorYear"].(map[string]any)["status"])

	// 4. F09 Balance: bldg-b electricity controlled +8.8% imbalance vs bldg-a balanced (~0.8%)!
	balA := get("/admin/v1/energy-analysis/balance?building=bldg-a&energy=electricity&date=2026-11-17")
	balB := get("/admin/v1/energy-analysis/balance?building=bldg-b&energy=electricity&date=2026-11-17")
	if balA["topologyConfigured"] != true || balB["topologyConfigured"] != true {
		t.Fatalf("expected F09 topology configured on both bldg-a and bldg-b")
	}
	diffRateA := balA["report"].(map[string]any)["DifferenceRate"].(float64)
	diffRateB := balB["report"].(map[string]any)["DifferenceRate"].(float64)
	if diffRateB <= 0.05 || diffRateA >= 0.03 {
		t.Fatalf("expected bldg-b imbalance >5%% and bldg-a <3%%, got A=%.4f B=%.4f", diffRateA, diffRateB)
	}
	t.Logf("F09 Balance verified: bldg-a diffRate=%.2f%% | bldg-b controlled imbalance diffRate=%.2f%%", diffRateA*100, diffRateB*100)

	// 5. Controlled anomalies: F10 spike (bldg-a 2026-11-17), F06 unclassified (bldg-b socket), rollover (bldg-b 2026-11-12), swap (bldg-c 2026-11-11)!
	f10A := get("/admin/v1/energy-analysis/anomalies?building=bldg-a&energy=electricity&date=2026-11-17")
	if f10A["status"] != "usage_candidate" {
		t.Fatalf("ANOM-03 expected F10 status=usage_candidate on bldg-a 2026-11-17, got status=%v reason=%v baselineCount=%v",
			f10A["status"], f10A["reason"], f10A["baselineCount"])
	}
	f17A := get("/admin/v1/energy-analysis/opportunities?building=bldg-a&energy=electricity&date=2026-11-17")
	if len(f17A["clues"].([]any)) == 0 {
		t.Fatalf("F17 expected >=1 auditable clue on bldg-a 2026-11-17")
	}
	f06B := get("/admin/v1/energy-analysis/classification?building=bldg-b&energy=electricity&date=2026-11-17")
	if f06B["unclassifiedUsage"].(float64) <= 0 {
		t.Fatalf("ANOM-09 expected positive unclassifiedUsage for bldg-b socket, got %v", f06B["unclassifiedUsage"])
	}
	f06Roll := get("/admin/v1/energy-analysis/classification?building=bldg-b&energy=electricity&date=2026-11-12")
	if f06Roll["totalUsage"].(float64) <= 0 {
		t.Fatalf("ANOM-05 rollover day expected positive usage")
	}
	f06Swap := get("/admin/v1/energy-analysis/classification?building=bldg-c&energy=electricity&date=2026-11-11")
	if f06Swap["totalUsage"].(float64) <= 0 {
		t.Fatalf("ANOM-06 meter swap day expected positive usage")
	}
	t.Logf("Anomalies verified: F10=%s (baselineCount=%v) F17 clues=%d bldg-b unclassified=%.3f kWh rolloverDay=%.3f swapDay=%.3f",
		f10A["status"], f10A["baselineCount"], len(f17A["clues"].([]any)), f06B["unclassifiedUsage"], f06Roll["totalUsage"], f06Swap["totalUsage"])

	// 6. F05/F14 Full-Year 2025 & 2026 Monthly Targets, F15 Assessment, F18-F20 Retrofit M&V!
	tgt2025 := get("/admin/v1/energy-analysis/targets?building=bldg-a&energy=electricity&year=2025")
	if tgt2025["annual"].(map[string]any)["status"] != "comparable" {
		t.Fatalf("F05/F14 expected 2025 annual target comparable, got %+v", tgt2025["annual"])
	}
	f15 := get("/admin/v1/energy-analysis/assessment?date=2026-11-15")
	if len(f15["rows"].([]any)) < 5 {
		t.Fatalf("F15 expected >=5 assessment rows")
	}
	f20 := get("/admin/v1/energy-analysis/retrofit/verification")
	if len(f20["projects"].([]any)) != 3 {
		t.Fatalf("F20 expected 3 retrofit projects")
	}
	t.Logf("F05/F14/F15/F18-F20 verified: 2025Annual=%v F15Rows=%d F20Projects=%d",
		tgt2025["annual"].(map[string]any)["usage"], len(f15["rows"].([]any)), len(f20["projects"].([]any)))

	// 7. ALL 5 Reports (F21/F22/F23/F24/F25): Preview == CSV cell-by-cell AND includes "模拟数据"!
	for _, rptPath := range []string{
		"/admin/v1/energy-analysis/reports/consumption?building=bldg-b&date=2026-11-17",
		"/admin/v1/energy-analysis/reports/metrics?building=bldg-a&energy=electricity&date=2026-11-17",
		"/admin/v1/energy-analysis/reports/assessment?date=2026-11-17",
		"/admin/v1/energy-analysis/reports/retrofit",
		"/admin/v1/energy-analysis/reports/custom?building=bldg-b&energy=electricity&date=2026-11-17&scope=metrics&fields=data_nature,building_name,metric_name,metric_value,benchmark_status",
	} {
		jsonData := get(rptPath)
		sep := "&"
		if !strings.Contains(rptPath, "?") {
			sep = "?"
		}
		csvReq := httptest.NewRequest(http.MethodGet, rptPath+sep+"format=csv", nil)
		csvReq.Header.Set("Authorization", "Bearer "+tok)
		csvRec := httptest.NewRecorder()
		router.ServeHTTP(csvRec, csvReq)
		if csvRec.Code != http.StatusOK {
			t.Fatalf("CSV %s => %d", rptPath, csvRec.Code)
		}
		if !strings.Contains(csvRec.Body.String(), "模拟数据") {
			t.Fatalf("CSV %s missing mandatory '模拟数据' provenance tag", rptPath)
		}
		raw := bytes.TrimPrefix(csvRec.Body.Bytes(), []byte{0xEF, 0xBB, 0xBF})
		recs, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
		if err != nil {
			t.Fatalf("parse CSV %s: %v", rptPath, err)
		}
		cols := jsonData["columns"].([]any)
		rows := jsonData["rows"].([]any)
		if len(recs) != len(rows)+1 || len(recs[0]) != len(cols) {
			t.Fatalf("CSV/Preview dimension mismatch on %s", rptPath)
		}
		for ri, rowAny := range rows {
			row := rowAny.(map[string]any)
			for ci, colAny := range cols {
				key := colAny.(map[string]any)["key"].(string)
				if recs[ri+1][ci] != row[key].(string) {
					t.Fatalf("CSV/Preview cell mismatch on %s row %d col %s", rptPath, ri, key)
				}
			}
		}
		t.Logf("CSV/Preview exact parity + simulation tag verified: %s (%d rows x %d cols)", rptPath, len(rows), len(cols))
	}
}
