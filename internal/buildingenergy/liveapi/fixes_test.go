package liveapi

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/authguard"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/pgadapter"
)

func TestCarbonEstimationAndWaterExclusion(t *testing.T) {
	elecUse := 1000.0
	elec := EstimateCarbon(buildingenergy.Electricity, "kWh", &elecUse)
	if elec.Status != "calculated" || elec.KgCO2e == nil || math.Abs(*elec.KgCO2e-570.3) > 0.001 {
		t.Fatalf("unexpected electricity carbon: %+v", elec)
	}

	coolUse := 10.0
	cool := EstimateCarbon(buildingenergy.Cooling, "GJ", &coolUse)
	heat := EstimateCarbon(buildingenergy.Heat, "GJ", &coolUse)
	if cool.KgCO2e == nil || heat.KgCO2e == nil || math.Abs(*cool.KgCO2e-528.0) > 0.001 || math.Abs(*heat.KgCO2e-1100.0) > 0.001 {
		t.Fatalf("expected distinct cooling (528) vs heat (1100), got cool=%+v heat=%+v", cool, heat)
	}

	waterUse := 50.0
	water := EstimateCarbon(buildingenergy.Water, "m³", &waterUse)
	if water.IncludedInTotal || water.KgCO2e != nil || water.Status != "excluded_water" {
		t.Fatalf("water must be excluded from energy carbon total: %+v", water)
	}

	missingGas := EstimateCarbon(buildingenergy.Gas, "Nm³", nil)
	if missingGas.KgCO2e != nil || missingGas.Status != "missing_usage" {
		t.Fatalf("nil usage must never produce 0 carbon: %+v", missingGas)
	}

	rate1 := 1.0
	sum := SummarizeCarbon([]LiveOverviewEnergyCard{
		{Energy: buildingenergy.Electricity, Unit: "kWh", Usage: &elecUse, Coverage: LiveCoverage{Rate: &rate1}, Carbon: &elec},
		{Energy: buildingenergy.Water, Unit: "m³", Usage: &waterUse, Coverage: LiveCoverage{Rate: &rate1}, Carbon: &water},
		{Energy: buildingenergy.Gas, Unit: "Nm³", Usage: nil, Coverage: LiveCoverage{}, Carbon: &missingGas},
	})
	if sum.Status != "partial" || sum.TotalKgCO2e == nil || math.Abs(*sum.TotalKgCO2e-570.3) > 0.001 {
		t.Fatalf("expected partial summary 570.3, got %+v", sum)
	}
	if len(sum.ExcludedEnergies) != 1 || sum.ExcludedEnergies[0] != "water" || len(sum.MissingEnergies) != 1 {
		t.Fatalf("unexpected summary lists: %+v", sum)
	}
}

func TestAssessmentPenaltySlopesAndCSVFormulaSanitization(t *testing.T) {
	if got := assessmentPenaltySlope("每超 1% 扣 2 分"); got != 200 {
		t.Fatalf("expected 200, got %v", got)
	}
	if got := assessmentPenaltySlope("餐饮用气考核（每超 1% 扣 1 分）"); got != 100 {
		t.Fatalf("expected 100, got %v", got)
	}
	if got := assessmentPenaltySlope("严格考核 3x"); got != 300 {
		t.Fatalf("expected 300, got %v", got)
	}

	if sanitizeCSVCell("-29.6%") != "-29.6%" {
		t.Fatalf("signed percentage must remain unchanged")
	}
	if sanitizeCSVCell("-12.345") != "-12.345" {
		t.Fatalf("signed number must remain unchanged")
	}
	if sanitizeCSVCell("=CMD|' /C calc'!A0") != "'=CMD|' /C calc'!A0" {
		t.Fatalf("formula payload must be quoted")
	}
	if sanitizeCSVCell("+SUM(A1:A2)") != "'+SUM(A1:A2)" {
		t.Fatalf("plus-prefixed formula must be quoted")
	}
}

