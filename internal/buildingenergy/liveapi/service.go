package liveapi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/authguard"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/pgadapter"
)

var (
	ErrEnergySchemaNotReady = errors.New("energy_domain schema is not migrated yet; configure incremental migrations on the local dev database first")
	ErrNotFound             = errors.New("requested building-energy object or metering point not found")
	ErrBadRequest           = errors.New("invalid building-energy request parameter")
)

// Service orchestrates real PostgreSQL reads/writes and existing internal/buildingenergy
// Go calculation engines behind authguard.Guard. It never serves hardcoded synthetic fixtures.
type Service struct {
	DB       pgadapter.Transactor
	Guard    *authguard.Guard
	Project  buildingenergy.ProjectKey
	Timezone string
	Now      func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) timezone() string {
	if strings.TrimSpace(s.Timezone) != "" {
		return s.Timezone
	}
	return "Asia/Shanghai"
}

func (s *Service) resolveWindow(st *liveState, dateStr string, days int) (buildingenergy.Window, error) {
	loc, err := time.LoadLocation(s.timezone())
	if err != nil {
		return buildingenergy.Window{}, err
	}
	if days <= 0 {
		days = 1
	}
	if days > 31 {
		days = 31
	}
	var latestHour time.Time
	for _, m := range st.meta.Meters {
		if m.MaxHourStart != nil && m.MaxHourStart.After(latestHour) {
			latestHour = *m.MaxHourStart
		}
		if m.MaxRawAt != nil && m.MaxRawAt.After(latestHour) {
			latestHour = *m.MaxRawAt
		}
	}
	if latestHour.IsZero() {
		latestHour = s.now()
	}
	asOf := latestHour.In(loc).Add(2 * time.Hour)
	var dayEnd time.Time
	if strings.TrimSpace(dateStr) != "" {
		parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(dateStr), loc)
		if err != nil {
			return buildingenergy.Window{}, fmt.Errorf("%w: date must be YYYY-MM-DD", ErrBadRequest)
		}
		dayEnd = parsed.AddDate(0, 0, 1)
	} else {
		maxLocal := latestHour.In(loc)
		dayEnd = time.Date(maxLocal.Year(), maxLocal.Month(), maxLocal.Day(), 0, 0, 0, 0, loc)
	}
	dayStart := dayEnd.AddDate(0, 0, -days)
	gran := buildingenergy.Hour
	if days > 1 {
		gran = buildingenergy.Day
	}
	return buildingenergy.Window{
		Range:       buildingenergy.Range{From: dayStart, To: dayEnd},
		Timezone:    s.timezone(),
		AsOf:        asOf,
		Granularity: gran,
	}, nil
}

// selectBuildingAt resolves the authoritative building revision effective at `at` (P3).
func (s *Service) selectBuildingAt(st *liveState, buildingID string, at time.Time) (BuildingProfile, error) {
	active := st.buildingsEffectiveAt(at)
	if strings.TrimSpace(buildingID) == "" {
		if len(active) > 0 {
			return active[0], nil
		}
		if len(st.buildings) > 0 {
			return st.buildings[0], nil
		}
		return BuildingProfile{}, ErrNotFound
	}
	if b, ok := st.buildingEffectiveAt(buildingID, at); ok {
		return b, nil
	}
	// Fallback to latest record ONLY to surface an honest unconfigured/out-of-period shell.
	for _, b := range st.buildings {
		if b.BuildingID == buildingID {
			return b, nil
		}
	}
	return BuildingProfile{}, ErrNotFound
}

func unconfiguredConfigMessage(st *liveState) string {
	if !st.energyDomainReady {
		return "energy_domain 管理表尚未初始化，且系统遵循 P0 规则禁止将未配置通道直接纳入正式能耗核算。"
	}
	return "当前建筑在该统计时段及能源类型下暂无已启用且生效的正式计量点配置（P0 规则：未配置计量口径禁止生成正式能耗结果），请先在「配置中心 → 计量点配置」中完成通道口径发布。"
}

func convertCategories(rows []buildingenergy.CategoryRow, total *float64) []LiveCategoryRow {
	out := make([]LiveCategoryRow, 0, len(rows))
	for _, r := range rows {
		if strings.HasPrefix(r.Category.ID, "_unconfigured_taxonomy_") {
			continue
		}
		var share *float64
		if r.Usage != nil && total != nil && *total > 0 {
			v := *r.Usage / *total
			share = &v
		}
		out = append(out, LiveCategoryRow{
			ID:       r.Category.ID,
			Name:     r.Category.Name,
			Usage:    r.Usage,
			Share:    share,
			Coverage: makeCoverage(r.Coverage),
			Children: convertCategories(r.Children, total),
		})
	}
	return out
}

func aggregateBucketTrend(inputs []buildingenergy.CategoryPointResult) []LiveBucketPoint {
	if len(inputs) == 0 || len(inputs[0].Result.Buckets) == 0 {
		return nil
	}
	n := len(inputs[0].Result.Buckets)
	out := make([]LiveBucketPoint, n)
	for i := 0; i < n; i++ {
		b0 := inputs[0].Result.Buckets[i]
		out[i] = LiveBucketPoint{From: b0.From, To: b0.To}
		var sum float64
		hasValid := false
		for _, in := range inputs {
			if i >= len(in.Result.Buckets) {
				continue
			}
			bk := in.Result.Buckets[i]
			out[i].Expected += bk.Expected
			out[i].Valid += bk.Valid
			if bk.Usage != nil {
				sum += *bk.Usage
				hasValid = true
			}
		}
		if hasValid {
			v := sum
			out[i].Usage = &v
		}
	}
	return out
}

