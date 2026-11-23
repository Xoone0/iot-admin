package liveapi

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

// ── F05 年度目标摘要 & F14 目标与分期分解 ───────────────────────────────────

func (s *Service) Targets(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, year int) (LiveTargetResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveTargetResponse{}, err
	}
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	loc, err := time.LoadLocation(s.timezone())
	if err != nil {
		return LiveTargetResponse{}, err
	}
	if year <= 0 {
		win, err := s.resolveWindow(st, "", 1)
		if err != nil {
			return LiveTargetResponse{}, err
		}
		year = win.From.In(loc).Year()
	}
	yearStart := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
	bldg, err := s.selectBuildingAt(st, buildingID, yearStart)
	if err != nil {
		return LiveTargetResponse{}, err
	}
	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID}

	unit := canonicalUnitForEnergy(energy)
	scope := buildingenergy.TargetScope{
		Project:  s.Project,
		Object:   bldgObj,
		Energy:   energy,
		Unit:     unit,
		Year:     year,
		Timezone: s.timezone(),
	}

	// Build TargetVersion list from energy_domain.energy_target_revision.
	var versions []buildingenergy.TargetVersion
	for _, tr := range st.targets {
		if tr.ObjectKind != buildingenergy.Building || tr.ObjectID != bldg.BuildingID || tr.Energy != energy || tr.Year != year {
			continue
		}
		var weights [12]int64
		if tr.Method == buildingenergy.TargetWeighted && len(tr.WeightPPM) == 12 {
			copy(weights[:], tr.WeightPPM)
		}
		versions = append(versions, buildingenergy.TargetVersion{
			Scope: scope,
			Meta: buildingenergy.VersionMeta{
				Version:   tr.Revision,
				Effective: rangeFromBounds(tr.EffectiveFrom, tr.EffectiveTo),
				Source:    buildingenergy.SourceRef{System: tr.SourceSystem, ID: tr.Revision},
			},
			AnnualMilli: tr.AnnualMilli,
			Method:      tr.Method,
			WeightPPM:   weights,
		})
	}

	// Pick an evaluation instant inside the natural year (latest available data instant clamped into [yearStart, yearEnd)).
	latestWin, _ := s.resolveWindow(st, "", 1)
	evalAt := latestWin.From.In(loc)
	yearEnd := time.Date(year+1, 1, 1, 0, 0, 0, 0, loc)
	if evalAt.Before(yearStart) || !evalAt.Before(yearEnd) {
		evalAt = yearStart
	}

	plan, err := buildingenergy.PlanEnergyTarget(scope, evalAt, versions)
	if err != nil {
		return LiveTargetResponse{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}

	// Evaluate each completed month within the data horizon using real PostgreSQL daily snapshots
	// (reconciled 100% with hourly snapshots on the same cumulative register series, covering full 2025-2026!).
	asOf := latestWin.AsOf
	buildSyntheticReport := func(rng buildingenergy.Range, gran buildingenergy.Granularity, usage *float64, cov LiveCoverage) *buildingenergy.ClassifiedUsageReport {
		if usage == nil && cov.Expected == 0 {
			return nil
		}
		return &buildingenergy.ClassifiedUsageReport{
			Query: buildingenergy.ClassifiedUsageQuery{
				Project: s.Project,
				Window:  buildingenergy.Window{Range: rng, Timezone: s.timezone(), AsOf: asOf, Granularity: gran},
				Object:  bldgObj,
			},
			RuleVersion: "m1-c-v1",
			Energy: []buildingenergy.EnergyCategorySummary{{
				Energy: energy,
				Unit:   unit,
				Total: buildingenergy.CategoryValue{
					Usage: usage,
					Coverage: buildingenergy.WeightedCoverage{
						Expected: cov.Expected, Valid: cov.Valid, Missing: cov.Missing, Suspect: cov.Suspect,
					},
				},
			}},
		}
	}

	monthRows := make([]LiveTargetPeriodRow, 0, 12)
	for m := 1; m <= 12; m++ {
		mp := plan.Months[m-1]
		var repPtr *buildingenergy.ClassifiedUsageReport
		if !asOf.Before(mp.To) {
			u, cov, ok, err := s.computeDailySnapshotBuildingUsage(ctx, st, bldg, energy, mp.Range, asOf)
			if err != nil {
				return LiveTargetResponse{}, err
			}
			if ok {
				repPtr = buildSyntheticReport(mp.Range, buildingenergy.Day, u, cov)
			}
		}
		comp, err := buildingenergy.CompareEnergyTarget(plan, time.Month(m), repPtr)
		if err != nil {
			return LiveTargetResponse{}, err
		}
		monthRows = append(monthRows, LiveTargetPeriodRow{
			Month:    m,
			Label:    fmt.Sprintf("%d-%02d", year, m),
			From:     mp.From,
			To:       mp.To,
			Target:   milliToFloat(mp.TargetMilli),
			Usage:    comp.Usage,
			Delta:    comp.Delta,
			Ratio:    comp.Ratio,
			Coverage: makeCoverage(comp.Coverage),
			Status:   comp.Status,
		})
	}

	// Annual comparison (comparable for full completed natural year 2025; incomplete_period for in-progress 2026).
	var annRepPtr *buildingenergy.ClassifiedUsageReport
	if !asOf.Before(plan.Annual.To) {
		u, cov, ok, err := s.computeDailySnapshotBuildingUsage(ctx, st, bldg, energy, plan.Annual.Range, asOf)
		if err != nil {
			return LiveTargetResponse{}, err
		}
		if ok {
			annRepPtr = buildSyntheticReport(plan.Annual.Range, buildingenergy.Month, u, cov)
		}
	}
	annComp, err := buildingenergy.CompareEnergyTarget(plan, 0, annRepPtr)
	if err != nil {
		return LiveTargetResponse{}, err
	}

	resp := LiveTargetResponse{
		DataMode:         "live_database",
		DataOriginNotice: st.dataOriginNotice,
		BuildingID:       bldg.BuildingID,
		BuildingName:     bldg.Name,
		Energy:           energy,
		Unit:             unit,
		Year:             year,
		Timezone:         s.timezone(),
		TargetConfigured: plan.Version != nil,
		RuleVersion:      buildingenergy.TargetRuleVersion,
		Annual: LiveTargetPeriodRow{
			Month: 0, Label: fmt.Sprintf("%d 全年", year), From: plan.Annual.From, To: plan.Annual.To,
			Target: milliToFloat(plan.Annual.TargetMilli), Usage: annComp.Usage, Delta: annComp.Delta,
			Ratio: annComp.Ratio, Coverage: makeCoverage(annComp.Coverage), Status: annComp.Status,
		},
		Months: monthRows,
	}
	if plan.Version != nil {
		resp.TargetVersion = plan.Version.Meta.Version
		resp.TargetMethod = plan.Version.Method
	} else {
		resp.Message = fmt.Sprintf("尚未在 energy_domain.energy_target_revision 中为该建筑 %d 年配置正式年度目标，未伪造目标值或完成率。", year)
	}
	return resp, nil
}