func TestLiveCSVExportPermissionAndF15F20Fixes(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("IOT_SIM_VERIFY_DSN"))
	if dsn == "" {
		t.Skip("IOT_SIM_VERIFY_DSN unset")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	secret := "sim-csv-auth-test-secret-key-2026"
	project := buildingenergy.ProjectKey{TenantID: "default", ProjectID: "iot-local"}
	rev := NewMemoryRevocationStore()
	validFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	// Subject A: ReadUsage ONLY (NO DownloadReport / ExportUsage!)
	// Subject B: ReadUsage + DownloadReport + ExportUsage
	policy := &authguard.ScopedPolicy{
		Revocations: rev,
		Grants: []authguard.Grant{
			{
				Subject:          buildingenergy.Subject{Issuer: "https://sim.local", ID: "preview-only-user"},
				Actions:          []buildingenergy.Action{buildingenergy.ReadUsage},
				Project:          project,
				AllowProjectWide: true,
				AllowAllPoints:   true,
				ValidRange:       buildingenergy.Range{From: validFrom},
			},
			{
				Subject:          buildingenergy.Subject{Issuer: "https://sim.local", ID: "export-user"},
				Actions:          []buildingenergy.Action{buildingenergy.ReadUsage, buildingenergy.DownloadReport, buildingenergy.ExportUsage},
				Project:          project,
				AllowProjectWide: true,
				AllowAllPoints:   true,
				ValidRange:       buildingenergy.Range{From: validFrom},
			},
		},
	}
	guard := &authguard.Guard{
		Authenticator: &HMACBearerAuthenticator{Secret: []byte(secret)},
		Identity: authguard.ContextIdentitySource{
			AllowedIssuers:   []string{"https://sim.local"},
			ExpectedAudience: "iot-energy",
			MaxCredentialTTL: time.Hour,
			Revocations:      rev,
			Now:              time.Now,
		},
		Policy: policy,
	}
	svc := &Service{DB: pgadapter.NewPGPool(pool), Guard: guard, Project: project, Timezone: "Asia/Shanghai", Now: time.Now}
	handler := NewRouter(guard, svc)

	now := time.Now().UTC()
	previewTok, _ := SignHMACToken(TokenClaims{
		Issuer: "https://sim.local", Subject: "preview-only-user", SessionID: "s1", Audience: "iot-energy",
		IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(30 * time.Minute).Unix(),
	}, []byte(secret))
	exportTok, _ := SignHMACToken(TokenClaims{
		Issuer: "https://sim.local", Subject: "export-user", SessionID: "s2", Audience: "iot-energy",
		IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(30 * time.Minute).Unix(),
	}, []byte(secret))

	// 1. Preview-only user CAN view JSON report (200), but MUST get 403 on CSV download!
	reqJSON := httptest.NewRequest(http.MethodGet, "/admin/v1/energy-analysis/reports/consumption?building=bldg-a&date=2026-11-17", nil)
	reqJSON.Header.Set("Authorization", "Bearer "+previewTok)
	rrJSON := httptest.NewRecorder()
	handler.ServeHTTP(rrJSON, reqJSON)
	if rrJSON.Code != http.StatusOK {
		t.Fatalf("preview-only user JSON report expected 200, got %d: %s", rrJSON.Code, rrJSON.Body.String())
	}

	for _, csvPath := range []string{
		"/admin/v1/energy-analysis/reports/consumption?building=bldg-a&date=2026-11-17&format=csv",
		"/admin/v1/energy-analysis/reports/metrics?building=bldg-a&energy=electricity&date=2026-11-17&format=csv",
		"/admin/v1/energy-analysis/reports/assessment?date=2026-11-17&format=csv",
		"/admin/v1/energy-analysis/reports/retrofit?format=csv",
		"/admin/v1/energy-analysis/reports/custom?building=bldg-a&energy=electricity&scope=metrics&format=csv",
	} {
		reqCSVDenied := httptest.NewRequest(http.MethodGet, csvPath, nil)
		reqCSVDenied.Header.Set("Authorization", "Bearer "+previewTok)
		rrDenied := httptest.NewRecorder()
		handler.ServeHTTP(rrDenied, reqCSVDenied)
		if rrDenied.Code != http.StatusForbidden {
			t.Fatalf("preview-only user CSV download (%s) expected 403 Forbidden, got %d: %s", csvPath, rrDenied.Code, rrDenied.Body.String())
		}
	}

	// 2. Authorized export user CAN download CSV (200) and CSV includes carbon column!
	reqCSVOK := httptest.NewRequest(http.MethodGet, "/admin/v1/energy-analysis/reports/consumption?building=bldg-a&date=2026-11-17&format=csv", nil)
	reqCSVOK.Header.Set("Authorization", "Bearer "+exportTok)
	rrOK := httptest.NewRecorder()
	handler.ServeHTTP(rrOK, reqCSVOK)
	if rrOK.Code != http.StatusOK || !strings.Contains(rrOK.Body.String(), "模拟参考碳排(kgCO2e)") {
		t.Fatalf("authorized user CSV expected 200 with carbon column, got %d", rrOK.Code)
	}

	// 3. Verify F15 weighted total + formula detail & F20 coverage + carbon reduction on live DB!
	pCtx := authguard.WithPrincipal(ctx, authguard.Principal{
		Subject: buildingenergy.Subject{Issuer: "https://sim.local", ID: "export-user"}, Audience: "iot-energy",
		SessionID: "s2", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(30 * time.Minute),
	})
	as, err := svc.Assessment(pCtx, "2026-11-17")
	if err != nil {
		t.Fatalf("Assessment: %v", err)
	}
	if as.WeightedTotalScore == nil || as.TotalWeightPct <= 0 {
		t.Fatalf("expected non-nil WeightedTotalScore, got %+v", as)
	}
	for _, r := range as.Rows {
		if r.FormulaDetail == "" {
			t.Fatalf("expected FormulaDetail on %s", r.RuleID)
		}
	}

	rf, err := svc.RetrofitProjects(pCtx, "F20")
	if err != nil {
		t.Fatalf("RetrofitProjects: %v", err)
	}
	verifiedIdx := -1
	for i := range rf.Projects {
		if rf.Projects[i].MVStatus == "verified_simulated" {
			verifiedIdx = i
			break
		}
	}
	if verifiedIdx < 0 || rf.Projects[verifiedIdx].ReportActualCoverage.Rate == nil || rf.Projects[verifiedIdx].VerifiedCarbonReductionKgCO2 == nil {
		t.Fatalf("expected F20 verified_simulated project with report coverage & carbon reduction, got %+v", rf.Projects)
	}
	verifiedProj := rf.Projects[verifiedIdx]

	ov, err := svc.Overview(pCtx, "bldg-a", "2026-11-17", 1)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if ov.CarbonSummary == nil || ov.CarbonSummary.TotalKgCO2e == nil || *ov.CarbonSummary.TotalKgCO2e <= 0 {
		t.Fatalf("expected F01 CarbonSummary total, got %+v", ov.CarbonSummary)
	}
	t.Logf("ALL 5 FIXES LIVE VERIFIED: CSV 403/200 enforced (F21-F25) | F01 Carbon=%.2f kgCO2e (%s) | F15 WeightedScore=%.1f | F20 (%s) ReportCov=%.1f%% CarbonRed=%.2f kgCO2e",
		*ov.CarbonSummary.TotalKgCO2e, ov.CarbonSummary.Status, *as.WeightedTotalScore, verifiedProj.RetrofitID, *verifiedProj.ReportActualCoverage.Rate*100, *verifiedProj.VerifiedCarbonReductionKgCO2)
}