// buildEnergyCards computes separated per-energy summaries (shared by F01 & F02 without mixing concerns).
func (s *Service) buildEnergyCards(
	ctx context.Context,
	st *liveState,
	bldg BuildingProfile,
	win buildingenergy.Window,
) ([]LiveOverviewEnergyCard, []LiveBucketPoint, error) {
	energyOrder := []buildingenergy.EnergyType{
		buildingenergy.Electricity, buildingenergy.Water, buildingenergy.Gas, buildingenergy.Heat, buildingenergy.Cooling, buildingenergy.Steam,
	}
	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID}
	var cards []LiveOverviewEnergyCard
	var elecTrend []LiveBucketPoint

	for _, eType := range energyOrder {
		pts := st.configuredPointsForScope(buildingenergy.Building, []buildingenergy.ObjectKey{bldgObj}, eType, win.Range)
		if len(pts) == 0 {
			continue
		}
		inputs, traces, err := s.calculateConfiguredInputs(ctx, st, buildingenergy.Building, []buildingenergy.ObjectKey{bldgObj}, pts, win)
		if err != nil {
			return nil, nil, err
		}
		if len(inputs) == 0 {
			continue
		}
		clsSnap, topoSnap := s.buildFormalClassificationArtifacts(st, pts, win.Range)
		rep, err := buildingenergy.CalculateClassifiedUsage(
			buildingenergy.ClassifiedUsageQuery{Project: s.Project, Window: win, Object: bldgObj},
			clsSnap, topoSnap, inputs,
		)
		if err != nil {
			return nil, nil, err
		}
		if len(rep.Energy) > 0 {
			e := rep.Energy[0]
			carbon := EstimateCarbon(e.Energy, e.Unit, e.Total.Usage)
			cards = append(cards, LiveOverviewEnergyCard{
				Energy:            e.Energy,
				Unit:              e.Unit,
				Usage:             e.Total.Usage,
				UnclassifiedUsage: e.Unclassified.Usage,
				Coverage:          makeCoverage(e.Total.Coverage),
				Carbon:            &carbon,
				MeterCount:        len(inputs),
				Categories:        convertCategories(e.Categories, e.Total.Usage),
				Sources:           traces,
			})
		}
		if eType == buildingenergy.Electricity && len(inputs) > 0 {
			elecTrend = aggregateBucketTrend(inputs)
		}
	}
	return cards, elecTrend, nil
}

// Overview implements F01 综合能耗概览.
func (s *Service) Overview(ctx context.Context, buildingID, dateStr string, days int) (LiveOverviewResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveOverviewResponse{}, err
	}
	win, err := s.resolveWindow(st, dateStr, days)
	if err != nil {
		return LiveOverviewResponse{}, err
	}
	bldg, err := s.selectBuildingAt(st, buildingID, win.From)
	if err != nil {
		return LiveOverviewResponse{}, err
	}
	cards, elecTrend, err := s.buildEnergyCards(ctx, st, bldg, win)
	if err != nil {
		return LiveOverviewResponse{}, err
	}
	cfgState := "ready"
	cfgMsg := ""
	if len(cards) == 0 {
		cfgState = "unconfigured"
		cfgMsg = unconfiguredConfigMessage(st)
	}
	var carbonSum *CarbonSummary
	if len(cards) > 0 {
		carbonSum = SummarizeCarbon(cards)
	}
	return LiveOverviewResponse{
		DataMode:             "live_database",
		DataOriginNotice:     st.dataOriginNotice,
		ConfigurationState:   cfgState,
		ConfigurationMessage: cfgMsg,
		BuildingID:           bldg.BuildingID,
		BuildingName:         bldg.Name,
		BuildingRevision:     bldg.Revision,
		Timezone:             win.Timezone,
		From:                 win.From,
		To:                   win.To,
		AsOf:                 win.AsOf,
		Energies:             cards,
		CarbonSummary:        carbonSum,
		HourlyTrend:          elecTrend,
		Exceptions:           st.exceptions,
	}, nil
}

// Composition implements F02 能源分类构成 (dedicated multi-energy category & unclassified structure view).
func (s *Service) Composition(ctx context.Context, buildingID, dateStr string, days int) (LiveCompositionResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveCompositionResponse{}, err
	}
	win, err := s.resolveWindow(st, dateStr, days)
	if err != nil {
		return LiveCompositionResponse{}, err
	}
	bldg, err := s.selectBuildingAt(st, buildingID, win.From)
	if err != nil {
		return LiveCompositionResponse{}, err
	}
	cards, _, err := s.buildEnergyCards(ctx, st, bldg, win)
	if err != nil {
		return LiveCompositionResponse{}, err
	}
	cfgState := "ready"
	cfgMsg := ""
	if len(cards) == 0 {
		cfgState = "unconfigured"
		cfgMsg = unconfiguredConfigMessage(st)
	}
	var carbonSum *CarbonSummary
	if len(cards) > 0 {
		carbonSum = SummarizeCarbon(cards)
	}
	return LiveCompositionResponse{
		DataMode:             "live_database",
		DataOriginNotice:     st.dataOriginNotice,
		ConfigurationState:   cfgState,
		ConfigurationMessage: cfgMsg,
		BuildingID:           bldg.BuildingID,
		BuildingName:         bldg.Name,
		BuildingRevision:     bldg.Revision,
		Timezone:             win.Timezone,
		From:                 win.From,
		To:                   win.To,
		AsOf:                 win.AsOf,
		Energies:             cards,
		CarbonSummary:        carbonSum,
		CategoryBindingCount: len(st.categoryBindings),
	}, nil
}