func milliToFloat(v *int64) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v) / 1000.0
	return &f
}

func canonicalUnitForEnergy(energy buildingenergy.EnergyType) string {
	switch energy {
	case buildingenergy.Electricity:
		return "kWh"
	case buildingenergy.Water:
		return "m³"
	case buildingenergy.Gas:
		return "Nm³"
	case buildingenergy.Heat, buildingenergy.Cooling, buildingenergy.Steam:
		return "GJ"
	default:
		return "kWh"
	}
}

// ── F10 异常识别 & F17 节能机会分析 ─────────────────────────────────────────

func (s *Service) evaluateAnomalyInternal(
	ctx context.Context,
	st *liveState,
	bldg BuildingProfile,
	energy buildingenergy.EnergyType,
	win buildingenergy.Window,
) (buildingenergy.AnomalyEvaluation, LiveAnomalyResponse, error) {
	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID}

	// Pick active anomaly rule effective across win.Range (nil when unconfigured -> honest missing_rule).
	var activeRule *buildingenergy.AnomalyRule
	activeRev := ""
	for i := range st.anomalyRules {
		r := &st.anomalyRules[i]
		if r.ObjectKind != buildingenergy.Building || r.ObjectID != bldg.BuildingID || r.Energy != energy {
			continue
		}
		if rangeCoversInstant(r.EffectiveFrom, r.EffectiveTo, win.From) {
			activeRev = r.Revision
			ruleEff := rangeFromBounds(r.EffectiveFrom, r.EffectiveTo)
			if ruleEff.To.IsZero() {
				ruleEff.To = win.To.AddDate(1, 0, 0)
			}
			activeRule = &buildingenergy.AnomalyRule{
				Project: s.Project, Object: bldgObj, Energy: energy,
				Meta: buildingenergy.VersionMeta{
					Version:   r.Revision,
					Effective: ruleEff,
					Source:    buildingenergy.SourceRef{System: r.SourceSystem, ID: r.Revision},
				},
				MinimumHistory:          r.MinimumHistory,
				MinimumAbsoluteIncrease: r.MinimumAbsoluteIncrease,
				MinimumRelativeIncrease: r.MinimumRelativeIncrease,
			}
		}
	}

	currRep, _, _, ok, err := s.computeBuildingClassification(ctx, st, bldg, energy, win)
	if err != nil {
		return buildingenergy.AnomalyEvaluation{}, LiveAnomalyResponse{}, err
	}
	if !ok {
		return buildingenergy.AnomalyEvaluation{
				Project: s.Project, Object: bldgObj, Energy: energy, Window: win,
				Status: buildingenergy.AnomalyInsufficientEvidence,
			}, LiveAnomalyResponse{
				DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
				BuildingID: bldg.BuildingID, BuildingName: bldg.Name, Energy: energy,
				Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
				RuleConfigured: activeRule != nil, RuleVersion: buildingenergy.AnomalyRuleVersion,
				ActiveRuleRevision: activeRev, Status: buildingenergy.AnomalyInsufficientEvidence,
				Reason: "missing_usage", Exceptions: st.exceptions,
			}, nil
	}

	// Collect up to 4 prior same-weekday complete days from real PostgreSQL.
	var history []buildingenergy.ClassifiedUsageReport
	var historyPoints []LiveBucketPoint
	for wBack := 1; wBack <= 4; wBack++ {
		pFrom := win.From.AddDate(0, 0, -7*wBack)
		pTo := win.To.AddDate(0, 0, -7*wBack)
		pw := buildingenergy.Window{Range: buildingenergy.Range{From: pFrom, To: pTo}, Timezone: win.Timezone, AsOf: win.AsOf, Granularity: buildingenergy.Hour}
		hRep, _, _, hOk, err := s.computeBuildingClassification(ctx, st, bldg, energy, pw)
		if err != nil {
			continue
		}
		if !hOk || len(hRep.Energy) == 0 || hRep.Energy[0].Total.Usage == nil {
			continue
		}
		cov := hRep.Energy[0].Total.Coverage
		if cov.Expected > 0 && cov.Valid == cov.Expected && cov.Suspect == 0 && cov.Missing == 0 {
			history = append(history, hRep)
			historyPoints = append(historyPoints, LiveBucketPoint{
				From: pFrom, To: pTo, Usage: hRep.Energy[0].Total.Usage,
				Valid: int(cov.Valid), Expected: int(cov.Expected),
			})
		}
	}

	ev, err := buildingenergy.EvaluateUsageAnomaly(energy, activeRule, currRep, history)
	if err != nil {
		return buildingenergy.AnomalyEvaluation{}, LiveAnomalyResponse{}, err
	}

	resp := LiveAnomalyResponse{
		DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
		BuildingID: bldg.BuildingID, BuildingName: bldg.Name, Energy: energy, Unit: ev.Unit,
		Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
		RuleConfigured: activeRule != nil, RuleVersion: buildingenergy.AnomalyRuleVersion,
		ActiveRuleRevision: activeRev, Status: ev.Status, Reason: ev.Reason,
		Current: ev.Current, Coverage: makeCoverage(ev.Coverage),
		BaselineCount: ev.BaselineCount, Baseline: ev.Baseline, Delta: ev.Delta, ChangeRate: ev.ChangeRate,
		HistoryDays: historyPoints, Exceptions: st.exceptions,
	}
	return ev, resp, nil
}

