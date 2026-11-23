package liveapi

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/authguard"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/pgadapter"
)

const selectDailySnapshotSeriesSQL = `
SELECT segment_start, value_num, quality
FROM snapshot_coarse
WHERE terminal_id = $1 AND item_code = $2
  AND granularity = 'day'
  AND segment_start >= $3 AND segment_start <= $4
ORDER BY segment_start ASC`

// computeDailySnapshotBuildingUsage calculates authorized building consumption
// across any multi-day/monthly/annual range [from, to) using snapshot_coarse
// granularity='day', honoring configured multiplier, register_modulus rollovers,
// and confirmed meter_swap events in energy_domain.meter_register_event.
// Because daily snapshots close on the exact same cumulative register as hourly
// snapshots, monthly & annual sums reconcile with hourly totals with zero double counting.
func (s *Service) computeDailySnapshotBuildingUsage(
	ctx context.Context,
	st *liveState,
	bldg BuildingProfile,
	energy buildingenergy.EnergyType,
	rng buildingenergy.Range,
	asOf time.Time,
) (*float64, LiveCoverage, bool, error) {
	return s.computeDailySnapshotScopeUsage(
		ctx, st, buildingenergy.Building,
		buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID},
		energy, rng, asOf,
	)
}

func (s *Service) computeDailySnapshotScopeUsage(
	ctx context.Context,
	st *liveState,
	view buildingenergy.ObjectKind,
	obj buildingenergy.ObjectKey,
	energy buildingenergy.EnergyType,
	rng buildingenergy.Range,
	asOf time.Time,
) (*float64, LiveCoverage, bool, error) {
	if s.Guard == nil {
		return nil, LiveCoverage{}, false, buildingenergy.ErrAccessDenied
	}
	if _, err := s.Guard.AuthorizeScope(ctx, st.catalog, buildingenergy.ReadUsage, buildingenergy.Scope{
		Project: s.Project, Period: rng, Objects: []buildingenergy.ObjectKey{obj},
	}); err != nil {
		return nil, LiveCoverage{}, false, err
	}
	pts := st.configuredPointsForScope(view, []buildingenergy.ObjectKey{obj}, energy, rng)
	if len(pts) == 0 {
		return nil, LiveCoverage{}, false, nil
	}

	loc, err := time.LoadLocation(s.timezone())
	if err != nil {
		return nil, LiveCoverage{}, false, err
	}
	fromLocal := rng.From.In(loc)
	toLocal := rng.To.In(loc)
	expectedDays := int(math.Round(toLocal.Sub(fromLocal).Hours() / 24.0))
	if expectedDays <= 0 {
		return nil, LiveCoverage{}, false, nil
	}
	openDay := fromLocal.AddDate(0, 0, -1)
	lastDay := toLocal.AddDate(0, 0, -1)

	evResolver := st.registerEventResolver()
	var totalSum float64
	var totalExpected, totalValid, totalMissing, totalSuspect float64
	hasAnyValid := false

	for _, pm := range pts {
		mult := 1.0
		var modulus float64
		defaultEpoch := "epoch-init"
		if len(pm.Profiles) > 0 {
			if pm.Profiles[0].Multiplier > 0 {
				mult = pm.Profiles[0].Multiplier
			}
			if pm.Profiles[0].RegisterModulus != nil && *pm.Profiles[0].RegisterModulus > 0 {
				modulus = *pm.Profiles[0].RegisterModulus
			}
			if pm.Profiles[0].RegisterEpoch != "" {
				defaultEpoch = pm.Profiles[0].RegisterEpoch
			}
		}

		rows, err := s.DB.Query(ctx, selectDailySnapshotSeriesSQL, pm.Key.TerminalID, pm.Key.ItemCode, openDay, lastDay)
		if err != nil {
			return nil, LiveCoverage{}, false, err
		}
		type daySample struct {
			at  time.Time
			val *float64
			q   int16
		}
		byDay := map[string]daySample{}
		for rows.Next() {
			var seg time.Time
			var val *float64
			var q int16
			if err := rows.Scan(&seg, &val, &q); err == nil {
				endAt := seg.In(loc).AddDate(0, 0, 1)
				if !endAt.After(asOf) {
					byDay[seg.In(loc).Format("2006-01-02")] = daySample{at: endAt, val: val, q: q}
				}
			}
		}
		rows.Close()

		pointRef := buildingenergy.PointRef{Project: s.Project, Key: pm.Key}
		for d := 0; d < expectedDays; d++ {
			totalExpected++
			curDay := fromLocal.AddDate(0, 0, d)
			prevDay := curDay.AddDate(0, 0, -1)
			sPrev, okPrev := byDay[prevDay.Format("2006-01-02")]
			sCur, okCur := byDay[curDay.Format("2006-01-02")]
			if !okPrev || !okCur || sPrev.val == nil || sCur.val == nil {
				totalMissing++
				continue
			}
			if sPrev.q != 0 || sCur.q != 0 {
				totalSuspect++
				continue
			}
			epPrev, _, _ := evResolver.ResolveRegisterState(ctx, pointRef, sPrev.at, sPrev.at, defaultEpoch)
			epCur, _, _ := evResolver.ResolveRegisterState(ctx, pointRef, sCur.at, sCur.at, defaultEpoch)
			rawDiff := *sCur.val - *sPrev.val
			if epCur != epPrev {
				// Meter swap within this day: new meter started at 0, so closing reading is valid post-swap usage.
				if *sCur.val >= 0 {
					rawDiff = *sCur.val
				} else {
					totalSuspect++
					continue
				}
			} else if rawDiff < 0 {
				if modulus > 0 && *sPrev.val <= modulus && *sCur.val < modulus {
					rawDiff = (modulus - *sPrev.val) + *sCur.val
				} else {
					totalSuspect++
					continue
				}
			}
			totalSum += rawDiff * mult
			totalValid++
			hasAnyValid = true
		}
	}

	cov := makeCoverage(buildingenergy.WeightedCoverage{
		Expected: totalExpected, Valid: totalValid, Missing: totalMissing, Suspect: totalSuspect,
	})
	if !hasAnyValid {
		return nil, cov, true, nil
	}
	v := math.Round(totalSum*1000) / 1000
	return &v, cov, true, nil
}

// ── F15 组织与区域考核 (PRD §6.3) ────────────────────────────────────────────