// ObjectsOverview implements F03 分区分项概览 (building total + space & organization distribution).
func (s *Service) ObjectsOverview(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string, days int) (LiveObjectsOverviewResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveObjectsOverviewResponse{}, err
	}
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	win, err := s.resolveWindow(st, dateStr, days)
	if err != nil {
		return LiveObjectsOverviewResponse{}, err
	}
	bldg, err := s.selectBuildingAt(st, buildingID, win.From)
	if err != nil {
		return LiveObjectsOverviewResponse{}, err
	}

	cls, err := s.Classification(ctx, bldg.BuildingID, energy, dateStr, days)
	if err != nil {
		return LiveObjectsOverviewResponse{}, err
	}
	spaceDims, err := s.Dimensions(ctx, buildingenergy.Space, energy, dateStr, days)
	if err != nil {
		return LiveObjectsOverviewResponse{}, err
	}
	orgDims, err := s.Dimensions(ctx, buildingenergy.Organization, energy, dateStr, days)
	if err != nil {
		return LiveObjectsOverviewResponse{}, err
	}

	// Filter space rows to those belonging to this building at win.From.
	bldgSpaces := map[string]bool{}
	for _, sp := range st.spacesEffectiveAt(win.From) {
		if sp.BuildingID == bldg.BuildingID {
			bldgSpaces[sp.SpaceID] = true
		}
	}
	var filteredSpaces []LiveDimensionRow
	for _, r := range spaceDims.Rows {
		if bldgSpaces[r.ID] {
			filteredSpaces = append(filteredSpaces, r)
		}
	}

	return LiveObjectsOverviewResponse{
		DataMode:             "live_database",
		DataOriginNotice:     st.dataOriginNotice,
		ConfigurationState:   cls.ConfigurationState,
		ConfigurationMessage: cls.ConfigurationMessage,
		BuildingID:           bldg.BuildingID,
		BuildingName:         bldg.Name,
		Energy:               energy,
		Unit:                 cls.Unit,
		Timezone:             win.Timezone,
		From:                 win.From,
		To:                   win.To,
		AsOf:                 win.AsOf,
		BuildingTotal:        cls.TotalUsage,
		SpaceRows:            filteredSpaces,
		OrgRows:              orgDims.Rows,
	}, nil
}

// RealtimeTrend implements F04 实时与历史趋势 (multi-day load curve + meter telemetry freshness).
func (s *Service) RealtimeTrend(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string, days int) (LiveRealtimeTrendResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveRealtimeTrendResponse{}, err
	}
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	if days <= 0 {
		days = 7
	}
	win, err := s.resolveWindow(st, dateStr, days)
	if err != nil {
		return LiveRealtimeTrendResponse{}, err
	}
	bldg, err := s.selectBuildingAt(st, buildingID, win.From)
	if err != nil {
		return LiveRealtimeTrendResponse{}, err
	}
	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID}
	pts := st.configuredPointsForScope(buildingenergy.Building, []buildingenergy.ObjectKey{bldgObj}, energy, win.Range)
	if len(pts) == 0 {
		return LiveRealtimeTrendResponse{
			DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
			ConfigurationState: "unconfigured", ConfigurationMessage: unconfiguredConfigMessage(st),
			BuildingID: bldg.BuildingID, BuildingName: bldg.Name, Energy: energy,
			Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
		}, nil
	}

	inputs, _, err := s.calculateConfiguredInputs(ctx, st, buildingenergy.Building, []buildingenergy.ObjectKey{bldgObj}, pts, win)
	if err != nil {
		return LiveRealtimeTrendResponse{}, err
	}
	clsSnap, topoSnap := s.buildFormalClassificationArtifacts(st, pts, win.Range)
	rep, err := buildingenergy.CalculateClassifiedUsage(
		buildingenergy.ClassifiedUsageQuery{Project: s.Project, Window: win, Object: bldgObj},
		clsSnap, topoSnap, inputs,
	)
	if err != nil {
		return LiveRealtimeTrendResponse{}, err
	}

	meterByKey := map[pointKey]pgadapter.IOTMeterRecord{}
	for _, m := range st.meta.Meters {
		meterByKey[pointKey{TerminalID: m.TerminalID, ItemCode: m.ItemCode}] = m
	}
	freshness := make([]LiveMeterFreshness, 0, len(pts))
	for _, pm := range pts {
		m := meterByKey[pm.Key]
		var latest *time.Time
		if m.MaxRawAt != nil {
			latest = m.MaxRawAt
		}
		if m.MaxHourStart != nil && (latest == nil || m.MaxHourStart.After(*latest)) {
			latest = m.MaxHourStart
		}
		freshness = append(freshness, LiveMeterFreshness{
			TerminalID:   pm.Key.TerminalID,
			TerminalName: st.terminalName(pm.Key),
			ItemCode:     pm.Key.ItemCode,
			SourceTable:  m.SourceTable,
			Online:       m.Online,
			HourRows:     m.HourRows,
			RawRows:      m.RawRows,
			LatestAt:     latest,
		})
	}

	resp := LiveRealtimeTrendResponse{
		DataMode:           "live_database",
		DataOriginNotice:   st.dataOriginNotice,
		ConfigurationState: "ready",
		BuildingID:         bldg.BuildingID,
		BuildingName:       bldg.Name,
		Energy:             energy,
		Timezone:           win.Timezone,
		From:               win.From,
		To:                 win.To,
		AsOf:               win.AsOf,
		Series:             aggregateBucketTrend(inputs),
		Freshness:          freshness,
	}
	if len(rep.Energy) > 0 {
		resp.Unit = rep.Energy[0].Unit
		resp.TotalUsage = rep.Energy[0].Total.Usage
		resp.Coverage = makeCoverage(rep.Energy[0].Total.Coverage)
	}
	return resp, nil
}

// computeBuildingClassification is the shared internal helper that computes an authorized
// buildingenergy.ClassifiedUsageReport for (building, energy, window) using ONLY configured points
// and formal energy_domain categories.
func (s *Service) computeBuildingClassification(
	ctx context.Context,
	st *liveState,
	bldg BuildingProfile,
	energy buildingenergy.EnergyType,
	win buildingenergy.Window,
) (buildingenergy.ClassifiedUsageReport, []buildingenergy.CategoryPointResult, []LiveSourceTrace, bool, error) {
	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID}
	pts := st.configuredPointsForScope(buildingenergy.Building, []buildingenergy.ObjectKey{bldgObj}, energy, win.Range)
	if len(pts) == 0 {
		return buildingenergy.ClassifiedUsageReport{}, nil, nil, false, nil
	}
	inputs, traces, err := s.calculateConfiguredInputs(ctx, st, buildingenergy.Building, []buildingenergy.ObjectKey{bldgObj}, pts, win)
	if err != nil {
		return buildingenergy.ClassifiedUsageReport{}, nil, nil, false, err
	}
	if len(inputs) == 0 {
		return buildingenergy.ClassifiedUsageReport{}, nil, nil, false, nil
	}
	clsSnap, topoSnap := s.buildFormalClassificationArtifacts(st, pts, win.Range)
	rep, err := buildingenergy.CalculateClassifiedUsage(
		buildingenergy.ClassifiedUsageQuery{Project: s.Project, Window: win, Object: bldgObj},
		clsSnap, topoSnap, inputs,
	)
	if err != nil {
		return buildingenergy.ClassifiedUsageReport{}, nil, nil, false, err
	}
	return rep, inputs, traces, true, nil
}