// Anomalies implements F10 异常浪费识别.
func (s *Service) Anomalies(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string) (LiveAnomalyResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveAnomalyResponse{}, err
	}
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	win, err := s.resolveWindow(st, dateStr, 1)
	if err != nil {
		return LiveAnomalyResponse{}, err
	}
	bldg, err := s.selectBuildingAt(st, buildingID, win.From)
	if err != nil {
		return LiveAnomalyResponse{}, err
	}
	_, resp, err := s.evaluateAnomalyInternal(ctx, st, bldg, energy, win)
	return resp, err
}

// Opportunities implements F17 节能机会分析 reusing F10 anomaly + F16 space peers + buildingenergy.BuildEnergyOpportunities.
func (s *Service) Opportunities(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string) (LiveOpportunitiesResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveOpportunitiesResponse{}, err
	}
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	win, err := s.resolveWindow(st, dateStr, 1)
	if err != nil {
		return LiveOpportunitiesResponse{}, err
	}
	bldg, err := s.selectBuildingAt(st, buildingID, win.From)
	if err != nil {
		return LiveOpportunitiesResponse{}, err
	}
	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID}

	ev, anomResp, err := s.evaluateAnomalyInternal(ctx, st, bldg, energy, win)
	if err != nil {
		return LiveOpportunitiesResponse{}, err
	}

	// Build comparable space peers inside this building using F07/F16 Dimensions.
	spaceDims, err := s.Dimensions(ctx, buildingenergy.Space, energy, dateStr, 1)
	if err != nil {
		return LiveOpportunitiesResponse{}, err
	}
	bldgSpaces := map[string]string{}
	for _, sp := range st.spacesEffectiveAt(win.From) {
		if sp.BuildingID == bldg.BuildingID {
			bldgSpaces[sp.SpaceID] = sp.Name
		}
	}
	var peerRows []buildingenergy.OpportunityPeer
	for _, r := range spaceDims.Rows {
		if _, ok := bldgSpaces[r.ID]; !ok {
			continue
		}
		peerRows = append(peerRows, buildingenergy.OpportunityPeer{
			Object: buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: r.ID},
			Value: buildingenergy.CategoryValue{
				Usage: r.Usage,
				Coverage: buildingenergy.WeightedCoverage{
					Expected: r.Coverage.Expected, Valid: r.Coverage.Valid,
					Missing: r.Coverage.Missing, Suspect: r.Coverage.Suspect,
				},
			},
			Status: r.Status,
			Rank:   r.Rank,
		})
	}
	peers := buildingenergy.OpportunityPeers{
		Project: s.Project, Object: bldgObj, Energy: energy, Unit: ev.Unit, Window: win, Rows: peerRows,
	}
	rawClues := buildingenergy.BuildEnergyOpportunities(ev, peers)
	clues := make([]LiveOpportunityClue, 0, len(rawClues))
	for _, c := range rawClues {
		objName := bldg.Name
		if c.Object.Kind == buildingenergy.Space {
			if n, ok := bldgSpaces[c.Object.ID]; ok {
				objName = n
			}
		}
		clues = append(clues, LiveOpportunityClue{
			Kind: c.Kind, Status: c.Status, ObjectID: c.Object.ID, ObjectName: objName,
			Usage: c.Usage, Baseline: c.Baseline, ComparablePeers: c.ComparablePeers,
			Evidence: c.Evidence, Limitations: c.Limitations, Confirm: c.Confirm,
		})
	}
	return LiveOpportunitiesResponse{
		DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
		BuildingID: bldg.BuildingID, BuildingName: bldg.Name, Energy: energy, Unit: ev.Unit,
		Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
		Anomaly: anomResp, Clues: clues, RuleVersion: buildingenergy.AnomalyRuleVersion,
	}, nil
}