func (s *Service) Assessment(ctx context.Context, dateStr string) (LiveAssessmentResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveAssessmentResponse{}, err
	}
	win, err := s.resolveWindow(st, dateStr, 1)
	if err != nil {
		return LiveAssessmentResponse{}, err
	}
	if s.Guard == nil {
		return LiveAssessmentResponse{}, buildingenergy.ErrAccessDenied
	}
	if _, err := s.Guard.AuthorizeScope(ctx, st.catalog, buildingenergy.ReadUsage, buildingenergy.Scope{
		Project: s.Project, Period: win.Range,
	}); err != nil {
		return LiveAssessmentResponse{}, err
	}

	// Cache F07 dimension usage per (ObjectKind, EnergyType) to guarantee exact reconciliation with F07!
	type dimKey struct {
		view   buildingenergy.ObjectKind
		energy buildingenergy.EnergyType
	}
	dimCache := map[dimKey]map[string]LiveDimensionRow{}
	lookupDim := func(view buildingenergy.ObjectKind, energy buildingenergy.EnergyType, objID string) (LiveDimensionRow, bool) {
		k := dimKey{view, energy}
		byID, ok := dimCache[k]
		if !ok {
			rep, err := s.Dimensions(ctx, view, energy, dateStr, 1)
			byID = map[string]LiveDimensionRow{}
			if err == nil {
				for _, r := range rep.Rows {
					byID[r.ID] = r
				}
			}
			dimCache[k] = byID
		}
		row, exists := byID[objID]
		return row, exists
	}

	nameLookup := func(kind buildingenergy.ObjectKind, id string) string {
		switch kind {
		case buildingenergy.Organization:
			for _, o := range st.orgsEffectiveAt(win.From) {
				if o.OrgID == id {
					return o.Name
				}
			}
		case buildingenergy.Space:
			for _, sp := range st.spacesEffectiveAt(win.From) {
				if sp.SpaceID == id {
					return sp.Name
				}
			}
		case buildingenergy.Building:
			if b, ok := st.buildingEffectiveAt(id, win.From); ok {
				return b.Name
			}
		}
		return id
	}

	rows := make([]LiveAssessmentRow, 0, len(st.assessmentRules))
	var totalWeight float64
	var weightedSum float64
	hasBlocked := false
	hasScored := false

	for _, rule := range st.assessmentRules {
		if !rangeCoversInstant(rule.EffectiveFrom, rule.EffectiveTo, win.From) {
			continue
		}
		dimRow, found := lookupDim(rule.ObjectKind, rule.Energy, rule.ObjectID)
		row := LiveAssessmentRow{
			RuleID:         rule.RuleID,
			ObjectKind:     rule.ObjectKind,
			ObjectID:       rule.ObjectID,
			ObjectName:     nameLookup(rule.ObjectKind, rule.ObjectID),
			Energy:         rule.Energy,
			MetricLabel:    rule.MetricLabel,
			TargetValue:    rule.TargetValue,
			Unit:           rule.Unit,
			WeightPct:      rule.WeightPct,
			PublicSharePct: rule.PublicSharePct,
			Exempt:         rule.Exempt,
			ExemptReason:   rule.ExemptReason,
			ReviewStatus:   rule.ReviewStatus,
			ReviewNote:     rule.ReviewNote,
			ScoringFormula: rule.ScoringFormula,
		}
		if found {
			row.ActualUsage = dimRow.Usage
			row.Coverage = dimRow.Coverage
		}
		covOK := row.Coverage.Rate != nil && *row.Coverage.Rate >= rule.MinCoverageRate && row.ActualUsage != nil
		if rule.Exempt {
			row.Confirmable = true
			row.ReviewStatus = "confirmed"
			row.FormulaDetail = "豁免对象，不计入扣分与加权评分"
		} else if !covOK {
			// PRD §6.3: missing data / insufficient coverage strictly prohibits formal confirmation & scoring!
			hasBlocked = true
			row.Confirmable = false
			row.ReviewStatus = "blocked_low_quality"
			row.ReviewNote = fmt.Sprintf("数据覆盖率不足（要求 ≥ %.0f%%）或缺少有效读数，依据 PRD §6.3 禁止正式确认与评分", rule.MinCoverageRate*100)
			row.FormulaDetail = "数据覆盖率未达门槛，禁止输出评分"
		} else {
			row.Confirmable = true
			dev := math.Round((*row.ActualUsage-rule.TargetValue)*1000) / 1000
			if math.Abs(dev) < 1e-6 {
				dev = 0
			}
			row.Deviation = &dev
			if rule.TargetValue > 0 {
				dr := dev / rule.TargetValue
				// Normalize tiny negative fractions so UI/CSV never displays "-0%".
				if math.Abs(dr) < 0.0005 {
					dr = 0
				}
				row.DeviationRate = &dr
				penaltySlope := assessmentPenaltySlope(rule.ScoringFormula)
				score := 100.0
				if dr > 0 {
					score = math.Max(0, 100.0-dr*penaltySlope)
				}
				score = math.Round(score*10) / 10
				row.Score = &score

				if rule.WeightPct > 0 {
					ws := math.Round((score*rule.WeightPct/100.0)*100) / 100
					row.WeightedScore = &ws
					totalWeight += rule.WeightPct
					weightedSum += ws
					hasScored = true
				}
				if dr <= 0 {
					row.FormulaDetail = fmt.Sprintf("实际 %.3f ≤ 目标 %.3f → 满分 100.0（斜率 %.0f 分/100%%，权重 %.0f%%）", *row.ActualUsage, rule.TargetValue, penaltySlope, rule.WeightPct)
				} else {
					row.FormulaDetail = fmt.Sprintf("max(0, 100 - %.2f%% × %.0f) = %.1f 分（权重 %.0f%%）", dr*100, penaltySlope, score, rule.WeightPct)
				}
			}
		}
		rows = append(rows, row)
	}

	summaryStatus := "empty"
	var weightedTotal *float64
	if len(rows) > 0 {
		if hasBlocked {
			summaryStatus = "partial_blocked"
		} else if hasScored && totalWeight > 0 {
			summaryStatus = "complete"
			norm := math.Round((weightedSum/totalWeight*100.0)*10) / 10
			weightedTotal = &norm
		} else {
			summaryStatus = "complete"
		}
	}

	return LiveAssessmentResponse{
		DataMode:           "live_database",
		DataOriginNotice:   st.dataOriginNotice,
		Dataset:            st.dataset,
		Timezone:           win.Timezone,
		From:               win.From,
		To:                 win.To,
		AsOf:               win.AsOf,
		TotalWeightPct:     math.Round(totalWeight*10) / 10,
		WeightedTotalScore: weightedTotal,
		SummaryStatus:      summaryStatus,
		Rows:               rows,
		Disclaimer:         "F15 组织与区域考核直接复用 F07 实时 Go 计算结果并按每条规则声明的扣分斜率与权重核算；缺测或低覆盖对象按 PRD §6.3 自动阻断正式确认与加权总分输出。",
	}, nil
}

// assessmentPenaltySlope resolves the penalty multiplier per 100% overrun from the rule formula text.
// Default is 200 (2 points deducted per 1% overrun); rules specifying "扣 1 分" / "1x" use 100.
func assessmentPenaltySlope(formula string) float64 {
	f := strings.ToLower(strings.TrimSpace(formula))
	if strings.Contains(f, "扣 1 分") || strings.Contains(f, "扣1分") || strings.Contains(f, "1x") {
		return 100.0
	}
	if strings.Contains(f, "扣 3 分") || strings.Contains(f, "扣3分") || strings.Contains(f, "3x") {
		return 300.0
	}
	return 200.0
}