// Classification implements F06 分类分项统计.
func (s *Service) Classification(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string, days int) (LiveClassificationResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveClassificationResponse{}, err
	}
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	win, err := s.resolveWindow(st, dateStr, days)
	if err != nil {
		return LiveClassificationResponse{}, err
	}
	bldg, err := s.selectBuildingAt(st, buildingID, win.From)
	if err != nil {
		return LiveClassificationResponse{}, err
	}
	report, _, traces, ok, err := s.computeBuildingClassification(ctx, st, bldg, energy, win)
	if err != nil {
		return LiveClassificationResponse{}, err
	}
	if !ok {
		return LiveClassificationResponse{
			DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
			ConfigurationState: "unconfigured", ConfigurationMessage: unconfiguredConfigMessage(st),
			BuildingID: bldg.BuildingID, BuildingName: bldg.Name, BuildingRevision: bldg.Revision,
			Energy: energy, Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
			RuleVersion: "m1-a-v1", ClassificationRuleVersion: "m1-c-v1",
		}, nil
	}
	resp := LiveClassificationResponse{
		DataMode:                  "live_database",
		DataOriginNotice:          st.dataOriginNotice,
		ConfigurationState:        "ready",
		BuildingID:                bldg.BuildingID,
		BuildingName:              bldg.Name,
		BuildingRevision:          bldg.Revision,
		Energy:                    energy,
		Timezone:                  win.Timezone,
		From:                      win.From,
		To:                        win.To,
		AsOf:                      win.AsOf,
		Sources:                   traces,
		RuleVersion:               "m1-a-v1",
		ClassificationRuleVersion: report.RuleVersion,
	}
	if len(report.Energy) > 0 {
		e := report.Energy[0]
		carbon := EstimateCarbon(e.Energy, e.Unit, e.Total.Usage)
		resp.Unit = e.Unit
		resp.TotalUsage = e.Total.Usage
		resp.TotalCoverage = makeCoverage(e.Total.Coverage)
		resp.UnclassifiedUsage = e.Unclassified.Usage
		resp.Carbon = &carbon
		resp.Categories = convertCategories(e.Categories, e.Total.Usage)
	}
	return resp, nil
}

// AuthorizeReportExport enforces explicit DownloadReport + ExportUsage authorization
// before any CSV file download is served (preventing ReadUsage-only callers from exporting).
func (s *Service) AuthorizeReportExport(ctx context.Context, buildingID string, rng buildingenergy.Range) error {
	if s.Guard == nil {
		return buildingenergy.ErrAccessDenied
	}
	st, err := s.loadState(ctx)
	if err != nil {
		return err
	}
	if rng.From.IsZero() {
		now := s.now().UTC()
		rng = buildingenergy.Range{From: now.Add(-24 * time.Hour), To: now}
	}
	scope := buildingenergy.Scope{Project: s.Project, Period: rng}
	if b := strings.TrimSpace(buildingID); b != "" && b != "all" {
		scope.Objects = []buildingenergy.ObjectKey{{Kind: buildingenergy.Building, ID: b}}
	}
	if _, err := s.Guard.AuthorizeScope(ctx, st.catalog, buildingenergy.DownloadReport, scope); err != nil {
		return err
	}
	if _, err := s.Guard.AuthorizeScope(ctx, st.catalog, buildingenergy.ExportUsage, scope); err != nil {
		return err
	}
	return nil
}

// Dimensions implements F07 多维统计 & F16 高耗能对象识别 (supports Building, Space, and Organization).
func (s *Service) Dimensions(ctx context.Context, view buildingenergy.ObjectKind, energy buildingenergy.EnergyType, dateStr string, days int) (LiveDimensionsResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveDimensionsResponse{}, err
	}
	if view == "" {
		view = buildingenergy.Building
	}
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	win, err := s.resolveWindow(st, dateStr, days)
	if err != nil {
		return LiveDimensionsResponse{}, err
	}

	var objs []buildingenergy.ObjectKey
	nameByID := map[string]string{}
	switch view {
	case buildingenergy.Building:
		for _, b := range st.buildingsEffectiveAt(win.From) {
			objs = append(objs, buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: b.BuildingID})
			nameByID[b.BuildingID] = b.Name
		}
	case buildingenergy.Space:
		for _, sp := range st.spacesEffectiveAt(win.From) {
			objs = append(objs, buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: sp.SpaceID})
			nameByID[sp.SpaceID] = sp.Name
		}
	case buildingenergy.Organization:
		for _, o := range st.orgsEffectiveAt(win.From) {
			objs = append(objs, buildingenergy.ObjectKey{Kind: buildingenergy.Organization, ID: o.OrgID})
			nameByID[o.OrgID] = o.Name
		}
	default:
		return LiveDimensionsResponse{
			DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
			ConfigurationState: "unconfigured", View: view, Energy: energy,
			Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
			RuleVersion: buildingenergy.DimensionRuleVersion,
		}, nil
	}

	pts := st.configuredPointsForScope(view, objs, energy, win.Range)
	if len(pts) == 0 || len(objs) == 0 {
		return LiveDimensionsResponse{
			DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
			ConfigurationState: "unconfigured", ConfigurationMessage: unconfiguredConfigMessage(st),
			View: view, Energy: energy, Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
			RuleVersion: buildingenergy.DimensionRuleVersion,
		}, nil
	}

	pointKeys := make([]buildingenergy.PointKey, 0, len(pts))
	for _, pm := range pts {
		pointKeys = append(pointKeys, pm.Key)
	}
	inputs, traces, err := s.calculateConfiguredInputs(ctx, st, view, objs, pts, win)
	if err != nil {
		return LiveDimensionsResponse{}, err
	}
	traceByPoint := make(map[buildingenergy.PointKey]LiveSourceTrace, len(traces))
	for _, tr := range traces {
		traceByPoint[buildingenergy.PointKey{TerminalID: tr.TerminalID, ItemCode: tr.ItemCode}] = tr
	}

	clsSnap, topoSnap := s.buildFormalClassificationArtifacts(st, pts, win.Range)
	dimReport, err := buildingenergy.CalculateDimensionUsage(
		buildingenergy.DimensionUsageQuery{
			Scope:  buildingenergy.Scope{Project: s.Project, Period: win.Range, Objects: objs, Points: pointKeys},
			View:   view,
			Window: win,
			Energy: energy,
		},
		clsSnap, topoSnap, inputs,
	)
	if err != nil {
		return LiveDimensionsResponse{}, err
	}

	rows := make([]LiveDimensionRow, 0, len(dimReport.Rows))
	for _, r := range dimReport.Rows {
		seen := map[buildingenergy.PointKey]bool{}
		var rowSources []LiveSourceTrace
		for _, ev := range r.Evidence {
			if !seen[ev.Point.Key] {
				seen[ev.Point.Key] = true
				if tr, ok := traceByPoint[ev.Point.Key]; ok {
					rowSources = append(rowSources, tr)
				}
			}
		}
		reason := ""
		if r.Rank == nil {
			reason = explainUnrankedStatus(r.Status)
		}
		rows = append(rows, LiveDimensionRow{
			ID:       r.Object.ID,
			Name:     nameByID[r.Object.ID],
			Usage:    r.Value.Usage,
			Coverage: makeCoverage(r.Value.Coverage),
			Status:   r.Status,
			Rank:     r.Rank,
			Reason:   reason,
			Sources:  rowSources,
		})
	}
	return LiveDimensionsResponse{
		DataMode:                  "live_database",
		DataOriginNotice:          st.dataOriginNotice,
		ConfigurationState:        "ready",
		View:                      view,
		Energy:                    energy,
		Unit:                      dimReport.Unit,
		Timezone:                  win.Timezone,
		From:                      win.From,
		To:                        win.To,
		AsOf:                      win.AsOf,
		RuleVersion:               buildingenergy.DimensionRuleVersion,
		ClassificationRuleVersion: dimReport.ClassificationRuleVersion,
		Rows:                      rows,
	}, nil
}