// ── F12 行业指标模板 & F13 定额标准对标 ─────────────────────────────────────

func (s *Service) IndustryTemplates(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string, days int) (LiveEfficiencyResponse, error) {
	return s.evaluateTemplates(ctx, buildingID, energy, dateStr, days, true)
}

func (s *Service) IndustryBenchmarks(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string, days int) (LiveBenchmarksResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveBenchmarksResponse{}, err
	}
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	win, err := s.resolveWindow(st, dateStr, days)
	if err != nil {
		return LiveBenchmarksResponse{}, err
	}
	bldg, err := s.selectBuildingAt(st, buildingID, win.From)
	if err != nil {
		return LiveBenchmarksResponse{}, err
	}
	bType := normalizeBuildingType(bldg.BuildingType)
	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID}

	clsRep, _, _, ok, err := s.computeBuildingClassification(ctx, st, bldg, energy, win)
	if err != nil {
		return LiveBenchmarksResponse{}, err
	}
	if !ok {
		return LiveBenchmarksResponse{
			DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
			ConfigurationState: "unconfigured", ConfigurationMessage: unconfiguredConfigMessage(st),
			BuildingID: bldg.BuildingID, BuildingName: bldg.Name, BuildingType: bType, Region: bldg.Region,
			Energy: energy, Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
			RuleVersion: buildingenergy.IndustryRuleVersion,
		}, nil
	}

	indCtx := buildingenergy.IndustryContext{Project: s.Project, Object: bldgObj, BuildingType: bType, Region: bldg.Region}
	specs := templatesForBuildingType(bType, energy, true)
	rows := make([]LiveBenchmarkRow, 0, len(specs))

	for _, sp := range specs {
		tmplMeta := buildingenergy.VersionMeta{
			Version:   sp.version,
			Effective: buildingenergy.Range{From: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
			Source:    buildingenergy.SourceRef{System: "energy-template-catalog", ID: sp.id},
		}
		tmpl := buildingenergy.IndustryTemplate{
			ID: sp.id, Name: sp.name, Meta: tmplMeta, BuildingType: bType,
			ObjectKind: buildingenergy.Building, Energy: energy, Denominator: sp.kind,
		}
		denom := s.resolveBusinessDenominator(st, bldg, sp.kind, win.Range)
		metric := buildingenergy.EvaluateIndustryMetric(indCtx, tmpl, clsRep, denom)

		// Find active benchmark rule configured in energy_domain.benchmark_rule_revision.
		var rulePtr *buildingenergy.BenchmarkRule
		for i := range st.benchmarks {
			br := &st.benchmarks[i]
			if br.MetricID != sp.id || br.BuildingType != bType {
				continue
			}
			if !rangeCoversInstant(br.EffectiveFrom, br.EffectiveTo, win.From) {
				continue
			}
			rulePtr = &buildingenergy.BenchmarkRule{
				ID: br.BenchmarkID, Name: br.Name, Publisher: br.Publisher, Region: br.Region,
				MetricID: br.MetricID, TemplateVersion: br.TemplateVersion, Unit: br.Unit,
				Kind: br.Kind, BuildingType: br.BuildingType, ObjectKind: br.ObjectKind,
				Period: br.PeriodGranularity, Maximum: br.MaximumValue, MinimumDenominator: br.MinimumDenominator,
				Meta: buildingenergy.VersionMeta{
					Version:   br.Revision,
					Effective: rangeFromBounds(br.EffectiveFrom, br.EffectiveTo),
					Source:    buildingenergy.SourceRef{System: br.SourceSystem, ID: br.BenchmarkID},
				},
			}
		}
		benchRes := buildingenergy.EvaluateBenchmark(metric, rulePtr)

		var denomVal *float64
		denomBasis := ""
		if denom != nil {
			v := denom.Value
			denomVal = &v
			denomBasis = denom.Basis
		}
		numUnit := ""
		if len(clsRep.Energy) > 0 {
			numUnit = clsRep.Energy[0].Unit
		}
		liveMetric := LiveEfficiencyMetric{
			ID: sp.id, Name: sp.name, TemplateVersion: sp.version, Energy: energy, BuildingType: bType,
			NumeratorUsage: metric.Numerator.Usage, NumeratorUnit: numUnit, NumeratorCoverage: makeCoverage(metric.Numerator.Coverage),
			DenominatorKind: sp.kind, DenominatorValue: denomVal, DenominatorUnit: sp.inputUnit, DenominatorBasis: denomBasis,
			Value: metric.Value, Unit: metric.Unit, Status: metric.Status, Reason: explainMetricStatus(metric.Status, sp.name, sp.inputUnit),
		}
		row := LiveBenchmarkRow{
			Metric:          liveMetric,
			BenchmarkStatus: benchRes.Status,
			BenchmarkReason: benchRes.Reason,
			BenchmarkDelta:  benchRes.Delta,
		}
		if benchRes.Rule != nil {
			maxV := benchRes.Rule.Maximum
			row.RuleID = benchRes.Rule.ID
			row.RuleName = benchRes.Rule.Name
			row.RuleVersion = benchRes.Rule.Meta.Version
			row.RulePublisher = benchRes.Rule.Publisher
			row.RuleKind = benchRes.Rule.Kind
			row.RuleRegion = benchRes.Rule.Region
			row.RuleMaximum = &maxV
			if benchRes.Rule.Kind == buildingenergy.BenchmarkDemo {
				row.RuleNotice = "演示参考阈值（不构成正式定额达标结论）"
			} else {
				row.RuleNotice = "已配置外部定额标准（仅反映数值对比关系）"
			}
		} else {
			row.RuleNotice = "尚未在 energy_domain.benchmark_rule_revision 配置定额标准，不生成正式达标结论"
		}
		rows = append(rows, row)
	}

	return LiveBenchmarksResponse{
		DataMode: "live_database", DataOriginNotice: st.dataOriginNotice, ConfigurationState: "ready",
		BuildingID: bldg.BuildingID, BuildingName: bldg.Name, BuildingType: bType, Region: bldg.Region,
		Energy: energy, Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
		Rows: rows, RuleVersion: buildingenergy.IndustryRuleVersion,
	}, nil
}