// ReviewAssessment updates review_status (confirmed/pending_review/appealed) and review_note
// for an F15 assessment rule in energy_domain.assessment_rule, blocking "confirmed" if
// current coverage is below min_coverage_rate (PRD §6.3).
func (s *Service) ReviewAssessment(ctx context.Context, req ReviewAssessmentRequest) (map[string]any, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return nil, err
	}
	if !st.energyDomainReady {
		return nil, ErrEnergySchemaNotReady
	}
	ruleID := strings.TrimSpace(req.RuleID)
	status := strings.TrimSpace(req.ReviewStatus)
	if ruleID == "" {
		return nil, fmt.Errorf("%w: ruleId is required", ErrBadRequest)
	}
	switch status {
	case "confirmed", "pending_review", "appealed":
	default:
		return nil, fmt.Errorf("%w: reviewStatus must be confirmed, pending_review, or appealed", ErrBadRequest)
	}
	if status == "confirmed" {
		as, err := s.Assessment(ctx, "")
		if err != nil {
			return nil, err
		}
		for _, r := range as.Rows {
			if r.RuleID == ruleID && !r.Confirmable {
				return nil, fmt.Errorf("%w: cannot confirm %s while coverage is below threshold (%s)", ErrBadRequest, ruleID, r.ReviewNote)
			}
		}
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
UPDATE energy_domain.assessment_rule
SET review_status = $1, review_note = $2
WHERE tenant_id = $3 AND project_id = $4 AND rule_id = $5`,
		status, strings.TrimSpace(req.ReviewNote), s.Project.TenantID, s.Project.ProjectID, ruleID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"ruleId": ruleID, "reviewStatus": status, "reviewNote": strings.TrimSpace(req.ReviewNote)}, nil
}

// UpsertAssessmentRule creates or updates an F15 organization/space assessment rule.
func (s *Service) UpsertAssessmentRule(ctx context.Context, req UpsertAssessmentRuleRequest) (map[string]any, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return nil, err
	}
	if !st.energyDomainReady {
		return nil, ErrEnergySchemaNotReady
	}
	ruleID := strings.TrimSpace(req.RuleID)
	objID := strings.TrimSpace(req.ObjectID)
	if ruleID == "" || objID == "" || req.TargetValue <= 0 {
		return nil, fmt.Errorf("%w: ruleId, objectId, and positive targetValue are required", ErrBadRequest)
	}
	if req.ObjectKind != buildingenergy.Organization && req.ObjectKind != buildingenergy.Space && req.ObjectKind != buildingenergy.Building {
		return nil, fmt.Errorf("%w: invalid objectKind %q", ErrBadRequest, req.ObjectKind)
	}
	if req.Energy == "" {
		req.Energy = buildingenergy.Electricity
	}
	unit := strings.TrimSpace(req.Unit)
	if unit == "" {
		unit = canonicalUnitForEnergy(req.Energy)
	}
	minCov := req.MinCoverageRate
	if minCov <= 0 || minCov > 1 {
		minCov = 0.90
	}
	formula := strings.TrimSpace(req.ScoringFormula)
	if formula == "" {
		formula = "实际用量 ≤ 目标值获满分，每超 1% 扣 2 分"
	}
	eff, err := pgadapter.FormatTstzrange(buildingenergy.Range{From: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO energy_domain.assessment_rule
  (tenant_id, project_id, rule_id, revision, effective, object_kind, object_id, energy_type, metric_label,
   target_value, unit, weight_pct, public_share_pct, exempt, exempt_reason, scoring_formula, min_coverage_rate, review_status, review_note)
VALUES ($1, $2, $3, 'v1', $4::tstzrange, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, 'pending_review', '配置中心维护')
ON CONFLICT (tenant_id, project_id, rule_id, revision) DO UPDATE SET
  object_kind = EXCLUDED.object_kind,
  object_id = EXCLUDED.object_id,
  energy_type = EXCLUDED.energy_type,
  metric_label = EXCLUDED.metric_label,
  target_value = EXCLUDED.target_value,
  unit = EXCLUDED.unit,
  weight_pct = EXCLUDED.weight_pct,
  public_share_pct = EXCLUDED.public_share_pct,
  exempt = EXCLUDED.exempt,
  exempt_reason = EXCLUDED.exempt_reason,
  scoring_formula = EXCLUDED.scoring_formula,
  min_coverage_rate = EXCLUDED.min_coverage_rate`,
		s.Project.TenantID, s.Project.ProjectID, ruleID, eff, string(req.ObjectKind), objID, string(req.Energy),
		strings.TrimSpace(req.MetricLabel), req.TargetValue, unit, req.WeightPct, req.PublicSharePct,
		req.Exempt, strings.TrimSpace(req.ExemptReason), formula, minCov); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"ruleId": ruleID, "status": "saved"}, nil
}

// ── F18 / F19 / F20 节能改造评估与 M&V 核验 (PRD §7.2-§7.3) ──────────────────

const defaultMVMinCoverageRate = 0.90

func (s *Service) enrichRetrofitRecord(
	ctx context.Context,
	st *liveState,
	rp RetrofitProjectRecord,
	asOf time.Time,
) RetrofitProjectRecord {
	item := rp
	item.MinReportCoverageRate = defaultMVMinCoverageRate
	if hist := st.retrofitBaselineRevisions[rp.RetrofitID]; len(hist) > 0 {
		item.BaselineHistory = hist
	}
	if logs := st.retrofitVerificationLogs[rp.RetrofitID]; len(logs) > 0 {
		item.VerificationLogs = logs
	}

	bldg, ok := st.buildingEffectiveAt(rp.BuildingID, rp.ReportFrom)
	if !ok {
		for _, b := range st.buildings {
			if b.BuildingID == rp.BuildingID {
				bldg = b
				ok = true
				break
			}
		}
	}
	if ok {
		item.BuildingName = bldg.Name
	}

	scopeView := buildingenergy.Building
	scopeObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: rp.BuildingID}
	if strings.TrimSpace(rp.SpaceID) != "" {
		scopeView = buildingenergy.Space
		scopeObj = buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: rp.SpaceID}
		for _, sp := range st.spaces {
			if sp.SpaceID == rp.SpaceID {
				item.SpaceName = sp.Name
				break
			}
		}
	}

	if ok {
		if bUse, bCov, bFound, err := s.computeDailySnapshotScopeUsage(ctx, st, scopeView, scopeObj, rp.Energy, buildingenergy.Range{From: rp.BaselineFrom, To: rp.BaselineTo}, asOf); err == nil && bFound {
			item.BaselineActualUsage = bUse
			item.BaselineActualCoverage = bCov
			if bUse != nil && rp.BaselineTo.After(rp.BaselineFrom) {
				days := rp.BaselineTo.Sub(rp.BaselineFrom).Hours() / 24.0
				if days >= 1 {
					ann := math.Round((*bUse/days*365.0)*1000) / 1000
					item.AnnualizedBaselineUsage = &ann
					if ann > 0 {
						rLow := rp.EstSavingsLow / ann
						rBase := rp.EstSavingsBase / ann
						rHigh := rp.EstSavingsHigh / ann
						item.EstSavingsRateLow = &rLow
						item.EstSavingsRateBase = &rBase
						item.EstSavingsRateHigh = &rHigh
					}
				}
			}
		}
		if rUse, rCov, rFound, err := s.computeDailySnapshotScopeUsage(ctx, st, scopeView, scopeObj, rp.Energy, buildingenergy.Range{From: rp.ReportFrom, To: rp.ReportTo}, asOf); err == nil && rFound {
			item.ReportActualUsage = rUse
			item.ReportActualCoverage = rCov
		}
	}

	// F19 Potential & static payback (explicitly modeled estimates; missing price/capex MUST stay nil, never 0!).
	item.PriceConfigured = rp.RefUnitPrice > 0
	if item.PriceConfigured {
		cLow := math.Round(rp.EstSavingsLow*rp.RefUnitPrice*100) / 100
		cBase := math.Round(rp.EstSavingsBase*rp.RefUnitPrice*100) / 100
		cHigh := math.Round(rp.EstSavingsHigh*rp.RefUnitPrice*100) / 100
		item.EstAnnualCostLow = &cLow
		item.EstAnnualCostBase = &cBase
		item.EstAnnualCostHigh = &cHigh
		if rp.CapexBudget > 0 {
			if cLow > 0 {
				pb := math.Round((rp.CapexBudget/cLow)*100) / 100
				item.StaticPaybackLowYears = &pb
			}
			if cBase > 0 {
				pb := math.Round((rp.CapexBudget/cBase)*100) / 100
				item.StaticPaybackYears = &pb
			}
			if cHigh > 0 {
				pb := math.Round((rp.CapexBudget/cHigh)*100) / 100
				item.StaticPaybackHighYears = &pb
			}
		}
	}

	// F20 M&V Verification strictly follows PRD §7.2-§7.3:
	// verified_savings = adjusted_baseline - actual - non_routine_adjustment
	// Missing weather data, unlocked baseline, OR insufficient baseline/reporting coverage (<90%) MUST block verification!
	reportCovOK := item.ReportActualCoverage.Rate != nil && *item.ReportActualCoverage.Rate >= item.MinReportCoverageRate
	baselineCovOK := item.BaselineActualUsage == nil || (item.BaselineActualCoverage.Rate != nil && *item.BaselineActualCoverage.Rate >= item.MinReportCoverageRate)

	if rp.BaselineLocked && rp.WeatherDataComplete && rp.AdjustedBaselineUse != nil && item.ReportActualUsage != nil && reportCovOK && baselineCovOK {
		sav := math.Round((*rp.AdjustedBaselineUse-*item.ReportActualUsage-rp.NonRoutineAdjustment)*1000) / 1000
		item.VerifiedSavings = &sav
		if *rp.AdjustedBaselineUse > 0 {
			rate := sav / *rp.AdjustedBaselineUse
			item.VerifiedSavingsRate = &rate
		}
		if item.PriceConfigured {
			cost := math.Round(sav*rp.RefUnitPrice*100) / 100
			item.VerifiedCostSavings = &cost
		}
		cEst := EstimateCarbon(rp.Energy, rp.Unit, &sav)
		if cEst.KgCO2e != nil {
			item.VerifiedCarbonReductionKgCO2 = cEst.KgCO2e
		}
	} else {
		if rp.MVStatus == "verified_simulated" {
			item.MVStatus = "pending_verification"
		}
		if rp.BaselineLocked && rp.WeatherDataComplete && rp.AdjustedBaselineUse != nil && (!reportCovOK || !baselineCovOK) {
			repPct := 0.0
			if item.ReportActualCoverage.Rate != nil {
				repPct = *item.ReportActualCoverage.Rate * 100
			}
			item.MVNote = fmt.Sprintf("报告期实算覆盖率 %.1f%% 未达 M&V 最低门槛 %.0f%%（基线期覆盖率校验=%v），依据 PRD §7.3 禁止标记已核验。", repPct, item.MinReportCoverageRate*100, baselineCovOK)
		}
	}
	return item
}