func explainUnrankedStatus(st buildingenergy.TrendStatus) string {
	switch st {
	case buildingenergy.TrendMissingData:
		return "缺少有效计量数据，不参与正式排名"
	case buildingenergy.TrendInsufficientQuality:
		return "覆盖率不足或存在可疑读数，不参与正式排名"
	case buildingenergy.TrendInconsistentScope:
		return "统计口径、计量边界或对象版本在窗口内发生变化，不参与同口径排名"
	default:
		return "不符合同口径排名条件"
	}
}

// Trend implements F08 趋势、同环比与排名.
func (s *Service) Trend(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string) (LiveTrendResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveTrendResponse{}, err
	}
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	win, err := s.resolveWindow(st, dateStr, 1)
	if err != nil {
		return LiveTrendResponse{}, err
	}
	bldg, err := s.selectBuildingAt(st, buildingID, win.From)
	if err != nil {
		return LiveTrendResponse{}, err
	}
	plan, err := buildingenergy.PlanTrend(buildingenergy.TrendRequest{
		At: win.From, Timezone: win.Timezone, Granularity: buildingenergy.Day, Lookback: 7,
	})
	if err != nil {
		return LiveTrendResponse{}, err
	}

	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID}
	currPts := st.configuredPointsForScope(buildingenergy.Building, []buildingenergy.ObjectKey{bldgObj}, energy, plan.Current)
	if len(currPts) == 0 {
		return LiveTrendResponse{
			DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
			ConfigurationState: "unconfigured", ConfigurationMessage: unconfiguredConfigMessage(st),
			BuildingID: bldg.BuildingID, BuildingName: bldg.Name, Energy: energy,
			Timezone: win.Timezone, Granularity: buildingenergy.Day,
			From: plan.Current.From, To: plan.Current.To, AsOf: win.AsOf,
			RuleVersion: buildingenergy.TrendRuleVersion,
		}, nil
	}

	runForRange := func(rng buildingenergy.Range) (buildingenergy.ClassifiedUsageReport, []buildingenergy.CategoryPointResult, error) {
		w := buildingenergy.Window{Range: rng, Timezone: win.Timezone, AsOf: win.AsOf, Granularity: buildingenergy.Hour}
		pts := st.configuredPointsForScope(buildingenergy.Building, []buildingenergy.ObjectKey{bldgObj}, energy, rng)
		if len(pts) == 0 {
			return buildingenergy.ClassifiedUsageReport{
				Query:       buildingenergy.ClassifiedUsageQuery{Project: s.Project, Window: w, Object: bldgObj},
				RuleVersion: "m1-c-v1",
			}, nil, nil
		}
		ins, _, err := s.calculateConfiguredInputs(ctx, st, buildingenergy.Building, []buildingenergy.ObjectKey{bldgObj}, pts, w)
		if err != nil {
			return buildingenergy.ClassifiedUsageReport{}, nil, err
		}
		clsSnap, topoSnap := s.buildFormalClassificationArtifacts(st, pts, rng)
		rep, err := buildingenergy.CalculateClassifiedUsage(
			buildingenergy.ClassifiedUsageQuery{Project: s.Project, Window: w, Object: bldgObj},
			clsSnap, topoSnap, ins,
		)
		return rep, ins, err
	}

	currRep, currInputs, err := runForRange(plan.Current)
	if err != nil {
		return LiveTrendResponse{}, err
	}
	prevRep, _, err := runForRange(plan.Previous)
	if err != nil {
		return LiveTrendResponse{}, err
	}
	priorYearRep, _, err := runForRange(plan.PriorYear)
	if err != nil {
		return LiveTrendResponse{}, err
	}
	comp, err := buildingenergy.CompareClassifiedPeriods(plan, energy, currRep, prevRep, priorYearRep)
	if err != nil {
		return LiveTrendResponse{}, err
	}

	var prevBaseline, priorYearBaseline *float64
	if len(prevRep.Energy) > 0 {
		prevBaseline = prevRep.Energy[0].Total.Usage
	}
	if len(priorYearRep.Energy) > 0 {
		priorYearBaseline = priorYearRep.Energy[0].Total.Usage
	}
	resp := LiveTrendResponse{
		DataMode:           "live_database",
		DataOriginNotice:   st.dataOriginNotice,
		ConfigurationState: "ready",
		BuildingID:         bldg.BuildingID,
		BuildingName:       bldg.Name,
		Energy:             energy,
		Timezone:           win.Timezone,
		Granularity:        buildingenergy.Day,
		From:               plan.Current.From,
		To:                 plan.Current.To,
		AsOf:               win.AsOf,
		Series:             aggregateBucketTrend(currInputs),
		RuleVersion:        buildingenergy.TrendRuleVersion,
		Previous: LiveTrendComparison{
			Status: comp.Previous.Status, BaselineFrom: plan.Previous.From, BaselineTo: plan.Previous.To,
			Baseline: prevBaseline, Delta: comp.Previous.Delta, Rate: comp.Previous.ChangeRate, Adjusted: plan.PreviousAdjusted,
		},
		PriorYear: LiveTrendComparison{
			Status: comp.PriorYear.Status, BaselineFrom: plan.PriorYear.From, BaselineTo: plan.PriorYear.To,
			Baseline: priorYearBaseline, Delta: comp.PriorYear.Delta, Rate: comp.PriorYear.ChangeRate, Adjusted: plan.PriorYearAdjusted,
		},
	}
	if len(currRep.Energy) > 0 {
		resp.Unit = currRep.Energy[0].Unit
		resp.Current = currRep.Energy[0].Total.Usage
		resp.Coverage = makeCoverage(currRep.Energy[0].Total.Coverage)
	}
	return resp, nil
}