// ── F21 / F22 / F25 Reports + Exact Preview↔CSV Export ──────────────────────

func fmtFloatPtr(v *float64) string {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 3, 64)
}

func fmtRatePtr(v *float64) string {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return ""
	}
	return strconv.FormatFloat(*v*100, 'f', 2, 64) + "%"
}

// ConsumptionReport implements F21 综合与分类分项报表 (reusing F01/F06 live Go calculation).
func (s *Service) ConsumptionReport(ctx context.Context, buildingID, dateStr string, days int) (LiveReportResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveReportResponse{}, err
	}
	ov, err := s.Overview(ctx, buildingID, dateStr, days)
	if err != nil {
		return LiveReportResponse{}, err
	}
	nature := datasetNatureTag(st.dataset)
	cols := []LiveReportColumn{
		{Key: "data_nature", Label: "数据属性"},
		{Key: "building_id", Label: "建筑编号"},
		{Key: "building_name", Label: "建筑名称"},
		{Key: "energy", Label: "能源类型"},
		{Key: "category_id", Label: "分项编号"},
		{Key: "category_name", Label: "分项名称"},
		{Key: "usage", Label: "用量"},
		{Key: "unit", Label: "单位"},
		{Key: "carbon_kgco2e", Label: "模拟参考碳排(kgCO2e)"},
		{Key: "share", Label: "占比"},
		{Key: "coverage_rate", Label: "覆盖率"},
		{Key: "valid_expected", Label: "有效/应有"},
		{Key: "window_from", Label: "开始时间"},
		{Key: "window_to", Label: "结束时间"},
	}
	var rows []map[string]string
	for _, card := range ov.Energies {
		totalCarbon := ""
		if card.Carbon != nil && card.Carbon.KgCO2e != nil {
			totalCarbon = fmtFloatPtr(card.Carbon.KgCO2e)
		}
		rows = append(rows, map[string]string{
			"data_nature":    nature,
			"building_id":    ov.BuildingID,
			"building_name":  ov.BuildingName,
			"energy":         string(card.Energy),
			"category_id":    "TOTAL",
			"category_name":  "能源总计",
			"usage":          fmtFloatPtr(card.Usage),
			"unit":           card.Unit,
			"carbon_kgco2e":  totalCarbon,
			"share":          "100.00%",
			"coverage_rate":  fmtRatePtr(card.Coverage.Rate),
			"valid_expected": fmt.Sprintf("%.0f/%.0f", card.Coverage.Valid, card.Coverage.Expected),
			"window_from":    ov.From.Format(time.RFC3339),
			"window_to":      ov.To.Format(time.RFC3339),
		})
		for _, c := range card.Categories {
			catEst := EstimateCarbon(card.Energy, card.Unit, c.Usage)
			rows = append(rows, map[string]string{
				"data_nature":    nature,
				"building_id":    ov.BuildingID,
				"building_name":  ov.BuildingName,
				"energy":         string(card.Energy),
				"category_id":    c.ID,
				"category_name":  c.Name,
				"usage":          fmtFloatPtr(c.Usage),
				"unit":           card.Unit,
				"carbon_kgco2e":  fmtFloatPtr(catEst.KgCO2e),
				"share":          fmtRatePtr(c.Share),
				"coverage_rate":  fmtRatePtr(c.Coverage.Rate),
				"valid_expected": fmt.Sprintf("%.0f/%.0f", c.Coverage.Valid, c.Coverage.Expected),
				"window_from":    ov.From.Format(time.RFC3339),
				"window_to":      ov.To.Format(time.RFC3339),
			})
		}
		if card.UnclassifiedUsage != nil && *card.UnclassifiedUsage > 0 {
			var share *float64
			if card.Usage != nil && *card.Usage > 0 {
				v := *card.UnclassifiedUsage / *card.Usage
				share = &v
			}
			uncEst := EstimateCarbon(card.Energy, card.Unit, card.UnclassifiedUsage)
			rows = append(rows, map[string]string{
				"data_nature":    nature,
				"building_id":    ov.BuildingID,
				"building_name":  ov.BuildingName,
				"energy":         string(card.Energy),
				"category_id":    "UNCLASSIFIED",
				"category_name":  "未分项",
				"usage":          fmtFloatPtr(card.UnclassifiedUsage),
				"unit":           card.Unit,
				"carbon_kgco2e":  fmtFloatPtr(uncEst.KgCO2e),
				"share":          fmtRatePtr(share),
				"coverage_rate":  fmtRatePtr(card.Coverage.Rate),
				"valid_expected": fmt.Sprintf("%.0f/%.0f", card.Coverage.Valid, card.Coverage.Expected),
				"window_from":    ov.From.Format(time.RFC3339),
				"window_to":      ov.To.Format(time.RFC3339),
			})
		}
	}
	return LiveReportResponse{
		ReportCode: "F21", ReportTitle: "综合与分类分项报表",
		DataMode: "live_database", DataOriginNotice: ov.DataOriginNotice, Dataset: st.dataset,
		BuildingID: ov.BuildingID, BuildingName: ov.BuildingName, Energy: "all",
		Timezone: ov.Timezone, From: ov.From, To: ov.To, AsOf: ov.AsOf, GeneratedAt: s.now().UTC(),
		Columns: cols, Rows: rows,
		Disclaimer: "报表直接复用 F01/F06 实时 Go 计算结果；含模拟数据属性标注，未配置分项归入未分项，页面预览与 CSV 导出逐字段一致。",
	}, nil
}