func (s *Service) RetrofitProjects(ctx context.Context, section string) (LiveRetrofitResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveRetrofitResponse{}, err
	}
	if s.Guard == nil {
		return LiveRetrofitResponse{}, buildingenergy.ErrAccessDenied
	}
	win, err := s.resolveWindow(st, "", 1)
	if err != nil {
		return LiveRetrofitResponse{}, err
	}
	if _, err := s.Guard.AuthorizeScope(ctx, st.catalog, buildingenergy.ReadUsage, buildingenergy.Scope{
		Project: s.Project, Period: win.Range,
	}); err != nil {
		return LiveRetrofitResponse{}, err
	}

	projects := make([]RetrofitProjectRecord, 0, len(st.retrofits))
	for _, rp := range st.retrofits {
		projects = append(projects, s.enrichRetrofitRecord(ctx, st, rp, win.AsOf))
	}

	disclaimer := "本模块全部节能量、收益与模拟减碳参考均基于模拟数据集模型测算，不冒充真实改造工程验收成果；缺少基线、能源单价、气象因子或报告期覆盖率低于 90% 时严禁以 0 代替或标记为已核验。"
	return LiveRetrofitResponse{
		Section:          section,
		DataMode:         "live_database",
		DataOriginNotice: st.dataOriginNotice,
		Dataset:          st.dataset,
		Projects:         projects,
		Disclaimer:       disclaimer,
	}, nil
}

func principalActorSub(ctx context.Context) string {
	if p, ok := authguard.PrincipalFromContext(ctx); ok && strings.TrimSpace(p.Subject.ID) != "" {
		return strings.TrimSpace(p.Subject.ID)
	}
	return "admin"
}

// CreateRetrofitProject creates a new F18-F20 retrofit project and its initial baseline revision.
func (s *Service) CreateRetrofitProject(ctx context.Context, req CreateRetrofitRequest) (RetrofitProjectRecord, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	if !st.p2Ready || !st.retrofitLifecycleReady {
		return RetrofitProjectRecord{}, ErrEnergySchemaNotReady
	}
	rid := strings.TrimSpace(req.RetrofitID)
	if rid == "" {
		rid = fmt.Sprintf("ret-%d", s.now().UTC().Unix())
	}
	bldgID := strings.TrimSpace(req.BuildingID)
	spaceID := strings.TrimSpace(req.SpaceID)
	name := strings.TrimSpace(req.Name)
	measure := strings.TrimSpace(req.MeasureSummary)
	if bldgID == "" || name == "" || measure == "" {
		return RetrofitProjectRecord{}, fmt.Errorf("%w: buildingId, name, and measureSummary are required", ErrBadRequest)
	}
	bldgFound := false
	for _, b := range st.buildings {
		if b.BuildingID == bldgID {
			bldgFound = true
			break
		}
	}
	if !bldgFound {
		return RetrofitProjectRecord{}, fmt.Errorf("%w: unknown buildingId %q", ErrBadRequest, bldgID)
	}
	if spaceID != "" {
		spOK := false
		for _, sp := range st.spaces {
			if sp.SpaceID == spaceID && sp.BuildingID == bldgID {
				spOK = true
				break
			}
		}
		if !spOK {
			return RetrofitProjectRecord{}, fmt.Errorf("%w: spaceId %q does not belong to building %q", ErrBadRequest, spaceID, bldgID)
		}
	}
	if req.Energy == "" {
		req.Energy = buildingenergy.Electricity
	}
	unit := canonicalUnitForEnergy(req.Energy)
	if req.BaselineFrom.IsZero() || req.BaselineTo.IsZero() || !req.BaselineTo.After(req.BaselineFrom) {
		return RetrofitProjectRecord{}, fmt.Errorf("%w: baselineFrom must be before baselineTo", ErrBadRequest)
	}
	bVer := strings.TrimSpace(req.BaselineVersion)
	if bVer == "" {
		bVer = fmt.Sprintf("base-%s-v1", rid)
	}
	bModel := strings.TrimSpace(req.BaselineModel)
	if bModel == "" {
		bModel = "历史同口径日均基线模型"
	}
	bFactors := strings.TrimSpace(req.BaselineFactors)
	if bFactors == "" {
		bFactors = "工作日/节假日、季节气候"
	}
	rFrom := req.BaselineTo
	rTo := req.BaselineTo.AddDate(0, 1, 0)
	if req.ReportFrom != nil && !req.ReportFrom.IsZero() {
		rFrom = req.ReportFrom.UTC()
	}
	if req.ReportTo != nil && !req.ReportTo.IsZero() {
		rTo = req.ReportTo.UTC()
	}
	if !rTo.After(rFrom) {
		return RetrofitProjectRecord{}, fmt.Errorf("%w: reportFrom must be before reportTo", ErrBadRequest)
	}
	if req.EstSavingsLow < 0 || req.EstSavingsBase < 0 || req.EstSavingsHigh < 0 || req.RefUnitPrice < 0 || req.CapexBudget < 0 {
		return RetrofitProjectRecord{}, fmt.Errorf("%w: savings, price, and capex cannot be negative", ErrBadRequest)
	}

	// Precompute actual baseline usage & coverage from real PostgreSQL daily snapshots.
	win, _ := s.resolveWindow(st, "", 1)
	scopeView := buildingenergy.Building
	scopeObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldgID}
	if spaceID != "" {
		scopeView = buildingenergy.Space
		scopeObj = buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: spaceID}
	}
	bUse, bCov, _, _ := s.computeDailySnapshotScopeUsage(ctx, st, scopeView, scopeObj, req.Energy, buildingenergy.Range{From: req.BaselineFrom.UTC(), To: req.BaselineTo.UTC()}, win.AsOf)
	covRate := 0.0
	if bCov.Rate != nil {
		covRate = *bCov.Rate
	}
	dailyAvg := 0.0
	if bUse != nil {
		days := req.BaselineTo.Sub(req.BaselineFrom).Hours() / 24.0
		if days >= 1 {
			dailyAvg = math.Round((*bUse/days)*1000) / 1000
		}
	}

	mvNote := strings.TrimSpace(req.MVNote)
	if mvNote == "" {
		mvNote = "新建改造项目，基线尚未锁定，当前仅处于方案与基线建档阶段。"
	}
	actor := principalActorSub(ctx)

	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