// Balance implements F09 能流与总分表平衡 using the effective-dated topology version active for `win.Range` (P3).
func (s *Service) Balance(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string) (LiveBalanceResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveBalanceResponse{}, err
	}
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	win, err := s.resolveWindow(st, dateStr, 1)
	if err != nil {
		return LiveBalanceResponse{}, err
	}
	bldg, err := s.selectBuildingAt(st, buildingID, win.From)
	if err != nil {
		return LiveBalanceResponse{}, err
	}

	edges := st.topologyEffectiveForWindow(bldg.BuildingID, energy, win.Range)
	if len(edges) == 0 {
		return LiveBalanceResponse{
			DataMode:           "live_database",
			DataOriginNotice:   st.dataOriginNotice,
			BuildingID:         bldg.BuildingID,
			BuildingName:       bldg.Name,
			Energy:             energy,
			Timezone:           win.Timezone,
			From:               win.From,
			To:                 win.To,
			AsOf:               win.AsOf,
			TopologyConfigured: false,
			Message:            "当前统计时段尚未配置该建筑的总表与直接分表物理拓扑（energy_domain.meter_topology），未生成虚构总分表差额。",
		}, nil
	}

	parentKey := buildingenergy.PointKey{TerminalID: edges[0].ParentTerminalID, ItemCode: edges[0].ParentItemCode}
	parentRef := buildingenergy.PointRef{Project: s.Project, Key: parentKey}
	parentPM, ok := st.configuredPoints[parentKey]
	if !ok || !parentPM.Enabled {
		return LiveBalanceResponse{
			DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
			BuildingID: bldg.BuildingID, BuildingName: bldg.Name, Energy: energy,
			Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
			TopologyConfigured: false,
			Message:            fmt.Sprintf("总表 %s/%s 尚未完成有效计量点配置或已停用，禁止计算总分表平衡。", parentKey.TerminalID, parentKey.ItemCode),
		}, nil
	}

	var allZones []string
	children := make([]buildingenergy.BalanceMeter, 0, len(edges))
	neededPMs := []*configuredPointMeta{parentPM}
	seenPM := map[pointKey]bool{parentKey: true}
	unit := "kWh"
	if len(parentPM.Profiles) > 0 && parentPM.Profiles[0].Unit != "" {
		unit = parentPM.Profiles[0].Unit
	}
	for _, e := range edges {
		ck := buildingenergy.PointKey{TerminalID: e.ChildTerminalID, ItemCode: e.ChildItemCode}
		cpm, exists := st.configuredPoints[ck]
		if !exists || !cpm.Enabled {
			return LiveBalanceResponse{
				DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
				BuildingID: bldg.BuildingID, BuildingName: bldg.Name, Energy: energy,
				Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
				TopologyConfigured: false,
				Message:            fmt.Sprintf("分表 %s/%s 尚未完成有效计量点配置或已停用，禁止计算总分表平衡。", ck.TerminalID, ck.ItemCode),
			}, nil
		}
		if !seenPM[ck] {
			seenPM[ck] = true
			neededPMs = append(neededPMs, cpm)
		}
		allZones = append(allZones, e.ZoneCode)
		children = append(children, buildingenergy.BalanceMeter{
			Point:             buildingenergy.PointRef{Project: s.Project, Key: ck},
			Channel:           buildingenergy.SourceRef{System: "iot-terminal", ID: ck.TerminalID + ":" + ck.ItemCode},
			MappingGeneration: cpm.Generation,
			Parent:            parentRef,
			Name:              st.terminalName(ck),
			Zones:             []string{e.ZoneCode},
		})
	}

	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID}
	inputs, _, err := s.calculateConfiguredInputs(ctx, st, buildingenergy.Building, []buildingenergy.ObjectKey{bldgObj}, neededPMs, win)
	if err != nil {
		return LiveBalanceResponse{}, err
	}
	topoEff := rangeFromBounds(edges[0].EffectiveFrom, edges[0].EffectiveTo)
	topo := buildingenergy.BalanceTopology{
		Project: s.Project, Object: bldgObj, Energy: energy, Unit: unit,
		Meta: buildingenergy.VersionMeta{
			Version:   edges[0].TopologyVersion,
			Effective: topoEff,
			Source:    buildingenergy.SourceRef{System: "energy-admin", ID: edges[0].TopologyVersion},
		},
		Parent: buildingenergy.BalanceMeter{
			Point: parentRef, Channel: buildingenergy.SourceRef{System: "iot-terminal", ID: parentKey.TerminalID + ":" + parentKey.ItemCode},
			MappingGeneration: parentPM.Generation, Name: st.terminalName(parentKey), Zones: allZones,
		},
		Children: children,
	}
	rep, err := buildingenergy.CalculateBalance(
		buildingenergy.BalanceQuery{Project: s.Project, Object: bldgObj, Energy: energy, Window: win},
		topo, inputs,
	)
	if err != nil {
		return LiveBalanceResponse{}, err
	}
	return LiveBalanceResponse{
		DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
		BuildingID: bldg.BuildingID, BuildingName: bldg.Name, Energy: energy,
		Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
		TopologyConfigured: true, TopologyVersion: edges[0].TopologyVersion, Report: &rep,
	}, nil
}