// MetricsReport implements F22 指标与定额报表 (reusing F11/F12/F13 live Go calculation).
func (s *Service) MetricsReport(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string, days int) (LiveReportResponse, error) {
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveReportResponse{}, err
	}
	bench, err := s.IndustryBenchmarks(ctx, buildingID, energy, dateStr, days)
	if err != nil {
		return LiveReportResponse{}, err
	}
	nature := datasetNatureTag(st.dataset)
	cols := []LiveReportColumn{
		{Key: "data_nature", Label: "数据属性"},
		{Key: "building_id", Label: "建筑编号"},
		{Key: "building_name", Label: "建筑名称"},
		{Key: "building_type", Label: "建筑类型"},
		{Key: "metric_id", Label: "指标编号"},
		{Key: "metric_name", Label: "指标名称"},
		{Key: "numerator_usage", Label: "分子用量"},
		{Key: "numerator_unit", Label: "分子单位"},
		{Key: "denominator_value", Label: "分母数值"},
		{Key: "denominator_unit", Label: "分母单位"},
		{Key: "metric_value", Label: "指标值"},
		{Key: "metric_unit", Label: "指标单位"},
		{Key: "metric_status", Label: "指标状态"},
		{Key: "benchmark_kind", Label: "标准性质"},
		{Key: "benchmark_max", Label: "标准上限"},
		{Key: "benchmark_status", Label: "对标状态"},
		{Key: "benchmark_notice", Label: "口径与边界说明"},
	}
	rows := make([]map[string]string, 0, len(bench.Rows))
	for _, r := range bench.Rows {
		rows = append(rows, map[string]string{
			"data_nature":       nature,
			"building_id":       bench.BuildingID,
			"building_name":     bench.BuildingName,
			"building_type":     string(bench.BuildingType),
			"metric_id":         r.Metric.ID,
			"metric_name":       r.Metric.Name,
			"numerator_usage":   fmtFloatPtr(r.Metric.NumeratorUsage),
			"numerator_unit":    r.Metric.NumeratorUnit,
			"denominator_value": fmtFloatPtr(r.Metric.DenominatorValue),
			"denominator_unit":  r.Metric.DenominatorUnit,
			"metric_value":      fmtFloatPtr(r.Metric.Value),
			"metric_unit":       r.Metric.Unit,
			"metric_status":     string(r.Metric.Status),
			"benchmark_kind":    string(r.RuleKind),
			"benchmark_max":     fmtFloatPtr(r.RuleMaximum),
			"benchmark_status":  string(r.BenchmarkStatus),
			"benchmark_notice":  r.RuleNotice,
		})
	}
	return LiveReportResponse{
		ReportCode: "F22", ReportTitle: "指标与定额报表",
		DataMode: "live_database", DataOriginNotice: bench.DataOriginNotice, Dataset: st.dataset,
		BuildingID: bench.BuildingID, BuildingName: bench.BuildingName, Energy: string(bench.Energy),
		Timezone: bench.Timezone, From: bench.From, To: bench.To, AsOf: bench.AsOf, GeneratedAt: s.now().UTC(),
		Columns: cols, Rows: rows,
		Disclaimer: "报表直接复用 F11/F12/F13 Go 引擎结果；缺失分母或未配置标准时指标/对标留空，演示阈值不构成正式达标结论。",
	}, nil
}