INSERT INTO energy_domain.retrofit_project
  (tenant_id, project_id, retrofit_id, building_id, space_id, energy_type, name, measure_summary, unit,
   baseline_version, baseline_from, baseline_to, baseline_model, baseline_factors, baseline_r2, baseline_coverage, baseline_locked, baseline_daily_avg,
   est_savings_low, est_savings_base, est_savings_high, ref_unit_price, capex_budget,
   report_from, report_to, adjusted_baseline_use, non_routine_adjustment, weather_data_complete, mv_status, mv_note, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9,
        $10, $11, $12, $13, $14, $15, $16, false, $17,
        $18, $19, $20, $21, $22,
        $23, $24, $25, $26, $27, 'estimated_only', $28, clock_timestamp())`,
		s.Project.TenantID, s.Project.ProjectID, rid, bldgID, spaceID, string(req.Energy), name, measure, unit,
		bVer, req.BaselineFrom.UTC(), req.BaselineTo.UTC(), bModel, bFactors, req.BaselineR2, covRate, dailyAvg,
		req.EstSavingsLow, req.EstSavingsBase, req.EstSavingsHigh, req.RefUnitPrice, req.CapexBudget,
		rFrom, rTo, req.AdjustedBaselineUse, req.NonRoutineAdjustment, req.WeatherDataComplete, mvNote); err != nil {
		return RetrofitProjectRecord{}, fmt.Errorf("%w: create retrofit project failed: %v", ErrBadRequest, err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO energy_domain.retrofit_baseline_revision
  (tenant_id, project_id, retrofit_id, baseline_version, building_id, space_id, energy_type,
   baseline_from, baseline_to, baseline_model, baseline_factors, baseline_r2, baseline_coverage,
   baseline_locked, baseline_daily_avg, baseline_actual_usage, change_reason, actor_sub)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, false, $14, $15, '新建改造项目初始基线', $16)`,
		s.Project.TenantID, s.Project.ProjectID, rid, bVer, bldgID, spaceID, string(req.Energy),
		req.BaselineFrom.UTC(), req.BaselineTo.UTC(), bModel, bFactors, req.BaselineR2, covRate,
		dailyAvg, bUse, actor); err != nil {
		return RetrofitProjectRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RetrofitProjectRecord{}, err
	}

	fresh, err := s.loadState(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	for _, rp := range fresh.retrofits {
		if rp.RetrofitID == rid {
			return s.enrichRetrofitRecord(ctx, fresh, rp, win.AsOf), nil
		}
	}
	return RetrofitProjectRecord{}, ErrNotFound
}

// UpdateRetrofitProject edits basic info, F19 scenario parameters, or baseline definitions.
// Locked baselines CANNOT be overwritten in place: changing any baseline parameter on a locked
// project requires CreateNewBaselineVersion=true, creating a new revision while preserving history.
func (s *Service) UpdateRetrofitProject(ctx context.Context, req UpdateRetrofitRequest) (RetrofitProjectRecord, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	if !st.p2Ready || !st.retrofitLifecycleReady {
		return RetrofitProjectRecord{}, ErrEnergySchemaNotReady
	}
	rid := strings.TrimSpace(req.RetrofitID)
	var existing *RetrofitProjectRecord
	for i := range st.retrofits {
		if st.retrofits[i].RetrofitID == rid {
			existing = &st.retrofits[i]
			break
		}
	}
	if existing == nil {
		return RetrofitProjectRecord{}, ErrNotFound
	}

	next := *existing
	if s := strings.TrimSpace(req.Name); s != "" {
		next.Name = s
	}
	if s := strings.TrimSpace(req.MeasureSummary); s != "" {
		next.MeasureSummary = s
	}
	if req.EstSavingsLow != nil {
		if *req.EstSavingsLow < 0 {
			return RetrofitProjectRecord{}, fmt.Errorf("%w: estSavingsLow cannot be negative", ErrBadRequest)
		}
		next.EstSavingsLow = *req.EstSavingsLow
	}
	if req.EstSavingsBase != nil {
		if *req.EstSavingsBase < 0 {
			return RetrofitProjectRecord{}, fmt.Errorf("%w: estSavingsBase cannot be negative", ErrBadRequest)
		}
		next.EstSavingsBase = *req.EstSavingsBase
	}
	if req.EstSavingsHigh != nil {
		if *req.EstSavingsHigh < 0 {
			return RetrofitProjectRecord{}, fmt.Errorf("%w: estSavingsHigh cannot be negative", ErrBadRequest)
		}
		next.EstSavingsHigh = *req.EstSavingsHigh
	}
	if req.RefUnitPrice != nil {
		if *req.RefUnitPrice < 0 {
			return RetrofitProjectRecord{}, fmt.Errorf("%w: refUnitPrice cannot be negative", ErrBadRequest)
		}
		next.RefUnitPrice = *req.RefUnitPrice
	}
	if req.CapexBudget != nil {
		if *req.CapexBudget < 0 {
			return RetrofitProjectRecord{}, fmt.Errorf("%w: capexBudget cannot be negative", ErrBadRequest)
		}
		next.CapexBudget = *req.CapexBudget
	}

	baselineChanged := false
	if b := strings.TrimSpace(req.BuildingID); b != "" && b != existing.BuildingID {
		next.BuildingID = b
		baselineChanged = true
	}
	if req.SpaceID != existing.SpaceID && (req.SpaceID != "" || req.BuildingID != "" || req.CreateNewBaselineVersion) {
		next.SpaceID = strings.TrimSpace(req.SpaceID)
		baselineChanged = true
	}
	if req.Energy != "" && req.Energy != existing.Energy {
		next.Energy = req.Energy
		next.Unit = canonicalUnitForEnergy(req.Energy)
		baselineChanged = true
	}
	if req.BaselineFrom != nil && !req.BaselineFrom.IsZero() && !req.BaselineFrom.UTC().Equal(existing.BaselineFrom.UTC()) {
		next.BaselineFrom = req.BaselineFrom.UTC()
		baselineChanged = true
	}
	if req.BaselineTo != nil && !req.BaselineTo.IsZero() && !req.BaselineTo.UTC().Equal(existing.BaselineTo.UTC()) {
		next.BaselineTo = req.BaselineTo.UTC()
		baselineChanged = true
	}
	if m := strings.TrimSpace(req.BaselineModel); m != "" && m != existing.BaselineModel {
		next.BaselineModel = m
		baselineChanged = true
	}
	if f := strings.TrimSpace(req.BaselineFactors); f != "" && f != existing.BaselineFactors {
		next.BaselineFactors = f
		baselineChanged = true
	}
	if req.BaselineR2 != nil && (existing.BaselineR2 == nil || math.Abs(*req.BaselineR2-*existing.BaselineR2) > 1e-6) {
		next.BaselineR2 = req.BaselineR2
		baselineChanged = true
	}

	if !next.BaselineTo.After(next.BaselineFrom) {
		return RetrofitProjectRecord{}, fmt.Errorf("%w: baselineFrom must be before baselineTo", ErrBadRequest)
	}

	// Validate building/space pairing
	if next.SpaceID != "" {
		spOK := false
		for _, sp := range st.spaces {
			if sp.SpaceID == next.SpaceID && sp.BuildingID == next.BuildingID {
				spOK = true
				break
			}
		}
		if !spOK {
			return RetrofitProjectRecord{}, fmt.Errorf("%w: spaceId %q does not belong to building %q", ErrBadRequest, next.SpaceID, next.BuildingID)
		}
	}

	// Enforce locked-baseline immutability!
	if existing.BaselineLocked && baselineChanged && !req.CreateNewBaselineVersion {
		return RetrofitProjectRecord{}, fmt.Errorf("%w: 基线版本 %s 已锁定，禁止直接覆盖；请勾选“创建新基线版本”并填写变更原因以保留历史版本", ErrBadRequest, existing.BaselineVersion)
	}

	if req.CreateNewBaselineVersion {
		reason := strings.TrimSpace(req.ChangeReason)
		if reason == "" {
			return RetrofitProjectRecord{}, fmt.Errorf("%w: 创建新基线版本必须填写变更原因", ErrBadRequest)
		}
		newVer := strings.TrimSpace(req.NewBaselineVersion)
		if newVer == "" || newVer == existing.BaselineVersion {
			nextIdx := len(st.retrofitBaselineRevisions[rid]) + 1
			if nextIdx < 2 {
				nextIdx = 2
			}
			newVer = fmt.Sprintf("base-%s-v%d", rid, nextIdx)
		}
		next.BaselineVersion = newVer
		next.BaselineLocked = false // New baseline revision starts unlocked for verification review.
		if next.MVStatus == "verified_simulated" {
			next.MVStatus = "pending_verification"
		}
	}

	win, _ := s.resolveWindow(st, "", 1)
	scopeView := buildingenergy.Building
	scopeObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: next.BuildingID}
	if next.SpaceID != "" {
		scopeView = buildingenergy.Space
		scopeObj = buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: next.SpaceID}
	}
	bUse, bCov, _, _ := s.computeDailySnapshotScopeUsage(ctx, st, scopeView, scopeObj, next.Energy, buildingenergy.Range{From: next.BaselineFrom.UTC(), To: next.BaselineTo.UTC()}, win.AsOf)
	if bCov.Rate != nil {
		next.BaselineCoverage = *bCov.Rate
	}
	if bUse != nil {
		days := next.BaselineTo.Sub(next.BaselineFrom).Hours() / 24.0
		if days >= 1 {
			next.BaselineDailyAvg = math.Round((*bUse/days)*1000) / 1000
		}
	}

	actor := principalActorSub(ctx)
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
UPDATE energy_domain.retrofit_project
SET building_id = $1, space_id = $2, energy_type = $3, name = $4, measure_summary = $5, unit = $6,
    baseline_version = $7, baseline_from = $8, baseline_to = $9, baseline_model = $10, baseline_factors = $11,
    baseline_r2 = $12, baseline_coverage = $13, baseline_locked = $14, baseline_daily_avg = $15,
    est_savings_low = $16, est_savings_base = $17, est_savings_high = $18, ref_unit_price = $19, capex_budget = $20,
    mv_status = $21, updated_at = clock_timestamp()