// Efficiency implements F11 通用能效强度 (area & per-capita intensity using historical effective-dated building revision).
func (s *Service) Efficiency(ctx context.Context, buildingID string, energy buildingenergy.EnergyType, dateStr string, days int) (LiveEfficiencyResponse, error) {
	return s.evaluateTemplates(ctx, buildingID, energy, dateStr, days, false)
}

// evaluateTemplates powers both F11 (general area/people) and F12 (industry-specific templates).
func (s *Service) evaluateTemplates(
	ctx context.Context,
	buildingID string,
	energy buildingenergy.EnergyType,
	dateStr string,
	days int,
	industryOnly bool,
) (LiveEfficiencyResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return LiveEfficiencyResponse{}, err
	}
	if energy == "" {
		energy = buildingenergy.Electricity
	}
	win, err := s.resolveWindow(st, dateStr, days)
	if err != nil {
		return LiveEfficiencyResponse{}, err
	}
	bldg, err := s.selectBuildingAt(st, buildingID, win.From)
	if err != nil {
		return LiveEfficiencyResponse{}, err
	}
	bType := normalizeBuildingType(bldg.BuildingType)
	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID}

	clsRep, _, traces, ok, err := s.computeBuildingClassification(ctx, st, bldg, energy, win)
	if err != nil {
		return LiveEfficiencyResponse{}, err
	}
	if !ok {
		return LiveEfficiencyResponse{
			DataMode: "live_database", DataOriginNotice: st.dataOriginNotice,
			ConfigurationState: "unconfigured", ConfigurationMessage: unconfiguredConfigMessage(st),
			BuildingID: bldg.BuildingID, BuildingName: bldg.Name, BuildingType: bType, Region: bldg.Region,
			BuildingRevision: bldg.Revision, Energy: energy,
			Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
		}, nil
	}

	indCtx := buildingenergy.IndustryContext{
		Project: s.Project, Object: bldgObj, BuildingType: bType, Region: bldg.Region,
	}
	specs := templatesForBuildingType(bType, energy, industryOnly)
	metrics := make([]LiveEfficiencyMetric, 0, len(specs))
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
		res := buildingenergy.EvaluateIndustryMetric(indCtx, tmpl, clsRep, denom)
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
		metrics = append(metrics, LiveEfficiencyMetric{
			ID: sp.id, Name: sp.name, TemplateVersion: sp.version, Energy: energy, BuildingType: bType,
			NumeratorUsage: res.Numerator.Usage, NumeratorUnit: numUnit, NumeratorCoverage: makeCoverage(res.Numerator.Coverage),
			DenominatorKind: sp.kind, DenominatorValue: denomVal, DenominatorUnit: sp.inputUnit, DenominatorBasis: denomBasis,
			Value: res.Value, Unit: res.Unit, Status: res.Status, Reason: explainMetricStatus(res.Status, sp.name, sp.inputUnit),
		})
	}

	return LiveEfficiencyResponse{
		DataMode: "live_database", DataOriginNotice: st.dataOriginNotice, ConfigurationState: "ready",
		BuildingID: bldg.BuildingID, BuildingName: bldg.Name, BuildingType: bType, Region: bldg.Region,
		BuildingRevision: bldg.Revision, Energy: energy,
		Timezone: win.Timezone, From: win.From, To: win.To, AsOf: win.AsOf,
		Metrics: metrics, Sources: traces,
	}, nil
}

type templateSpec struct {
	id, name, version string
	kind              buildingenergy.DenominatorKind
	inputUnit         string
}

func normalizeBuildingType(raw string) buildingenergy.BuildingType {
	bt := buildingenergy.BuildingType(strings.TrimSpace(raw))
	switch bt {
	case buildingenergy.BuildingOffice, buildingenergy.BuildingCommercial, buildingenergy.BuildingHospital, buildingenergy.BuildingSchool, buildingenergy.BuildingHotel:
		return bt
	default:
		return buildingenergy.BuildingOffice
	}
}