// CustomReport implements F25 行业专项与受限自定义报表 with strict field whitelist enforcement.
func (s *Service) CustomReport(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string, days int, scopeKind string, fields []string) (LiveReportResponse, error) {
	if scopeKind == "" {
		scopeKind = "metrics"
	}
	var base LiveReportResponse
	var err error
	switch scopeKind {
	case "consumption":
		base, err = s.ConsumptionReport(ctx, buildingID, dateStr, days)
	case "metrics":
		base, err = s.MetricsReport(ctx, buildingID, energy, dateStr, days)
	default:
		return LiveReportResponse{}, fmt.Errorf("%w: unsupported custom report scope %q (allowed: consumption, metrics)", ErrBadRequest, scopeKind)
	}
	if err != nil {
		return LiveReportResponse{}, err
	}

	allowedByKey := make(map[string]LiveReportColumn, len(base.Columns))
	for _, c := range base.Columns {
		allowedByKey[c.Key] = c
	}

	var selectedCols []LiveReportColumn
	var selectedKeys []string
	if len(fields) == 0 {
		selectedCols = base.Columns
		for _, c := range base.Columns {
			selectedKeys = append(selectedKeys, c.Key)
		}
	} else {
		seen := map[string]bool{}
		for _, raw := range fields {
			k := strings.TrimSpace(raw)
			if k == "" {
				continue
			}
			col, ok := allowedByKey[k]
			if !ok {
				return LiveReportResponse{}, fmt.Errorf("%w: field %q is not in the authorized whitelist for scope %q", ErrBadRequest, k, scopeKind)
			}
			if !seen[k] {
				seen[k] = true
				selectedCols = append(selectedCols, col)
				selectedKeys = append(selectedKeys, k)
			}
		}
		if len(selectedCols) == 0 {
			return LiveReportResponse{}, fmt.Errorf("%w: at least one valid field must be selected", ErrBadRequest)
		}
	}

	filteredRows := make([]map[string]string, 0, len(base.Rows))
	for _, r := range base.Rows {
		nr := make(map[string]string, len(selectedCols))
		for _, c := range selectedCols {
			nr[c.Key] = r[c.Key]
		}
		filteredRows = append(filteredRows, nr)
	}

	base.ReportCode = "F25"
	base.ReportTitle = "行业专项与受限自定义报表"
	base.AllowedFields = base.Columns
	base.SelectedFields = selectedKeys
	base.Columns = selectedCols
	base.Rows = filteredRows
	base.Disclaimer = "仅允许从受限白名单中选择已计算业务字段，严禁跨权读取未授权列；预览与 CSV 导出逐字段一致。"
	return base, nil
}