WHERE tenant_id = $22 AND project_id = $23 AND retrofit_id = $24`,
		next.BuildingID, next.SpaceID, string(next.Energy), next.Name, next.MeasureSummary, next.Unit,
		next.BaselineVersion, next.BaselineFrom.UTC(), next.BaselineTo.UTC(), next.BaselineModel, next.BaselineFactors,
		next.BaselineR2, next.BaselineCoverage, next.BaselineLocked, next.BaselineDailyAvg,
		next.EstSavingsLow, next.EstSavingsBase, next.EstSavingsHigh, next.RefUnitPrice, next.CapexBudget,
		next.MVStatus, s.Project.TenantID, s.Project.ProjectID, rid); err != nil {
		return RetrofitProjectRecord{}, err
	}

	if req.CreateNewBaselineVersion {
		if _, err := tx.Exec(ctx, `
INSERT INTO energy_domain.retrofit_baseline_revision
  (tenant_id, project_id, retrofit_id, baseline_version, building_id, space_id, energy_type,
   baseline_from, baseline_to, baseline_model, baseline_factors, baseline_r2, baseline_coverage,
   baseline_locked, baseline_daily_avg, baseline_actual_usage, change_reason, actor_sub)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, false, $14, $15, $16, $17)`,
			s.Project.TenantID, s.Project.ProjectID, rid, next.BaselineVersion, next.BuildingID, next.SpaceID, string(next.Energy),
			next.BaselineFrom.UTC(), next.BaselineTo.UTC(), next.BaselineModel, next.BaselineFactors, next.BaselineR2, next.BaselineCoverage,
			next.BaselineDailyAvg, bUse, strings.TrimSpace(req.ChangeReason), actor); err != nil {
			return RetrofitProjectRecord{}, fmt.Errorf("%w: baseline version %q already exists or failed: %v", ErrBadRequest, next.BaselineVersion, err)
		}
	} else if baselineChanged && !existing.BaselineLocked {
		// Unlocked draft baseline revision can be updated in place before locking.
		if _, err := tx.Exec(ctx, `
INSERT INTO energy_domain.retrofit_baseline_revision
  (tenant_id, project_id, retrofit_id, baseline_version, building_id, space_id, energy_type,
   baseline_from, baseline_to, baseline_model, baseline_factors, baseline_r2, baseline_coverage,
   baseline_locked, baseline_daily_avg, baseline_actual_usage, change_reason, actor_sub)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, false, $14, $15, '编辑未锁定基线草案', $16)