func templatesForBuildingType(bt buildingenergy.BuildingType, energy buildingenergy.EnergyType, industryOnly bool) []templateSpec {
	prefix := string(energy)
	if !industryOnly {
		return []templateSpec{
			{id: prefix + "-area-intensity", name: "单位建筑面积能耗强度", version: "tmpl-general-v1", kind: buildingenergy.DenominatorArea, inputUnit: "m²"},
			{id: prefix + "-people-intensity", name: "人均能耗强度", version: "tmpl-general-v1", kind: buildingenergy.DenominatorPeople, inputUnit: "人"},
		}
	}
	switch bt {
	case buildingenergy.BuildingCommercial:
		return []templateSpec{
			{id: "commercial-" + prefix + "-area", name: "商业综合体单位建筑面积能耗强度", version: "tmpl-commercial-v1", kind: buildingenergy.DenominatorArea, inputUnit: "m²"},
			{id: "commercial-" + prefix + "-revenue", name: "商业综合体万元营收能耗强度", version: "tmpl-commercial-v1", kind: buildingenergy.DenominatorRevenue, inputUnit: "元"},
			{id: "commercial-" + prefix + "-people", name: "商业综合体人均能耗强度", version: "tmpl-commercial-v1", kind: buildingenergy.DenominatorPeople, inputUnit: "人"},
		}
	case buildingenergy.BuildingHospital:
		return []templateSpec{
			{id: "hospital-" + prefix + "-bed-day", name: "医院单位床日能耗强度", version: "tmpl-hospital-v1", kind: buildingenergy.DenominatorBedDays, inputUnit: "床日"},
			{id: "hospital-" + prefix + "-area", name: "医院单位建筑面积能耗强度", version: "tmpl-hospital-v1", kind: buildingenergy.DenominatorArea, inputUnit: "m²"},
		}
	case buildingenergy.BuildingSchool:
		return []templateSpec{
			{id: "school-" + prefix + "-student", name: "学校生均能耗强度", version: "tmpl-school-v1", kind: buildingenergy.DenominatorStudents, inputUnit: "人"},
			{id: "school-" + prefix + "-area", name: "学校单位建筑面积能耗强度", version: "tmpl-school-v1", kind: buildingenergy.DenominatorArea, inputUnit: "m²"},
		}
	case buildingenergy.BuildingHotel:
		return []templateSpec{
			{id: "hotel-" + prefix + "-room-night", name: "酒店单位间夜能耗强度", version: "tmpl-hotel-v1", kind: buildingenergy.DenominatorRoomNights, inputUnit: "间夜"},
			{id: "hotel-" + prefix + "-revenue", name: "酒店万元营收能耗强度", version: "tmpl-hotel-v1", kind: buildingenergy.DenominatorRevenue, inputUnit: "元"},
			{id: "hotel-" + prefix + "-area", name: "酒店单位建筑面积能耗强度", version: "tmpl-hotel-v1", kind: buildingenergy.DenominatorArea, inputUnit: "m²"},
		}
	default: // Office
		return []templateSpec{
			{id: "office-" + prefix + "-area", name: "办公建筑单位建筑面积能耗强度", version: "tmpl-office-v1", kind: buildingenergy.DenominatorArea, inputUnit: "m²"},
			{id: "office-" + prefix + "-people", name: "办公建筑人均能耗强度", version: "tmpl-office-v1", kind: buildingenergy.DenominatorPeople, inputUnit: "人"},
			{id: "office-" + prefix + "-revenue", name: "办公建筑万元营收能耗强度", version: "tmpl-office-v1", kind: buildingenergy.DenominatorRevenue, inputUnit: "元"},
		}
	}
}

// resolveBusinessDenominator looks up an explicit versioned denominator in
// energy_domain.business_denominator_revision first, falling back to the
// effective-dated building_revision grossAreaSqm / occupiedHeadcount for snapshot denominators.
func (s *Service) resolveBusinessDenominator(
	st *liveState,
	bldg BuildingProfile,
	kind buildingenergy.DenominatorKind,
	period buildingenergy.Range,
) *buildingenergy.BusinessDenominator {
	bldgObj := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bldg.BuildingID}
	// 1. Explicit business_denominator_revision covering period.
	var best *DenominatorConfigRecord
	for i := range st.denominators {
		d := &st.denominators[i]
		if d.ObjectKind != buildingenergy.Building || d.ObjectID != bldg.BuildingID || d.DenominatorKind != kind {
			continue
		}
		if !rangeCoversInstant(d.EffectiveFrom, d.EffectiveTo, period.From) {
			continue
		}
		if best == nil || !d.EffectiveFrom.Before(best.EffectiveFrom) {
			best = d
		}
	}
	if best != nil {
		return &buildingenergy.BusinessDenominator{
			Project: s.Project, Object: bldgObj, Kind: kind,
			Value: best.Value, Unit: best.Unit,
			Period: buildingenergy.Range{From: best.PeriodFrom, To: best.PeriodTo},
			Meta: buildingenergy.VersionMeta{
				Version:   best.Revision,
				Effective: rangeFromBounds(best.EffectiveFrom, best.EffectiveTo),
				Source:    buildingenergy.SourceRef{System: best.SourceSystem, ID: best.Revision},
			},
			Basis: best.Basis,
		}
	}
	// 2. Fallback to effective-dated building_revision grossAreaSqm / occupiedHeadcount.
	effMeta := buildingenergy.VersionMeta{
		Version:   bldg.Revision,
		Effective: rangeFromBounds(bldg.EffectiveFrom, bldg.EffectiveTo),
		Source:    buildingenergy.SourceRef{System: bldg.SourceSystem, ID: bldg.BuildingID},
	}
	if kind == buildingenergy.DenominatorArea && bldg.GrossAreaSqm != nil && !math.IsNaN(*bldg.GrossAreaSqm) && *bldg.GrossAreaSqm > 0 {
		return &buildingenergy.BusinessDenominator{
			Project: s.Project, Object: bldgObj, Kind: kind, Value: *bldg.GrossAreaSqm, Unit: "m²",
			Period: period, Meta: effMeta, Basis: "building_archive",
		}
	}
	if kind == buildingenergy.DenominatorPeople && bldg.OccupiedHeadcount != nil && !math.IsNaN(*bldg.OccupiedHeadcount) && *bldg.OccupiedHeadcount > 0 {
		return &buildingenergy.BusinessDenominator{
			Project: s.Project, Object: bldgObj, Kind: kind, Value: *bldg.OccupiedHeadcount, Unit: "人",
			Period: period, Meta: effMeta, Basis: "building_archive",
		}
	}
	return nil
}

func explainMetricStatus(status buildingenergy.IndustryMetricStatus, name, unit string) string {
	switch status {
	case buildingenergy.IndustryMetricReady:
		return "已完成分子分母同口径计算"
	case buildingenergy.IndustryMissingDenominator:
		return fmt.Sprintf("尚未配置「%s」所需业务分母（%s），指标留空（CALC-06）", name, unit)
	case buildingenergy.IndustryDenominatorOutOfPeriod:
		return fmt.Sprintf("已配置分母区间未精确覆盖统计周期，指标留空（CALC-06）")
	case buildingenergy.IndustryInsufficientQuality:
		return "计量数据覆盖率不足或存在可疑读数，不生成正式强度值"
	case buildingenergy.IndustryMissingUsage:
		return "统计窗口内无有效能耗计量数据"
	default:
		return string(status)
	}
}