// RenderReportCSV serializes LiveReportResponse into deterministic UTF-8 BOM CSV
// strictly matching rep.Columns and rep.Rows (guaranteeing preview == CSV export)
// while neutralizing spreadsheet formula injection payloads.
func RenderReportCSV(rep LiveReportResponse) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM for Excel Chinese compatibility
	w := csv.NewWriter(&buf)
	header := make([]string, len(rep.Columns))
	for i, c := range rep.Columns {
		header[i] = sanitizeCSVCell(c.Label)
	}
	_ = w.Write(header)
	for _, row := range rep.Rows {
		rec := make([]string, len(rep.Columns))
		for i, c := range rep.Columns {
			rec[i] = sanitizeCSVCell(row[c.Key])
		}
		_ = w.Write(rec)
	}
	w.Flush()
	return buf.Bytes()
}

func sanitizeCSVCell(s string) string {
	if s == "" {
		return s
	}
	first := s[0]
	if first == '=' || first == '@' || first == '\t' || first == '\r' {
		return "'" + s
	}
	if first == '+' || first == '-' {
		// Preserve signed numbers & percentages (e.g. "-29.6%", "-12.345"), neutralize formula strings.
		trimmed := strings.TrimSuffix(strings.TrimSpace(s), "%")
		if _, err := strconv.ParseFloat(trimmed, 64); err != nil {
			return "'" + s
		}
	}
	return s
}