ON CONFLICT (tenant_id, project_id, retrofit_id, baseline_version) DO UPDATE SET
  building_id = EXCLUDED.building_id, space_id = EXCLUDED.space_id, energy_type = EXCLUDED.energy_type,
  baseline_from = EXCLUDED.baseline_from, baseline_to = EXCLUDED.baseline_to,
  baseline_model = EXCLUDED.baseline_model, baseline_factors = EXCLUDED.baseline_factors,
  baseline_r2 = EXCLUDED.baseline_r2, baseline_coverage = EXCLUDED.baseline_coverage,
  baseline_daily_avg = EXCLUDED.baseline_daily_avg, baseline_actual_usage = EXCLUDED.baseline_actual_usage,
  recorded_at = clock_timestamp()`,
			s.Project.TenantID, s.Project.ProjectID, rid, next.BaselineVersion, next.BuildingID, next.SpaceID, string(next.Energy),
			next.BaselineFrom.UTC(), next.BaselineTo.UTC(), next.BaselineModel, next.BaselineFactors, next.BaselineR2, next.BaselineCoverage,
			next.BaselineDailyAvg, bUse, actor); err != nil {
			return RetrofitProjectRecord{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return RetrofitProjectRecord{}, err
	}
	fresh, err := s.loadState(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	for _, rp := range fresh.retrofits {
		if rp.RetrofitID == rid {
			return s.enrichRetrofitRecord(ctx, fresh, rp, win.AsOf), nil
		}
	}
	return RetrofitProjectRecord{}, ErrNotFound
}

// LockRetrofitBaseline locks the active baseline version and freezes its actual usage/coverage snapshot.
func (s *Service) LockRetrofitBaseline(ctx context.Context, req LockRetrofitBaselineRequest) (RetrofitProjectRecord, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	if !st.p2Ready || !st.retrofitLifecycleReady {
		return RetrofitProjectRecord{}, ErrEnergySchemaNotReady
	}
	rid := strings.TrimSpace(req.RetrofitID)
	var existing *RetrofitProjectRecord
	for i := range st.retrofits {
		if st.retrofits[i].RetrofitID == rid {
			existing = &st.retrofits[i]
			break
		}
	}
	if existing == nil {
		return RetrofitProjectRecord{}, ErrNotFound
	}
	win, _ := s.resolveWindow(st, "", 1)
	enriched := s.enrichRetrofitRecord(ctx, st, *existing, win.AsOf)
	if enriched.BaselineActualUsage == nil {
		return RetrofitProjectRecord{}, fmt.Errorf("%w: 基线统计周期内缺少有效实算数据，禁止锁定空基线", ErrBadRequest)
	}
	covRate := 0.0
	if enriched.BaselineActualCoverage.Rate != nil {
		covRate = *enriched.BaselineActualCoverage.Rate
	}
	reason := strings.TrimSpace(req.ChangeReason)
	if reason == "" {
		reason = "基线复核通过并锁定"
	}
	actor := principalActorSub(ctx)

	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
UPDATE energy_domain.retrofit_project
SET baseline_locked = true, baseline_coverage = $1, updated_at = clock_timestamp()
WHERE tenant_id = $2 AND project_id = $3 AND retrofit_id = $4`,
		covRate, s.Project.TenantID, s.Project.ProjectID, rid); err != nil {
		return RetrofitProjectRecord{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO energy_domain.retrofit_baseline_revision
  (tenant_id, project_id, retrofit_id, baseline_version, building_id, space_id, energy_type,
   baseline_from, baseline_to, baseline_model, baseline_factors, baseline_r2, baseline_coverage,
   baseline_locked, baseline_daily_avg, baseline_actual_usage, change_reason, actor_sub)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, true, $14, $15, $16, $17)
ON CONFLICT (tenant_id, project_id, retrofit_id, baseline_version) DO UPDATE SET
  baseline_locked = true, baseline_coverage = EXCLUDED.baseline_coverage,
  baseline_actual_usage = EXCLUDED.baseline_actual_usage, change_reason = EXCLUDED.change_reason,
  actor_sub = EXCLUDED.actor_sub, recorded_at = clock_timestamp()`,
		s.Project.TenantID, s.Project.ProjectID, rid, existing.BaselineVersion, existing.BuildingID, existing.SpaceID, string(existing.Energy),
		existing.BaselineFrom.UTC(), existing.BaselineTo.UTC(), existing.BaselineModel, existing.BaselineFactors, existing.BaselineR2, covRate,
		existing.BaselineDailyAvg, enriched.BaselineActualUsage, reason, actor); err != nil {
		return RetrofitProjectRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RetrofitProjectRecord{}, err
	}

	fresh, err := s.loadState(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	for _, rp := range fresh.retrofits {
		if rp.RetrofitID == rid {
			return s.enrichRetrofitRecord(ctx, fresh, rp, win.AsOf), nil
		}
	}
	return RetrofitProjectRecord{}, ErrNotFound
}

// RecordRetrofitVerification evaluates F20 M&V verification with the strict 90% coverage gate,
// locked-baseline gate, and weather-factor gate, and appends an auditable log entry.
func (s *Service) RecordRetrofitVerification(ctx context.Context, req VerifyRetrofitRequest) (RetrofitProjectRecord, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	if !st.p2Ready || !st.retrofitLifecycleReady {
		return RetrofitProjectRecord{}, ErrEnergySchemaNotReady
	}
	rid := strings.TrimSpace(req.RetrofitID)
	var existing *RetrofitProjectRecord
	for i := range st.retrofits {
		if st.retrofits[i].RetrofitID == rid {
			existing = &st.retrofits[i]
			break
		}
	}
	if existing == nil {
		return RetrofitProjectRecord{}, ErrNotFound
	}
	if req.ReportFrom.IsZero() || req.ReportTo.IsZero() || !req.ReportTo.After(req.ReportFrom) {
		return RetrofitProjectRecord{}, fmt.Errorf("%w: reportFrom must be before reportTo", ErrBadRequest)
	}

	candidate := *existing
	candidate.ReportFrom = req.ReportFrom.UTC()
	candidate.ReportTo = req.ReportTo.UTC()
	candidate.AdjustedBaselineUse = req.AdjustedBaselineUse
	candidate.NonRoutineAdjustment = req.NonRoutineAdjustment
	candidate.WeatherDataComplete = req.WeatherDataComplete
	if note := strings.TrimSpace(req.MVNote); note != "" {
		candidate.MVNote = note
	}

	win, _ := s.resolveWindow(st, "", 1)
	evaluated := s.enrichRetrofitRecord(ctx, st, candidate, win.AsOf)

	reportCovOK := evaluated.ReportActualCoverage.Rate != nil && *evaluated.ReportActualCoverage.Rate >= defaultMVMinCoverageRate
	baselineCovOK := evaluated.BaselineActualUsage == nil || (evaluated.BaselineActualCoverage.Rate != nil && *evaluated.BaselineActualCoverage.Rate >= defaultMVMinCoverageRate)
	canVerify := candidate.BaselineLocked && candidate.WeatherDataComplete && candidate.AdjustedBaselineUse != nil && evaluated.ReportActualUsage != nil && reportCovOK && baselineCovOK

	if req.MarkVerified && !canVerify {
		var reasons []string
		if !candidate.BaselineLocked {
			reasons = append(reasons, "基线尚未锁定")
		}
		if !candidate.WeatherDataComplete {
			reasons = append(reasons, "气象/运行修正因子不完整")
		}
		if candidate.AdjustedBaselineUse == nil {
			reasons = append(reasons, "缺少报告期调整后基线用量")
		}
		if evaluated.ReportActualUsage == nil || !reportCovOK {
			repPct := 0.0
			if evaluated.ReportActualCoverage.Rate != nil {
				repPct = *evaluated.ReportActualCoverage.Rate * 100
			}
			reasons = append(reasons, fmt.Sprintf("报告期实算覆盖率 %.1f%% 低于 90%% 门禁", repPct))
		}
		if !baselineCovOK {
			reasons = append(reasons, "基线期实算覆盖率低于 90% 门禁")
		}
		return RetrofitProjectRecord{}, fmt.Errorf("%w: 未满足 M&V 核验条件（%s），严禁标记为已核验", ErrBadRequest, strings.Join(reasons, "；"))
	}

	finalStatus := "pending_verification"
	if req.MarkVerified && canVerify {
		finalStatus = "verified_simulated"
	} else if !candidate.BaselineLocked && candidate.AdjustedBaselineUse == nil {
		finalStatus = "estimated_only"
	}
	finalNote := evaluated.MVNote
	if strings.TrimSpace(req.MVNote) != "" && canVerify {
		finalNote = strings.TrimSpace(req.MVNote)
	}
	actor := principalActorSub(ctx)

	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
UPDATE energy_domain.retrofit_project
SET report_from = $1, report_to = $2, adjusted_baseline_use = $3, non_routine_adjustment = $4,
    weather_data_complete = $5, mv_status = $6, mv_note = $7, updated_at = clock_timestamp()
WHERE tenant_id = $8 AND project_id = $9 AND retrofit_id = $10`,
		candidate.ReportFrom, candidate.ReportTo, candidate.AdjustedBaselineUse, candidate.NonRoutineAdjustment,
		candidate.WeatherDataComplete, finalStatus, finalNote, s.Project.TenantID, s.Project.ProjectID, rid); err != nil {
		return RetrofitProjectRecord{}, err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO energy_domain.retrofit_verification_log
  (tenant_id, project_id, retrofit_id, baseline_version, report_from, report_to,
   adjusted_baseline_use, report_actual_usage, report_coverage_rate, non_routine_adjustment,
   verified_savings, verified_savings_rate, verified_carbon_kgco2e, weather_data_complete,
   mv_status, mv_note, actor_sub)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		s.Project.TenantID, s.Project.ProjectID, rid, candidate.BaselineVersion, candidate.ReportFrom, candidate.ReportTo,
		candidate.AdjustedBaselineUse, evaluated.ReportActualUsage, evaluated.ReportActualCoverage.Rate, candidate.NonRoutineAdjustment,
		evaluated.VerifiedSavings, evaluated.VerifiedSavingsRate, evaluated.VerifiedCarbonReductionKgCO2, candidate.WeatherDataComplete,
		finalStatus, finalNote, actor); err != nil {
		return RetrofitProjectRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RetrofitProjectRecord{}, err
	}

	fresh, err := s.loadState(ctx)
	if err != nil {
		return RetrofitProjectRecord{}, err
	}
	for _, rp := range fresh.retrofits {
		if rp.RetrofitID == rid {
			return s.enrichRetrofitRecord(ctx, fresh, rp, win.AsOf), nil
		}
	}
	return RetrofitProjectRecord{}, ErrNotFound
}

// ── F23 组织考核报表 & F24 诊断与改造成效报告 (with exact CSV parity) ───────

func datasetNatureTag(ds *DatasetMeta) string {
	if ds != nil && ds.IsSimulated {
		return fmt.Sprintf("模拟数据 (%s / seed=%d)", ds.DatasetVersion, ds.Seed)
	}
	return "数据库实测"
}

func (s *Service) AssessmentReport(ctx context.Context, dateStr string) (LiveReportResponse, error) {
	as, err := s.Assessment(ctx, dateStr)
	if err != nil {
		return LiveReportResponse{}, err
	}
	nature := datasetNatureTag(as.Dataset)
	cols := []LiveReportColumn{
		{Key: "data_nature", Label: "数据属性"},
		{Key: "rule_id", Label: "考核规则"},
		{Key: "object_kind", Label: "对象层级"},
		{Key: "object_name", Label: "考核对象"},
		{Key: "energy", Label: "能源类型"},
		{Key: "target_value", Label: "考核目标"},
		{Key: "actual_usage", Label: "实际用量"},
		{Key: "unit", Label: "单位"},
		{Key: "deviation", Label: "偏差量"},
		{Key: "weight_pct", Label: "权重(%)"},
		{Key: "public_share_pct", Label: "公区分摊(%)"},
		{Key: "coverage_rate", Label: "覆盖率"},
		{Key: "score", Label: "考核得分"},
		{Key: "weighted_score", Label: "加权得分"},
		{Key: "review_status", Label: "复核状态"},
		{Key: "review_note", Label: "说明与申诉记录"},
	}
	rows := make([]map[string]string, 0, len(as.Rows))
	for _, r := range as.Rows {
		rows = append(rows, map[string]string{
			"data_nature":      nature,
			"rule_id":          r.RuleID,
			"object_kind":      string(r.ObjectKind),
			"object_name":      r.ObjectName,
			"energy":           string(r.Energy),
			"target_value":     fmt.Sprintf("%.3f", r.TargetValue),
			"actual_usage":     fmtFloatPtr(r.ActualUsage),
			"unit":             r.Unit,
			"deviation":        fmtFloatPtr(r.Deviation),
			"weight_pct":       fmt.Sprintf("%.1f", r.WeightPct),
			"public_share_pct": fmt.Sprintf("%.1f", r.PublicSharePct),
			"coverage_rate":    fmtRatePtr(r.Coverage.Rate),
			"score":            fmtFloatPtr(r.Score),
			"weighted_score":   fmtFloatPtr(r.WeightedScore),
			"review_status":    r.ReviewStatus,
			"review_note":      r.ReviewNote,
		})
	}
	return LiveReportResponse{
		ReportCode: "F23", ReportTitle: "组织考核报表",
		DataMode: "live_database", DataOriginNotice: as.DataOriginNotice, Dataset: as.Dataset,
		BuildingID: "all", BuildingName: "全部模拟园区", Energy: "all",
		Timezone: as.Timezone, From: as.From, To: as.To, AsOf: as.AsOf, GeneratedAt: s.now().UTC(),
		Columns: cols, Rows: rows,
		Disclaimer: "报表直接复用 F15 实时考核结果；低覆盖/缺数对象禁止正式确认与评分；预览与 CSV 导出逐字段一致。",
	}, nil
}

func (s *Service) RetrofitReport(ctx context.Context) (LiveReportResponse, error) {
	rf, err := s.RetrofitProjects(ctx, "F24")
	if err != nil {
		return LiveReportResponse{}, err
	}
	nature := datasetNatureTag(rf.Dataset)
	cols := []LiveReportColumn{
		{Key: "data_nature", Label: "数据属性"},
		{Key: "retrofit_id", Label: "项目编号"},
		{Key: "building_name", Label: "所属建筑"},
		{Key: "name", Label: "改造项目名称"},
		{Key: "energy", Label: "能源类型"},
		{Key: "baseline_version", Label: "基线版本"},
		{Key: "est_savings_base", Label: "年化估算节能量(基准)"},
		{Key: "static_payback_years", Label: "估算回收期(年)"},
		{Key: "adjusted_baseline_use", Label: "报告期调整基线"},
		{Key: "report_actual_usage", Label: "报告期实际用量"},
		{Key: "report_coverage_rate", Label: "报告期覆盖率"},
		{Key: "verified_savings", Label: "M&V核验节能量"},
		{Key: "verified_savings_rate", Label: "节能率"},
		{Key: "verified_carbon_kgco2e", Label: "模拟参考减碳(kgCO2e)"},
		{Key: "unit", Label: "单位"},
		{Key: "mv_status", Label: "核验状态"},
		{Key: "mv_note", Label: "核验边界与局限"},
	}
	rows := make([]map[string]string, 0, len(rf.Projects))
	for _, p := range rf.Projects {
		rows = append(rows, map[string]string{
			"data_nature":            nature,
			"retrofit_id":            p.RetrofitID,
			"building_name":          p.BuildingName,
			"name":                   p.Name,
			"energy":                 string(p.Energy),
			"baseline_version":       p.BaselineVersion,
			"est_savings_base":       fmt.Sprintf("%.3f", p.EstSavingsBase),
			"static_payback_years":   fmtFloatPtr(p.StaticPaybackYears),
			"adjusted_baseline_use":  fmtFloatPtr(p.AdjustedBaselineUse),
			"report_actual_usage":    fmtFloatPtr(p.ReportActualUsage),
			"report_coverage_rate":   fmtRatePtr(p.ReportActualCoverage.Rate),
			"verified_savings":       fmtFloatPtr(p.VerifiedSavings),
			"verified_savings_rate":  fmtRatePtr(p.VerifiedSavingsRate),
			"verified_carbon_kgco2e": fmtFloatPtr(p.VerifiedCarbonReductionKgCO2),
			"unit":                   p.Unit,
			"mv_status":              p.MVStatus,
			"mv_note":                p.MVNote,
		})
	}
	now := s.now().UTC()
	return LiveReportResponse{
		ReportCode: "F24", ReportTitle: "诊断与改造成效报告",
		DataMode: "live_database", DataOriginNotice: rf.DataOriginNotice, Dataset: rf.Dataset,
		BuildingID: "all", BuildingName: "全部模拟园区", Energy: "all",
		Timezone: s.timezone(), From: now.AddDate(0, -1, 0), To: now, AsOf: now, GeneratedAt: now,
		Columns: cols, Rows: rows,
		Disclaimer: "报表整合 F18 基线、F19 潜力测算与 F20 M&V 核验，全部属模拟数据模型测算，不冒充现场改造成果；预览与 CSV 导出逐字段一致。",
	}, nil
}
