package liveapi

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/pgadapter"
)

type pointKey = buildingenergy.PointKey

type configuredPointMeta struct {
	Key          pointKey
	Generation   uint64
	Enabled      bool
	StatusReason string
	SourceTables map[string]pgadapter.SourceTable // profile revision -> source_table
	Timeline     buildingenergy.MappingTimeline
	Profiles     []MeteringProfileRevisionRecord
	Bindings     []MeteringBindingRevisionRecord
}

type liveState struct {
	meta               *pgadapter.IOTMetadataSnapshot
	dataOriginNotice   string
	dataset            *DatasetMeta
	energyDomainReady  bool
	governanceReady    bool // migration 0002 applied
	p2Ready                    bool // migration 0003 applied
	retrofitLifecycleReady     bool // migration 0004 applied
	buildingHistory            []BuildingProfile
	spaceHistory               []SpaceProfile
	orgHistory                 []OrganizationProfile
	buildings                  []BuildingProfile     // latest view for inventory dropdowns
	spaces                     []SpaceProfile        // latest view for inventory dropdowns
	organizations              []OrganizationProfile // latest view for inventory dropdowns
	configuredPoints           map[pointKey]*configuredPointMeta
	categories                 []CategoryNodeRecord
	categoryBindings           []PointCategoryBindingRecord
	topology                   []TopologyEdge
	registerEvents             []RegisterEventRecord
	targets                    []TargetConfigRecord
	anomalyRules               []AnomalyRuleConfigRecord
	denominators               []DenominatorConfigRecord
	benchmarks                 []BenchmarkConfigRecord
	customTemplates            []CustomReportTemplateRecord
	assessmentRules            []AssessmentRuleRecord
	retrofits                  []RetrofitProjectRecord
	retrofitBaselineRevisions  map[string][]RetrofitBaselineRevision
	retrofitVerificationLogs   map[string][]RetrofitVerificationRecord
	exceptions                 []ExceptionSummary
	catalog                    *domainCatalog
}

const (
	checkEnergyDomainReadySQL      = `SELECT to_regclass('energy_domain.building_revision') IS NOT NULL`
	checkGovernanceReadySQL        = `SELECT to_regclass('energy_domain.energy_target_revision') IS NOT NULL`
	checkP2ReadySQL                = `SELECT to_regclass('energy_domain.dataset_metadata') IS NOT NULL`
	checkRetrofitLifecycleReadySQL = `SELECT to_regclass('energy_domain.retrofit_baseline_revision') IS NOT NULL`
	checkHeadEnabledColSQL         = `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'energy_domain' AND table_name = 'metering_point_head' AND column_name = 'enabled')`
	checkBldgRegionColSQL          = `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'energy_domain' AND table_name = 'building_revision' AND column_name = 'region')`

	selectDatasetMetaSQL = `
SELECT dataset_id, dataset_name, dataset_version, seed, is_simulated, generated_at,
       data_from, data_to, hourly_from, raw_from, building_scope, energy_scope, anomaly_manifest::text
FROM energy_domain.dataset_metadata
WHERE tenant_id = $1 AND project_id = $2
ORDER BY generated_at DESC
LIMIT 1`

	selectAssessmentRulesSQL = `
SELECT rule_id, revision, lower(effective), upper(effective), object_kind, object_id, energy_type,
       metric_label, target_value, unit, weight_pct, public_share_pct, exempt, exempt_reason,
       scoring_formula, min_coverage_rate, review_status, review_note
FROM energy_domain.assessment_rule
WHERE tenant_id = $1 AND project_id = $2
ORDER BY rule_id ASC, lower(effective) DESC`

	selectRetrofitProjectsSQL = `
SELECT retrofit_id, building_id, ''::text, energy_type, name, measure_summary, unit,
       baseline_version, baseline_from, baseline_to, baseline_model, baseline_factors,
       baseline_r2, baseline_coverage, baseline_locked, baseline_daily_avg,
       est_savings_low, est_savings_base, est_savings_high, ref_unit_price, capex_budget,
       report_from, report_to, adjusted_baseline_use, non_routine_adjustment,
       weather_data_complete, mv_standard_ref, mv_status, mv_note
FROM energy_domain.retrofit_project
WHERE tenant_id = $1 AND project_id = $2
ORDER BY retrofit_id ASC`

	selectRetrofitProjectsWithSpaceSQL = `
SELECT retrofit_id, building_id, coalesce(space_id, ''), energy_type, name, measure_summary, unit,
       baseline_version, baseline_from, baseline_to, baseline_model, baseline_factors,
       baseline_r2, baseline_coverage, baseline_locked, baseline_daily_avg,
       est_savings_low, est_savings_base, est_savings_high, ref_unit_price, capex_budget,
       report_from, report_to, adjusted_baseline_use, non_routine_adjustment,
       weather_data_complete, mv_standard_ref, mv_status, mv_note
FROM energy_domain.retrofit_project
WHERE tenant_id = $1 AND project_id = $2
ORDER BY updated_at DESC, retrofit_id ASC`

	selectRetrofitBaselineRevisionsSQL = `
SELECT retrofit_id, baseline_version, building_id, coalesce(space_id, ''), energy_type,
       baseline_from, baseline_to, baseline_model, baseline_factors, baseline_r2,
       baseline_coverage, baseline_locked, baseline_daily_avg, baseline_actual_usage,
       change_reason, actor_sub, recorded_at
FROM energy_domain.retrofit_baseline_revision
WHERE tenant_id = $1 AND project_id = $2
ORDER BY retrofit_id ASC, recorded_at DESC, baseline_version DESC`

	selectRetrofitVerificationLogsSQL = `
SELECT id, retrofit_id, baseline_version, report_from, report_to,
       adjusted_baseline_use, report_actual_usage, report_coverage_rate, non_routine_adjustment,
       verified_savings, verified_savings_rate, verified_carbon_kgco2e,
       weather_data_complete, mv_status, mv_note, actor_sub, recorded_at
FROM energy_domain.retrofit_verification_log
WHERE tenant_id = $1 AND project_id = $2
ORDER BY recorded_at DESC, id DESC
LIMIT 500`

	selectAllDomainBuildingsWithRegionSQL = `
SELECT building_id, revision, name, building_type, coalesce(region, ''), gross_area_sqm, occupied_headcount, timezone,
       lower(effective), upper(effective), legacy_building_id, source_system
FROM energy_domain.building_revision
WHERE tenant_id = $1 AND project_id = $2
ORDER BY building_id ASC, lower(effective) ASC, recorded_at ASC`

	selectAllDomainBuildingsBaseSQL = `
SELECT building_id, revision, name, building_type, ''::text, gross_area_sqm, occupied_headcount, timezone,
       lower(effective), upper(effective), legacy_building_id, source_system
FROM energy_domain.building_revision
WHERE tenant_id = $1 AND project_id = $2
ORDER BY building_id ASC, lower(effective) ASC, recorded_at ASC`

	selectAllDomainSpacesSQL = `
SELECT space_id, revision, building_id, coalesce(parent_space_id, ''), name, area_sqm,
       lower(effective), upper(effective), legacy_area_id, source_system
FROM energy_domain.space_revision
WHERE tenant_id = $1 AND project_id = $2
ORDER BY space_id ASC, lower(effective) ASC, recorded_at ASC`

	selectAllDomainOrgsSQL = `
SELECT org_id, revision, coalesce(parent_org_id, ''), name, headcount,
       lower(effective), upper(effective), source_system
FROM energy_domain.organization_revision
WHERE tenant_id = $1 AND project_id = $2
ORDER BY org_id ASC, lower(effective) ASC, recorded_at ASC`

	selectHeadsWithEnabledSQL = `
SELECT terminal_id, item_code, coalesce(current_generation, 0), enabled, coalesce(status_reason, '')
FROM energy_domain.metering_point_head
WHERE tenant_id = $1 AND project_id = $2 AND current_generation IS NOT NULL
ORDER BY terminal_id ASC, item_code ASC`

	selectHeadsBaseSQL = `
SELECT terminal_id, item_code, coalesce(current_generation, 0), true, ''::text
FROM energy_domain.metering_point_head
WHERE tenant_id = $1 AND project_id = $2 AND current_generation IS NOT NULL
ORDER BY terminal_id ASC, item_code ASC`

	selectCurrentProfilesSQL = `
SELECT p.terminal_id, p.item_code, p.generation, p.revision,
       lower(p.effective), upper(p.effective),
       p.source_table, p.source_system, p.source_id, p.channel_system, p.channel_id,
       coalesce(p.register_epoch, ''), p.measurement_kind, p.energy_type, p.unit,
       p.multiplier::double precision, p.register_modulus::double precision, coalesce(p.cadence_seconds, 3600)
FROM energy_domain.metering_point_head h
JOIN energy_domain.metering_point_profile p
  ON p.tenant_id = h.tenant_id AND p.project_id = h.project_id
 AND p.terminal_id = h.terminal_id AND p.item_code = h.item_code
 AND p.generation = h.current_generation
WHERE h.tenant_id = $1 AND h.project_id = $2 AND h.current_generation IS NOT NULL
ORDER BY p.terminal_id ASC, p.item_code ASC, lower(p.effective) ASC`

	selectCurrentBindingsSQL = `
SELECT b.terminal_id, b.item_code, b.generation, b.view_kind, b.revision,
       lower(b.effective), upper(b.effective),
       b.source_system, b.source_id,
       a.target_id, a.target_revision, a.weight_ppm
FROM energy_domain.metering_point_head h
JOIN energy_domain.metering_point_binding b
  ON b.tenant_id = h.tenant_id AND b.project_id = h.project_id
 AND b.terminal_id = h.terminal_id AND b.item_code = h.item_code
 AND b.generation = h.current_generation
JOIN energy_domain.metering_point_binding_allocation a
  ON a.tenant_id = b.tenant_id AND a.project_id = b.project_id
 AND a.terminal_id = b.terminal_id AND a.item_code = b.item_code
 AND a.generation = b.generation AND a.view_kind = b.view_kind
 AND a.binding_revision = b.revision
WHERE h.tenant_id = $1 AND h.project_id = $2 AND h.current_generation IS NOT NULL
ORDER BY b.terminal_id ASC, b.item_code ASC, b.view_kind ASC, lower(b.effective) ASC, a.target_id ASC`

	selectDomainCategoriesSQL = `
SELECT category_id, coalesce(parent_id, ''), name, energy_type
FROM energy_domain.energy_category
WHERE tenant_id = $1 AND project_id = $2
ORDER BY energy_type ASC, parent_id ASC, category_id ASC`

	selectDomainCategoryBindingsSQL = `
SELECT terminal_id, item_code, revision, lower(effective), upper(effective), category_id, weight_ppm
FROM energy_domain.point_category_binding
WHERE tenant_id = $1 AND project_id = $2
ORDER BY terminal_id ASC, item_code ASC, lower(effective) ASC, revision ASC, category_id ASC`

	selectDomainTopologySQL = `
SELECT topology_version, lower(effective), upper(effective), building_id, energy_type,
       parent_terminal_id, parent_item_code, child_terminal_id, child_item_code, zone_code
FROM energy_domain.meter_topology
WHERE tenant_id = $1 AND project_id = $2
ORDER BY lower(effective) DESC, topology_version DESC, building_id ASC, energy_type ASC, parent_terminal_id ASC, child_terminal_id ASC`

	selectDomainRegisterEventsSQL = `
SELECT id, terminal_id, item_code, event_kind, effective_at, coalesce(new_epoch, ''), reason, actor_issuer || ':' || actor_sub
FROM energy_domain.meter_register_event
WHERE tenant_id = $1 AND project_id = $2
ORDER BY effective_at DESC, id DESC
LIMIT 500`

	selectDomainTargetsSQL = `
SELECT object_kind, object_id, energy_type, target_year, revision, lower(effective), upper(effective),
       unit, annual_milli, method, weight_ppm, source_system
FROM energy_domain.energy_target_revision
WHERE tenant_id = $1 AND project_id = $2
ORDER BY target_year DESC, object_id ASC, energy_type ASC, lower(effective) ASC`

	selectDomainAnomalyRulesSQL = `
SELECT object_kind, object_id, energy_type, revision, lower(effective), upper(effective),
       minimum_history, minimum_absolute_increase, minimum_relative_increase, source_system
FROM energy_domain.anomaly_rule_revision
WHERE tenant_id = $1 AND project_id = $2
ORDER BY object_id ASC, energy_type ASC, lower(effective) ASC`

	selectDomainDenominatorsSQL = `
SELECT object_kind, object_id, denominator_kind, revision,
       lower(period_range), upper(period_range), lower(effective), upper(effective),
       value, unit, basis, source_system
FROM energy_domain.business_denominator_revision
WHERE tenant_id = $1 AND project_id = $2
ORDER BY object_id ASC, denominator_kind ASC, lower(effective) ASC`

	selectDomainBenchmarksSQL = `
SELECT benchmark_id, revision, lower(effective), upper(effective),
       name, publisher, region, metric_id, template_version, unit, kind,
       building_type, object_kind, period_granularity, maximum_value, minimum_denominator, source_system
FROM energy_domain.benchmark_rule_revision
WHERE tenant_id = $1 AND project_id = $2
ORDER BY building_type ASC, metric_id ASC, lower(effective) ASC`

	selectDomainCustomTemplatesSQL = `
SELECT template_id, name, scope_kind, fields, created_by, updated_at
FROM energy_domain.custom_report_template
WHERE tenant_id = $1 AND project_id = $2
ORDER BY updated_at DESC, template_id ASC`

	selectPublicExceptionsSQL = `
SELECT terminal_id, error_type, sum(count)::bigint
FROM exception_count
GROUP BY terminal_id, error_type
ORDER BY sum(count) DESC, terminal_id ASC
LIMIT 100`
)

func rangeCoversInstant(from time.Time, to *time.Time, at time.Time) bool {
	if at.Before(from) {
		return false
	}
	if to != nil && !to.IsZero() && !at.Before(*to) {
		return false
	}
	return true
}

func rangeFromBounds(from time.Time, to *time.Time) buildingenergy.Range {
	r := buildingenergy.Range{From: from.UTC()}
	if to != nil && !to.IsZero() {
		r.To = to.UTC()
	}
	return r
}

func rangeOverlaps(from time.Time, to *time.Time, rng buildingenergy.Range) bool {
	if !rng.To.IsZero() && !from.Before(rng.To) {
		return false
	}
	if to != nil && !to.IsZero() && !to.After(rng.From) {
		return false
	}
	return true
}

func (s *Service) loadState(ctx context.Context) (*liveState, error) {
	if s == nil || s.DB == nil {
		return nil, buildingenergy.ErrCatalogScope
	}
	effAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	meta, err := pgadapter.LoadIOTMetadata(ctx, s.DB, s.Project, s.timezone(), effAt)
	if err != nil {
		return nil, err
	}

	notice := "当前直连 PostgreSQL 数据库，仅纳入已显式配置的计量点进行正式能耗核算，未混入前端合成样本。"
	for _, m := range meta.Meters {
		if strings.HasPrefix(m.TerminalID, "demo-") || strings.HasPrefix(m.DomainCode, "demo_") {
			notice = "当前数据库为本地开发适配样本（iot_preview），仅用于验证真实 PostgreSQL 适配与计算链路，不代表现场实际运行能耗。"
			break
		}
	}

	st := &liveState{
		meta:             meta,
		dataOriginNotice: notice,
		configuredPoints: make(map[pointKey]*configuredPointMeta),
	}

	// Read-only fallback display entries from public.energy_building / energy_area
	// (overridden/extended by effective-dated energy_domain revisions whenever present).
	publicBuildings := make(map[string]BuildingProfile)
	for _, b := range meta.Buildings {
		id := pgadapter.BuildingObjectID(b.ID)
		legacyID := b.ID
		bp := BuildingProfile{
			BuildingID:       id,
			Revision:         "iot-bldg-" + fmt.Sprintf("%d", b.ID) + "-v1",
			Name:             b.Name,
			BuildingType:     "office",
			Timezone:         s.timezone(),
			EffectiveFrom:    effAt,
			LegacyBuildingID: &legacyID,
			SourceSystem:     "iot-public-readonly",
		}
		publicBuildings[id] = bp
	}
	publicSpaces := make(map[string]SpaceProfile)
	for _, a := range meta.Areas {
		id := pgadapter.AreaObjectID(a.ID)
		legacyID := a.ID
		sp := SpaceProfile{
			SpaceID:       id,
			Revision:      "iot-area-" + fmt.Sprintf("%d", a.ID) + "-v1",
			BuildingID:    pgadapter.BuildingObjectID(a.BuildingID),
			Name:          a.Name,
			EffectiveFrom: effAt,
			LegacyAreaID:  &legacyID,
			SourceSystem:  "iot-public-readonly",
		}
		publicSpaces[id] = sp
	}

	if eRows, err := s.DB.Query(ctx, selectPublicExceptionsSQL); err == nil {
		for eRows.Next() {
			var ex ExceptionSummary
			if err := eRows.Scan(&ex.TerminalID, &ex.ErrorType, &ex.Count); err == nil {
				st.exceptions = append(st.exceptions, ex)
			}
		}
		eRows.Close()
	}

	var ready bool
	if err := s.DB.QueryRow(ctx, checkEnergyDomainReadySQL).Scan(&ready); err == nil && ready {
		st.energyDomainReady = true

		var hasRegion, hasEnabled, govReady bool
		_ = s.DB.QueryRow(ctx, checkBldgRegionColSQL).Scan(&hasRegion)
		_ = s.DB.QueryRow(ctx, checkHeadEnabledColSQL).Scan(&hasEnabled)
		_ = s.DB.QueryRow(ctx, checkGovernanceReadySQL).Scan(&govReady)
		st.governanceReady = govReady

		// 1. Building revisions (full effective history)
		bSQL := selectAllDomainBuildingsBaseSQL
		if hasRegion {
			bSQL = selectAllDomainBuildingsWithRegionSQL
		}
		domainBuildingIDs := map[string]bool{}
		if rows, err := s.DB.Query(ctx, bSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
			for rows.Next() {
				var bp BuildingProfile
				var effFrom time.Time
				var effTo *time.Time
				if err := rows.Scan(
					&bp.BuildingID, &bp.Revision, &bp.Name, &bp.BuildingType, &bp.Region,
					&bp.GrossAreaSqm, &bp.OccupiedHeadcount, &bp.Timezone,
					&effFrom, &effTo, &bp.LegacyBuildingID, &bp.SourceSystem,
				); err == nil {
					bp.EffectiveFrom = effFrom.UTC()
					if effTo != nil && !effTo.IsZero() {
						t := effTo.UTC()
						bp.EffectiveTo = &t
					}
					domainBuildingIDs[bp.BuildingID] = true
					st.buildingHistory = append(st.buildingHistory, bp)
				}
			}
			rows.Close()
		}

		// 2. Space revisions (full effective history)
		domainSpaceIDs := map[string]bool{}
		if rows, err := s.DB.Query(ctx, selectAllDomainSpacesSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
			for rows.Next() {
				var sp SpaceProfile
				var effFrom time.Time
				var effTo *time.Time
				if err := rows.Scan(
					&sp.SpaceID, &sp.Revision, &sp.BuildingID, &sp.ParentSpaceID, &sp.Name, &sp.AreaSqm,
					&effFrom, &effTo, &sp.LegacyAreaID, &sp.SourceSystem,
				); err == nil {
					sp.EffectiveFrom = effFrom.UTC()
					if effTo != nil && !effTo.IsZero() {
						t := effTo.UTC()
						sp.EffectiveTo = &t
					}
					domainSpaceIDs[sp.SpaceID] = true
					st.spaceHistory = append(st.spaceHistory, sp)
				}
			}
			rows.Close()
		}

		// 3. Organization revisions (full effective history)
		if rows, err := s.DB.Query(ctx, selectAllDomainOrgsSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
			for rows.Next() {
				var op OrganizationProfile
				var effFrom time.Time
				var effTo *time.Time
				if err := rows.Scan(
					&op.OrgID, &op.Revision, &op.ParentOrgID, &op.Name, &op.Headcount,
					&effFrom, &effTo, &op.SourceSystem,
				); err == nil {
					op.EffectiveFrom = effFrom.UTC()
					if effTo != nil && !effTo.IsZero() {
						t := effTo.UTC()
						op.EffectiveTo = &t
					}
					st.orgHistory = append(st.orgHistory, op)
				}
			}
			rows.Close()
		}

		// Include public buildings/spaces ONLY when energy_domain has not defined authoritative buildings yet.
		if len(domainBuildingIDs) == 0 {
			for _, bp := range publicBuildings {
				st.buildingHistory = append(st.buildingHistory, bp)
			}
		}
		if len(domainSpaceIDs) == 0 {
			for _, sp := range publicSpaces {
				st.spaceHistory = append(st.spaceHistory, sp)
			}
		}

		// 4. Metering point heads, full profiles, and multi-view bindings
		headSQL := selectHeadsBaseSQL
		if hasEnabled {
			headSQL = selectHeadsWithEnabledSQL
		}
		if rows, err := s.DB.Query(ctx, headSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
			for rows.Next() {
				var tid, code, reason string
				var gen uint64
				var enabled bool
				if err := rows.Scan(&tid, &code, &gen, &enabled, &reason); err == nil && gen > 0 {
					pk := pointKey{TerminalID: tid, ItemCode: code}
					st.configuredPoints[pk] = &configuredPointMeta{
						Key:          pk,
						Generation:   gen,
						Enabled:      enabled,
						StatusReason: reason,
						SourceTables: make(map[string]pgadapter.SourceTable),
						Timeline: buildingenergy.MappingTimeline{
							Point:      buildingenergy.PointRef{Project: s.Project, Key: pk},
							Generation: gen,
						},
					}
				}
			}
			rows.Close()
		}

		if rows, err := s.DB.Query(ctx, selectCurrentProfilesSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
			for rows.Next() {
				var (
					tid, code, rev, srcTable, srcSys, srcID, chSys, chID, regEpoch, kindStr, energyStr, unit string
					gen                                                                                      uint64
					effFrom                                                                                  time.Time
					effTo                                                                                    *time.Time
					mult, mod                                                                                *float64
					cadSec                                                                                   int
				)
				if err := rows.Scan(
					&tid, &code, &gen, &rev, &effFrom, &effTo,
					&srcTable, &srcSys, &srcID, &chSys, &chID, &regEpoch,
					&kindStr, &energyStr, &unit, &mult, &mod, &cadSec,
				); err != nil {
					continue
				}
				pk := pointKey{TerminalID: tid, ItemCode: code}
				pm, ok := st.configuredPoints[pk]
				if !ok || pm.Generation != gen {
					continue
				}
				mVal := 1.0
				if mult != nil && *mult > 0 {
					mVal = *mult
				}
				modVal := 0.0
				if mod != nil && *mod > 0 {
					modVal = *mod
				}
				if cadSec <= 0 {
					cadSec = 3600
				}
				eff := rangeFromBounds(effFrom, effTo)
				stTable := pgadapter.SourceTable(srcTable)
				if stTable != pgadapter.SourceTableRawItem {
					stTable = pgadapter.SourceTableSnapshotCoarse
				}
				pm.SourceTables[rev] = stTable
				pm.Timeline.Profiles = append(pm.Timeline.Profiles, buildingenergy.ProfileRevision{
					Profile: buildingenergy.PointProfile{
						Point:      buildingenergy.PointRef{Project: s.Project, Key: pk},
						Kind:       buildingenergy.MeasurementKind(kindStr),
						Energy:     buildingenergy.EnergyType(energyStr),
						Unit:       unit,
						Multiplier: mVal,
						Modulus:    modVal,
						Cadence:    time.Duration(cadSec) * time.Second,
						Meta: buildingenergy.VersionMeta{
							Version:   rev,
							Effective: eff,
							Source:    buildingenergy.SourceRef{System: srcSys, ID: srcID},
						},
					},
					Channel:       buildingenergy.SourceRef{System: chSys, ID: chID},
					RegisterEpoch: regEpoch,
				})
				rec := MeteringProfileRevisionRecord{
					Generation:      gen,
					Revision:        rev,
					EffectiveFrom:   eff.From,
					SourceTable:     stTable,
					Energy:          buildingenergy.EnergyType(energyStr),
					MeasurementKind: buildingenergy.MeasurementKind(kindStr),
					Unit:            unit,
					Multiplier:      mVal,
					RegisterModulus: mod,
					RegisterEpoch:   regEpoch,
					CadenceSeconds:  cadSec,
				}
				if !eff.To.IsZero() {
					t := eff.To
					rec.EffectiveTo = &t
				}
				pm.Profiles = append(pm.Profiles, rec)
			}
			rows.Close()
		}

		type bindGroupKey struct {
			pk       pointKey
			viewKind buildingenergy.ObjectKind
			rev      string
		}
		bindIdx := map[bindGroupKey]int{}
		if rows, err := s.DB.Query(ctx, selectCurrentBindingsSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
			for rows.Next() {
				var (
					tid, code, viewStr, rev, srcSys, srcID, targetID, targetRev string
					gen                                                         uint64
					effFrom                                                     time.Time
					effTo                                                       *time.Time
					weightPPM                                                   int64
				)
				if err := rows.Scan(
					&tid, &code, &gen, &viewStr, &rev, &effFrom, &effTo,
					&srcSys, &srcID, &targetID, &targetRev, &weightPPM,
				); err != nil {
					continue
				}
				pk := pointKey{TerminalID: tid, ItemCode: code}
				pm, ok := st.configuredPoints[pk]
				if !ok || pm.Generation != gen {
					continue
				}
				vk := buildingenergy.ObjectKind(viewStr)
				gk := bindGroupKey{pk: pk, viewKind: vk, rev: rev}
				idx, exists := bindIdx[gk]
				if !exists {
					eff := rangeFromBounds(effFrom, effTo)
					idx = len(pm.Timeline.Bindings)
					bindIdx[gk] = idx
					pm.Timeline.Bindings = append(pm.Timeline.Bindings, buildingenergy.BindingRevision{
						View: vk,
						Meta: buildingenergy.VersionMeta{
							Version:   rev,
							Effective: eff,
							Source:    buildingenergy.SourceRef{System: srcSys, ID: srcID},
						},
					})
					brec := MeteringBindingRevisionRecord{
						Generation:    gen,
						ViewKind:      vk,
						Revision:      rev,
						EffectiveFrom: eff.From,
					}
					if !eff.To.IsZero() {
						t := eff.To
						brec.EffectiveTo = &t
					}
					pm.Bindings = append(pm.Bindings, brec)
				}
				pm.Timeline.Bindings[idx].Allocations = append(pm.Timeline.Bindings[idx].Allocations, buildingenergy.Allocation{
					Target:         buildingenergy.ObjectKey{Kind: vk, ID: targetID},
					TargetRevision: targetRev,
					WeightPPM:      int(weightPPM),
				})
				pm.Bindings[idx].Allocations = append(pm.Bindings[idx].Allocations, MeteringBindingAllocationRecord{
					TargetID:       targetID,
					TargetRevision: targetRev,
					WeightPPM:      weightPPM,
				})
			}
			rows.Close()
		}

		// 5. Formal Categories & Point-to-Category Bindings (P2 — ZERO keyword fallback!)
		if rows, err := s.DB.Query(ctx, selectDomainCategoriesSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
			for rows.Next() {
				var c CategoryNodeRecord
				var eStr string
				if err := rows.Scan(&c.CategoryID, &c.ParentID, &c.Name, &eStr); err == nil {
					c.Energy = buildingenergy.EnergyType(eStr)
					st.categories = append(st.categories, c)
				}
			}
			rows.Close()
		}

		type catBindKey struct {
			tid, code, rev string
		}
		catBindMap := map[catBindKey]int{}
		if rows, err := s.DB.Query(ctx, selectDomainCategoryBindingsSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
			for rows.Next() {
				var tid, code, rev, catID string
				var effFrom time.Time
				var effTo *time.Time
				var wPPM int64
				if err := rows.Scan(&tid, &code, &rev, &effFrom, &effTo, &catID, &wPPM); err != nil {
					continue
				}
				k := catBindKey{tid, code, rev}
				idx, exists := catBindMap[k]
				if !exists {
					idx = len(st.categoryBindings)
					catBindMap[k] = idx
					rec := PointCategoryBindingRecord{
						TerminalID:    tid,
						ItemCode:      code,
						Revision:      rev,
						EffectiveFrom: effFrom.UTC(),
					}
					if effTo != nil && !effTo.IsZero() {
						t := effTo.UTC()
						rec.EffectiveTo = &t
					}
					st.categoryBindings = append(st.categoryBindings, rec)
				}
				st.categoryBindings[idx].Allocations = append(st.categoryBindings[idx].Allocations, CategoryAllocationInput{
					CategoryID: catID,
					WeightPPM:  wPPM,
				})
			}
			rows.Close()
		}

		// 6. Meter Topology (with effective range!)
		if rows, err := s.DB.Query(ctx, selectDomainTopologySQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
			for rows.Next() {
				var edge TopologyEdge
				var effFrom time.Time
				var effTo *time.Time
				var eStr string
				if err := rows.Scan(
					&edge.TopologyVersion, &effFrom, &effTo, &edge.BuildingID, &eStr,
					&edge.ParentTerminalID, &edge.ParentItemCode, &edge.ChildTerminalID, &edge.ChildItemCode, &edge.ZoneCode,
				); err == nil {
					edge.Energy = buildingenergy.EnergyType(eStr)
					edge.EffectiveFrom = effFrom.UTC()
					if effTo != nil && !effTo.IsZero() {
						t := effTo.UTC()
						edge.EffectiveTo = &t
					}
					st.topology = append(st.topology, edge)
				}
			}
			rows.Close()
		}

		// 7. Register Events
		if rows, err := s.DB.Query(ctx, selectDomainRegisterEventsSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
			for rows.Next() {
				var ev RegisterEventRecord
				if err := rows.Scan(&ev.ID, &ev.TerminalID, &ev.ItemCode, &ev.EventKind, &ev.EffectiveAt, &ev.NewEpoch, &ev.Reason, &ev.Actor); err == nil {
					ev.EffectiveAt = ev.EffectiveAt.UTC()
					st.registerEvents = append(st.registerEvents, ev)
				}
			}
			rows.Close()
		}

		// 8. Migration 0002 Governance tables (F05/F14 targets, F10/F17 anomaly rules, F12/F13 denominators & benchmarks, F25 templates)
		if st.governanceReady {
			if rows, err := s.DB.Query(ctx, selectDomainTargetsSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
				for rows.Next() {
					var tr TargetConfigRecord
					var okStr, eStr, mStr string
					var effFrom time.Time
					var effTo *time.Time
					var weights []int32
					if err := rows.Scan(
						&okStr, &tr.ObjectID, &eStr, &tr.Year, &tr.Revision, &effFrom, &effTo,
						&tr.Unit, &tr.AnnualMilli, &mStr, &weights, &tr.SourceSystem,
					); err == nil {
						tr.ObjectKind = buildingenergy.ObjectKind(okStr)
						tr.Energy = buildingenergy.EnergyType(eStr)
						tr.Method = buildingenergy.TargetMethod(mStr)
						tr.EffectiveFrom = effFrom.UTC()
						if effTo != nil && !effTo.IsZero() {
							t := effTo.UTC()
							tr.EffectiveTo = &t
						}
						if len(weights) == 12 {
							tr.WeightPPM = make([]int64, 12)
							for i, w := range weights {
								tr.WeightPPM[i] = int64(w)
							}
						}
						st.targets = append(st.targets, tr)
					}
				}
				rows.Close()
			}
			if rows, err := s.DB.Query(ctx, selectDomainAnomalyRulesSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
				for rows.Next() {
					var ar AnomalyRuleConfigRecord
					var okStr, eStr string
					var effFrom time.Time
					var effTo *time.Time
					if err := rows.Scan(
						&okStr, &ar.ObjectID, &eStr, &ar.Revision, &effFrom, &effTo,
						&ar.MinimumHistory, &ar.MinimumAbsoluteIncrease, &ar.MinimumRelativeIncrease, &ar.SourceSystem,
					); err == nil {
						ar.ObjectKind = buildingenergy.ObjectKind(okStr)
						ar.Energy = buildingenergy.EnergyType(eStr)
						ar.EffectiveFrom = effFrom.UTC()
						if effTo != nil && !effTo.IsZero() {
							t := effTo.UTC()
							ar.EffectiveTo = &t
						}
						st.anomalyRules = append(st.anomalyRules, ar)
					}
				}
				rows.Close()
			}
			if rows, err := s.DB.Query(ctx, selectDomainDenominatorsSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
				for rows.Next() {
					var dr DenominatorConfigRecord
					var okStr, dkStr string
					var pFrom, pTo, effFrom time.Time
					var effTo *time.Time
					if err := rows.Scan(
						&okStr, &dr.ObjectID, &dkStr, &dr.Revision,
						&pFrom, &pTo, &effFrom, &effTo,
						&dr.Value, &dr.Unit, &dr.Basis, &dr.SourceSystem,
					); err == nil {
						dr.ObjectKind = buildingenergy.ObjectKind(okStr)
						dr.DenominatorKind = buildingenergy.DenominatorKind(dkStr)
						dr.PeriodFrom = pFrom.UTC()
						dr.PeriodTo = pTo.UTC()
						dr.EffectiveFrom = effFrom.UTC()
						if effTo != nil && !effTo.IsZero() {
							t := effTo.UTC()
							dr.EffectiveTo = &t
						}
						st.denominators = append(st.denominators, dr)
					}
				}
				rows.Close()
			}
			if rows, err := s.DB.Query(ctx, selectDomainBenchmarksSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
				for rows.Next() {
					var br BenchmarkConfigRecord
					var effFrom time.Time
					var effTo *time.Time
					var kindStr, btStr, okStr, granStr string
					if err := rows.Scan(
						&br.BenchmarkID, &br.Revision, &effFrom, &effTo,
						&br.Name, &br.Publisher, &br.Region, &br.MetricID, &br.TemplateVersion,
						&br.Unit, &kindStr, &btStr, &okStr, &granStr,
						&br.MaximumValue, &br.MinimumDenominator, &br.SourceSystem,
					); err == nil {
						br.EffectiveFrom = effFrom.UTC()
						if effTo != nil && !effTo.IsZero() {
							t := effTo.UTC()
							br.EffectiveTo = &t
						}
						br.Kind = buildingenergy.BenchmarkKind(kindStr)
						br.BuildingType = buildingenergy.BuildingType(btStr)
						br.ObjectKind = buildingenergy.ObjectKind(okStr)
						br.PeriodGranularity = buildingenergy.Granularity(granStr)
						st.benchmarks = append(st.benchmarks, br)
					}
				}
				rows.Close()
			}
			if rows, err := s.DB.Query(ctx, selectDomainCustomTemplatesSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
				for rows.Next() {
					var ct CustomReportTemplateRecord
					if err := rows.Scan(&ct.TemplateID, &ct.Name, &ct.ScopeKind, &ct.Fields, &ct.CreatedBy, &ct.UpdatedAt); err == nil {
						st.customTemplates = append(st.customTemplates, ct)
					}
				}
				rows.Close()
			}
		}

		// 9. Migration 0003 unified simulation metadata + F15/F18-F20/F23-F24 tables
		var p2Ready bool
		_ = s.DB.QueryRow(ctx, checkP2ReadySQL).Scan(&p2Ready)
		st.p2Ready = p2Ready
		if p2Ready {
			var dm DatasetMeta
			var manifestRaw string
			if err := s.DB.QueryRow(ctx, selectDatasetMetaSQL, s.Project.TenantID, s.Project.ProjectID).Scan(
				&dm.DatasetID, &dm.DatasetName, &dm.DatasetVersion, &dm.Seed, &dm.IsSimulated,
				&dm.GeneratedAt, &dm.DataFrom, &dm.DataTo, &dm.HourlyFrom, &dm.RawFrom,
				&dm.BuildingScope, &dm.EnergyScope, &manifestRaw,
			); err == nil {
				_ = json.Unmarshal([]byte(manifestRaw), &dm.AnomalyManifest)
				st.dataset = &dm
				if dm.IsSimulated {
					st.dataOriginNotice = fmt.Sprintf(
						"【模拟数据】%s · 版本 %s（Seed %d）· 范围 %s · 时段 %s ~ %s",
						dm.DatasetName, dm.DatasetVersion, dm.Seed, dm.BuildingScope,
						dm.DataFrom.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02"),
						dm.DataTo.AddDate(0, 0, -1).In(time.FixedZone("CST", 8*3600)).Format("2006-01-02"),
					)
				}
			}
			if rows, err := s.DB.Query(ctx, selectAssessmentRulesSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
				for rows.Next() {
					var ar AssessmentRuleRecord
					var effFrom time.Time
					var effTo *time.Time
					var okStr, eStr string
					if err := rows.Scan(
						&ar.RuleID, &ar.Revision, &effFrom, &effTo, &okStr, &ar.ObjectID, &eStr,
						&ar.MetricLabel, &ar.TargetValue, &ar.Unit, &ar.WeightPct, &ar.PublicSharePct,
						&ar.Exempt, &ar.ExemptReason, &ar.ScoringFormula, &ar.MinCoverageRate,
						&ar.ReviewStatus, &ar.ReviewNote,
					); err == nil {
						ar.EffectiveFrom = effFrom.UTC()
						if effTo != nil && !effTo.IsZero() {
							t := effTo.UTC()
							ar.EffectiveTo = &t
						}
						ar.ObjectKind = buildingenergy.ObjectKind(okStr)
						ar.Energy = buildingenergy.EnergyType(eStr)
						st.assessmentRules = append(st.assessmentRules, ar)
					}
				}
				rows.Close()
			}
			var lifecycleReady bool
			_ = s.DB.QueryRow(ctx, checkRetrofitLifecycleReadySQL).Scan(&lifecycleReady)
			st.retrofitLifecycleReady = lifecycleReady
			st.retrofitBaselineRevisions = make(map[string][]RetrofitBaselineRevision)
			st.retrofitVerificationLogs = make(map[string][]RetrofitVerificationRecord)

			rfSQL := selectRetrofitProjectsSQL
			if lifecycleReady {
				rfSQL = selectRetrofitProjectsWithSpaceSQL
			}
			if rows, err := s.DB.Query(ctx, rfSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
				for rows.Next() {
					var rp RetrofitProjectRecord
					var eStr string
					if err := rows.Scan(
						&rp.RetrofitID, &rp.BuildingID, &rp.SpaceID, &eStr, &rp.Name, &rp.MeasureSummary, &rp.Unit,
						&rp.BaselineVersion, &rp.BaselineFrom, &rp.BaselineTo, &rp.BaselineModel, &rp.BaselineFactors,
						&rp.BaselineR2, &rp.BaselineCoverage, &rp.BaselineLocked, &rp.BaselineDailyAvg,
						&rp.EstSavingsLow, &rp.EstSavingsBase, &rp.EstSavingsHigh, &rp.RefUnitPrice, &rp.CapexBudget,
						&rp.ReportFrom, &rp.ReportTo, &rp.AdjustedBaselineUse, &rp.NonRoutineAdjustment,
						&rp.WeatherDataComplete, &rp.MVStandardRef, &rp.MVStatus, &rp.MVNote,
					); err == nil {
						rp.Energy = buildingenergy.EnergyType(eStr)
						st.retrofits = append(st.retrofits, rp)
					}
				}
				rows.Close()
			}
			if lifecycleReady {
				if rows, err := s.DB.Query(ctx, selectRetrofitBaselineRevisionsSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
					for rows.Next() {
						var rid string
						var rev RetrofitBaselineRevision
						if err := rows.Scan(
							&rid, &rev.BaselineVersion, &rev.BuildingID, &rev.SpaceID, &rev.Energy,
							&rev.BaselineFrom, &rev.BaselineTo, &rev.BaselineModel, &rev.BaselineFactors, &rev.BaselineR2,
							&rev.BaselineCoverage, &rev.BaselineLocked, &rev.BaselineDailyAvg, &rev.BaselineActualUsage,
							&rev.ChangeReason, &rev.ActorSub, &rev.RecordedAt,
						); err == nil {
							st.retrofitBaselineRevisions[rid] = append(st.retrofitBaselineRevisions[rid], rev)
						}
					}
					rows.Close()
				}
				if rows, err := s.DB.Query(ctx, selectRetrofitVerificationLogsSQL, s.Project.TenantID, s.Project.ProjectID); err == nil {
					for rows.Next() {
						var rid string
						var rec RetrofitVerificationRecord
						if err := rows.Scan(
							&rec.ID, &rid, &rec.BaselineVersion, &rec.ReportFrom, &rec.ReportTo,
							&rec.AdjustedBaselineUse, &rec.ReportActualUsage, &rec.ReportCoverageRate, &rec.NonRoutineAdjustment,
							&rec.VerifiedSavings, &rec.VerifiedSavingsRate, &rec.VerifiedCarbonKgCO2e,
							&rec.WeatherDataComplete, &rec.MVStatus, &rec.MVNote, &rec.ActorSub, &rec.RecordedAt,
						); err == nil {
							st.retrofitVerificationLogs[rid] = append(st.retrofitVerificationLogs[rid], rec)
						}
					}
					rows.Close()
				}
			}
		}
	} else {
		// energy_domain not migrated yet: keep public display list ONLY for inventory.
		for _, bp := range publicBuildings {
			st.buildingHistory = append(st.buildingHistory, bp)
		}
		for _, sp := range publicSpaces {
			st.spaceHistory = append(st.spaceHistory, sp)
		}
	}

	sort.Slice(st.buildingHistory, func(i, j int) bool {
		if st.buildingHistory[i].BuildingID != st.buildingHistory[j].BuildingID {
			return st.buildingHistory[i].BuildingID < st.buildingHistory[j].BuildingID
		}
		return st.buildingHistory[i].EffectiveFrom.Before(st.buildingHistory[j].EffectiveFrom)
	})
	sort.Slice(st.spaceHistory, func(i, j int) bool {
		if st.spaceHistory[i].SpaceID != st.spaceHistory[j].SpaceID {
			return st.spaceHistory[i].SpaceID < st.spaceHistory[j].SpaceID
		}
		return st.spaceHistory[i].EffectiveFrom.Before(st.spaceHistory[j].EffectiveFrom)
	})
	sort.Slice(st.orgHistory, func(i, j int) bool {
		if st.orgHistory[i].OrgID != st.orgHistory[j].OrgID {
			return st.orgHistory[i].OrgID < st.orgHistory[j].OrgID
		}
		return st.orgHistory[i].EffectiveFrom.Before(st.orgHistory[j].EffectiveFrom)
	})

	// Build latest views for inventory dropdowns (distinct by ID, taking most recent effectiveFrom).
	st.buildings = latestBuildings(st.buildingHistory)
	st.spaces = latestSpaces(st.spaceHistory)
	st.organizations = latestOrgs(st.orgHistory)

	// Enrich meta.Meters with P0/P1 configured energy_domain profiles (NEVER silently fabricating profiles!).
	for i := range st.meta.Meters {
		m := &st.meta.Meters[i]
		pk := pointKey{TerminalID: m.TerminalID, ItemCode: m.ItemCode}
		pm, configured := st.configuredPoints[pk]
		if !configured || len(pm.Profiles) == 0 {
			continue
		}
		latestProf := pm.Profiles[len(pm.Profiles)-1]
		m.Configured = true
		m.PointEnabled = pm.Enabled
		m.StatusReason = pm.StatusReason
		m.Generation = pm.Generation
		m.ProfileVersion = latestProf.Revision
		m.SourceTable = latestProf.SourceTable
		m.Energy = latestProf.Energy
		m.MeasurementKind = latestProf.MeasurementKind
		m.Unit = latestProf.Unit
		m.Multiplier = latestProf.Multiplier
		m.RegisterModulus = latestProf.RegisterModulus
		m.RegisterEpoch = latestProf.RegisterEpoch
		m.CadenceSeconds = latestProf.CadenceSeconds
		ef := latestProf.EffectiveFrom
		m.EffectiveFrom = &ef
		m.EffectiveTo = latestProf.EffectiveTo
		if pm.Enabled {
			m.ChannelStatus = pgadapter.ChannelConfigured
		} else {
			m.ChannelStatus = pgadapter.ChannelDisabled
		}
		for _, b := range pm.Bindings {
			if len(b.Allocations) == 0 {
				continue
			}
			switch b.ViewKind {
			case buildingenergy.Building:
				m.BoundBuildingID = b.Allocations[0].TargetID
			case buildingenergy.Space:
				m.BoundSpaceID = b.Allocations[0].TargetID
			case buildingenergy.Organization:
				m.BoundOrgID = b.Allocations[0].TargetID
			}
		}
	}

	st.catalog = &domainCatalog{
		project:          s.Project,
		timezone:         s.timezone(),
		effectiveAt:      effAt,
		buildingHistory:  st.buildingHistory,
		spaceHistory:     st.spaceHistory,
		orgHistory:       st.orgHistory,
		configuredPoints: st.configuredPoints,
	}
	return st, nil
}

func latestBuildings(history []BuildingProfile) []BuildingProfile {
	byID := map[string]BuildingProfile{}
	for _, b := range history {
		prev, ok := byID[b.BuildingID]
		if !ok || !b.EffectiveFrom.Before(prev.EffectiveFrom) {
			byID[b.BuildingID] = b
		}
	}
	out := make([]BuildingProfile, 0, len(byID))
	for _, b := range byID {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BuildingID < out[j].BuildingID })
	return out
}

func latestSpaces(history []SpaceProfile) []SpaceProfile {
	byID := map[string]SpaceProfile{}
	for _, sp := range history {
		prev, ok := byID[sp.SpaceID]
		if !ok || !sp.EffectiveFrom.Before(prev.EffectiveFrom) {
			byID[sp.SpaceID] = sp
		}
	}
	out := make([]SpaceProfile, 0, len(byID))
	for _, sp := range byID {
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SpaceID < out[j].SpaceID })
	return out
}

func latestOrgs(history []OrganizationProfile) []OrganizationProfile {
	byID := map[string]OrganizationProfile{}
	for _, o := range history {
		prev, ok := byID[o.OrgID]
		if !ok || !o.EffectiveFrom.Before(prev.EffectiveFrom) {
			byID[o.OrgID] = o
		}
	}
	out := make([]OrganizationProfile, 0, len(byID))
	for _, o := range byID {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OrgID < out[j].OrgID })
	return out
}

// Effective-dated object selectors for a statistical window (P3 historical rule: NEVER blindly take latest!).

func (st *liveState) buildingEffectiveAt(buildingID string, at time.Time) (BuildingProfile, bool) {
	var best *BuildingProfile
	for i := range st.buildingHistory {
		b := &st.buildingHistory[i]
		if b.BuildingID != buildingID {
			continue
		}
		if rangeCoversInstant(b.EffectiveFrom, b.EffectiveTo, at) {
			if best == nil || !b.EffectiveFrom.Before(best.EffectiveFrom) {
				best = b
			}
		}
	}
	if best != nil {
		return *best, true
	}
	return BuildingProfile{}, false
}

func (st *liveState) buildingsEffectiveAt(at time.Time) []BuildingProfile {
	byID := map[string]BuildingProfile{}
	for _, b := range st.buildingHistory {
		if rangeCoversInstant(b.EffectiveFrom, b.EffectiveTo, at) {
			prev, ok := byID[b.BuildingID]
			if !ok || !b.EffectiveFrom.Before(prev.EffectiveFrom) {
				byID[b.BuildingID] = b
			}
		}
	}
	out := make([]BuildingProfile, 0, len(byID))
	for _, b := range byID {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BuildingID < out[j].BuildingID })
	return out
}

func (st *liveState) spacesEffectiveAt(at time.Time) []SpaceProfile {
	byID := map[string]SpaceProfile{}
	for _, sp := range st.spaceHistory {
		if rangeCoversInstant(sp.EffectiveFrom, sp.EffectiveTo, at) {
			prev, ok := byID[sp.SpaceID]
			if !ok || !sp.EffectiveFrom.Before(prev.EffectiveFrom) {
				byID[sp.SpaceID] = sp
			}
		}
	}
	out := make([]SpaceProfile, 0, len(byID))
	for _, sp := range byID {
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SpaceID < out[j].SpaceID })
	return out
}

func (st *liveState) orgsEffectiveAt(at time.Time) []OrganizationProfile {
	byID := map[string]OrganizationProfile{}
	for _, o := range st.orgHistory {
		if rangeCoversInstant(o.EffectiveFrom, o.EffectiveTo, at) {
			prev, ok := byID[o.OrgID]
			if !ok || !o.EffectiveFrom.Before(prev.EffectiveFrom) {
				byID[o.OrgID] = o
			}
		}
	}
	out := make([]OrganizationProfile, 0, len(byID))
	for _, o := range byID {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OrgID < out[j].OrgID })
	return out
}

func (st *liveState) topologyEffectiveForWindow(buildingID string, energy buildingenergy.EnergyType, rng buildingenergy.Range) []TopologyEdge {
	var matching []TopologyEdge
	for _, e := range st.topology {
		if e.BuildingID != buildingID || e.Energy != energy {
			continue
		}
		if rangeCoversInstant(e.EffectiveFrom, e.EffectiveTo, rng.From) {
			matching = append(matching, e)
		}
	}
	if len(matching) == 0 {
		return nil
	}
	// Keep all edges belonging to the most recent effective topology_version active at rng.From.
	sort.Slice(matching, func(i, j int) bool {
		if !matching[i].EffectiveFrom.Equal(matching[j].EffectiveFrom) {
			return matching[i].EffectiveFrom.After(matching[j].EffectiveFrom)
		}
		return matching[i].TopologyVersion > matching[j].TopologyVersion
	})
	ver := matching[0].TopologyVersion
	var out []TopologyEdge
	for _, e := range matching {
		if e.TopologyVersion == ver {
			out = append(out, e)
		}
	}
	return out
}

// ── domainCatalog (Effective-Dated buildingenergy.Catalog + MappingReader) ──

type domainCatalog struct {
	project          buildingenergy.ProjectKey
	timezone         string
	effectiveAt      time.Time
	buildingHistory  []BuildingProfile
	spaceHistory     []SpaceProfile
	orgHistory       []OrganizationProfile
	configuredPoints map[pointKey]*configuredPointMeta
}

func (c *domainCatalog) Project(_ context.Context, key buildingenergy.ProjectKey, at time.Time) (buildingenergy.Project, error) {
	if c == nil || key != c.project || at.Before(c.effectiveAt) {
		return buildingenergy.Project{}, buildingenergy.ErrCatalogScope
	}
	return buildingenergy.Project{
		Key:      c.project,
		Timezone: c.timezone,
		Meta: buildingenergy.VersionMeta{
			Version:   "energy-domain-proj-v1",
			Effective: buildingenergy.Range{From: c.effectiveAt},
			Source:    buildingenergy.SourceRef{System: "energy-admin", ID: key.ProjectID},
		},
	}, nil
}

func (c *domainCatalog) Object(_ context.Context, project buildingenergy.ProjectKey, key buildingenergy.ObjectKey, at time.Time) (buildingenergy.Object, error) {
	if c == nil || project != c.project {
		return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
	}
	switch key.Kind {
	case buildingenergy.Building:
		var best *BuildingProfile
		for i := range c.buildingHistory {
			b := &c.buildingHistory[i]
			if b.BuildingID == key.ID && rangeCoversInstant(b.EffectiveFrom, b.EffectiveTo, at) {
				if best == nil || !b.EffectiveFrom.Before(best.EffectiveFrom) {
					best = b
				}
			}
		}
		if best != nil {
			return buildingenergy.Object{
				Project:    project,
				Key:        key,
				BuildingID: best.BuildingID,
				Meta: buildingenergy.VersionMeta{
					Version:   best.Revision,
					Effective: rangeFromBounds(best.EffectiveFrom, best.EffectiveTo),
					Source:    buildingenergy.SourceRef{System: best.SourceSystem, ID: best.BuildingID},
				},
			}, nil
		}
	case buildingenergy.Space:
		var best *SpaceProfile
		for i := range c.spaceHistory {
			sp := &c.spaceHistory[i]
			if sp.SpaceID == key.ID && rangeCoversInstant(sp.EffectiveFrom, sp.EffectiveTo, at) {
				if best == nil || !sp.EffectiveFrom.Before(best.EffectiveFrom) {
					best = sp
				}
			}
		}
		if best != nil {
			parent := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: best.BuildingID}
			if best.ParentSpaceID != "" {
				parent = buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: best.ParentSpaceID}
			}
			return buildingenergy.Object{
				Project:    project,
				Key:        key,
				Parent:     parent,
				BuildingID: best.BuildingID,
				Meta: buildingenergy.VersionMeta{
					Version:   best.Revision,
					Effective: rangeFromBounds(best.EffectiveFrom, best.EffectiveTo),
					Source:    buildingenergy.SourceRef{System: best.SourceSystem, ID: best.SpaceID},
				},
			}, nil
		}
	case buildingenergy.Organization:
		var best *OrganizationProfile
		for i := range c.orgHistory {
			o := &c.orgHistory[i]
			if o.OrgID == key.ID && rangeCoversInstant(o.EffectiveFrom, o.EffectiveTo, at) {
				if best == nil || !o.EffectiveFrom.Before(best.EffectiveFrom) {
					best = o
				}
			}
		}
		if best != nil {
			var parent buildingenergy.ObjectKey
			if best.ParentOrgID != "" {
				parent = buildingenergy.ObjectKey{Kind: buildingenergy.Organization, ID: best.ParentOrgID}
			}
			return buildingenergy.Object{
				Project: project,
				Key:     key,
				Parent:  parent,
				Meta: buildingenergy.VersionMeta{
					Version:   best.Revision,
					Effective: rangeFromBounds(best.EffectiveFrom, best.EffectiveTo),
					Source:    buildingenergy.SourceRef{System: best.SourceSystem, ID: best.OrgID},
				},
			}, nil
		}
	}
	return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
}

func (c *domainCatalog) ReadMapping(_ context.Context, point buildingenergy.PointRef, generation uint64) (buildingenergy.MappingTimeline, error) {
	if c == nil || point.Project != c.project {
		return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
	}
	pm, ok := c.configuredPoints[point.Key]
	if !ok || !pm.Enabled || pm.Generation != generation || len(pm.Timeline.Profiles) == 0 {
		return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
	}
	return pm.Timeline, nil
}

// ── P0/P2 Strict Point Selection, Source Dispatch & Classification Artifacts ──

func (st *liveState) registerEventResolver() *pgadapter.StaticRegisterEventResolver {
	entries := make([]pgadapter.RegisterEventEntry, 0, len(st.registerEvents))
	for _, ev := range st.registerEvents {
		entries = append(entries, pgadapter.RegisterEventEntry{
			TerminalID:  ev.TerminalID,
			ItemCode:    ev.ItemCode,
			EventKind:   ev.EventKind,
			EffectiveAt: ev.EffectiveAt,
			NewEpoch:    ev.NewEpoch,
		})
	}
	return &pgadapter.StaticRegisterEventResolver{Events: entries}
}

// configuredPointsForScope returns ONLY explicitly configured, enabled, non-instantaneous
// leaf metering points bound to at least one target object in `objects` (for `view`) and
// matching `energy` (or any energy when empty) during `rng`.
//
// PRD CALC-02 non-double-counting guarantee: any point that is a parent total meter
// (parent_terminal_id in active energy_domain.meter_topology covering rng) is excluded
// from leaf consumption aggregation because its direct submeters cover the same physical
// load; F09 Balance explicitly loads both parent total meters and direct submeters.
func (st *liveState) configuredPointsForScope(
	view buildingenergy.ObjectKind,
	objects []buildingenergy.ObjectKey,
	energy buildingenergy.EnergyType,
	rng buildingenergy.Range,
) []*configuredPointMeta {
	parentTotals := map[pointKey]bool{}
	for _, edge := range st.topology {
		if rangeOverlaps(edge.EffectiveFrom, edge.EffectiveTo, rng) {
			parentTotals[pointKey{TerminalID: edge.ParentTerminalID, ItemCode: edge.ParentItemCode}] = true
		}
	}
	var out []*configuredPointMeta
	for _, pm := range st.configuredPoints {
		if !pm.Enabled || len(pm.Timeline.Profiles) == 0 || parentTotals[pm.Key] {
			continue
		}
		// Must have at least one profile covering rng with matching energy and consumptive measurement_kind.
		hasProfile := false
		for _, pr := range pm.Timeline.Profiles {
			if pr.Profile.Kind == buildingenergy.Instantaneous {
				continue
			}
			if energy != "" && pr.Profile.Energy != energy {
				continue
			}
			var effTo *time.Time
			if !pr.Profile.Meta.Effective.To.IsZero() {
				t := pr.Profile.Meta.Effective.To
				effTo = &t
			}
			if rangeOverlaps(pr.Profile.Meta.Effective.From, effTo, rng) {
				hasProfile = true
				break
			}
		}
		if !hasProfile {
			continue
		}
		// Must have at least one active binding for `view` pointing into `objects` overlapping `rng`.
		hasBinding := false
		for _, br := range pm.Timeline.Bindings {
			if br.View != view {
				continue
			}
			var effTo *time.Time
			if !br.Meta.Effective.To.IsZero() {
				t := br.Meta.Effective.To
				effTo = &t
			}
			if !rangeOverlaps(br.Meta.Effective.From, effTo, rng) {
				continue
			}
			for _, alloc := range br.Allocations {
				if len(objects) == 0 || slices.Contains(objects, alloc.Target) {
					hasBinding = true
					break
				}
			}
			if hasBinding {
				break
			}
		}
		if hasBinding {
			out = append(out, pm)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.TerminalID != out[j].Key.TerminalID {
			return out[i].Key.TerminalID < out[j].Key.TerminalID
		}
		return out[i].Key.ItemCode < out[j].Key.ItemCode
	})
	return out
}

func (st *liveState) terminalName(pk pointKey) string {
	for _, m := range st.meta.Meters {
		if m.TerminalID == pk.TerminalID && m.ItemCode == pk.ItemCode {
			return m.TerminalName
		}
	}
	return pk.TerminalID
}

// buildFormalClassificationArtifacts builds M1-C snapshots STRICTLY from energy_domain.energy_category
// and energy_domain.point_category_binding (supporting multi-category weight_ppm & historical effective
// revisions). ZERO keyword inference! Unbound configured points go directly to M1-C Unclassified.
func (s *Service) buildFormalClassificationArtifacts(
	st *liveState,
	points []*configuredPointMeta,
	rng buildingenergy.Range,
) (buildingenergy.ClassificationSnapshot, buildingenergy.CategoryTopologySnapshot) {
	nodes := make([]buildingenergy.CategoryNode, 0, len(st.categories))
	nodeEnergies := map[buildingenergy.EnergyType]bool{}
	for _, c := range st.categories {
		nodes = append(nodes, buildingenergy.CategoryNode{
			ID:       c.CategoryID,
			ParentID: c.ParentID,
			Name:     c.Name,
			Energy:   c.Energy,
		})
		nodeEnergies[c.Energy] = true
	}
	// M1-C CalculateClassifiedUsage requires a non-empty Nodes slice per catalog.
	// When no formal energy_category has been created yet, supply an unbound sentinel node
	// so 100% of configured meter usage flows cleanly into M1-C Unclassified ("未分项")
	// without fabricating keyword-inferred categories (P2-7 & P2-8).
	if len(nodes) == 0 {
		for _, pm := range points {
			if len(pm.Profiles) > 0 && !nodeEnergies[pm.Profiles[0].Energy] {
				e := pm.Profiles[0].Energy
				nodeEnergies[e] = true
				nodes = append(nodes, buildingenergy.CategoryNode{
					ID:     "_unconfigured_taxonomy_" + string(e),
					Name:   "待配置分项",
					Energy: e,
				})
			}
		}
	}
	pointSet := make(map[pointKey]bool, len(points))
	var boundaries [][]buildingenergy.CategoryBoundaryContributor
	for _, pm := range points {
		pointSet[pm.Key] = true
		ch := buildingenergy.SourceRef{System: "iot-terminal", ID: pm.Key.TerminalID + ":" + pm.Key.ItemCode}
		if len(pm.Timeline.Profiles) > 0 && pm.Timeline.Profiles[0].Channel.System != "" {
			ch = pm.Timeline.Profiles[0].Channel
		}
		boundaries = append(boundaries, []buildingenergy.CategoryBoundaryContributor{{
			BoundaryContributor: buildingenergy.BoundaryContributor{
				Point: buildingenergy.PointRef{Project: s.Project, Key: pm.Key},
				Role:  buildingenergy.BoundaryTotal,
			},
			MappingGeneration: pm.Generation,
			Channel:           ch,
		}})
	}

	var bindings []buildingenergy.CategoryBinding
	for _, cb := range st.categoryBindings {
		pk := pointKey{TerminalID: cb.TerminalID, ItemCode: cb.ItemCode}
		if !pointSet[pk] {
			continue
		}
		if !rangeOverlaps(cb.EffectiveFrom, cb.EffectiveTo, rng) {
			continue
		}
		allocs := make([]buildingenergy.CategoryAllocation, 0, len(cb.Allocations))
		for _, a := range cb.Allocations {
			allocs = append(allocs, buildingenergy.CategoryAllocation{
				CategoryID: a.CategoryID,
				WeightPPM:  int(a.WeightPPM),
			})
		}
		if len(allocs) == 0 {
			continue
		}
		bindings = append(bindings, buildingenergy.CategoryBinding{
			Point: buildingenergy.PointRef{Project: s.Project, Key: pk},
			Meta: buildingenergy.VersionMeta{
				Version:   cb.Revision,
				Effective: rangeFromBounds(cb.EffectiveFrom, cb.EffectiveTo),
				Source:    buildingenergy.SourceRef{System: "energy-admin", ID: cb.Revision},
			},
			Allocations: allocs,
		})
	}

	metaEff := buildingenergy.Range{From: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	meta := buildingenergy.VersionMeta{
		Version:   "energy-domain-taxonomy-v1",
		Effective: metaEff,
		Source:    buildingenergy.SourceRef{System: "energy-admin", ID: s.Project.ProjectID},
	}
	return buildingenergy.ClassificationSnapshot{
			Project:  s.Project,
			Meta:     meta,
			Nodes:    nodes,
			Bindings: bindings,
		}, buildingenergy.CategoryTopologySnapshot{
			Project:    s.Project,
			Meta:       meta,
			Boundaries: boundaries,
		}
}

// calculateConfiguredInputs executes M1-A calculation per configured point, dispatching
// cleanly between SnapshotCoarseMeteringSource (snapshot_coarse) and RawMeteringSource (raw_item)
// according to the point's effective metering_point_profile.source_table.
func (s *Service) calculateConfiguredInputs(
	ctx context.Context,
	st *liveState,
	view buildingenergy.ObjectKind,
	authorizedObjects []buildingenergy.ObjectKey,
	points []*configuredPointMeta,
	win buildingenergy.Window,
) ([]buildingenergy.CategoryPointResult, []LiveSourceTrace, error) {
	if s.Guard == nil {
		return nil, nil, buildingenergy.ErrAccessDenied
	}
	evResolver := st.registerEventResolver()
	authorizer := &pgadapter.MappingPointAuthorizer{
		Identity: s.Guard.Identity,
		Policy:   s.Guard.Policy,
		Catalog:  st.catalog,
		Mappings: st.catalog,
	}
	snapSrc := &pgadapter.SnapshotCoarseMeteringSource{
		DB:         s.DB,
		Authorizer: authorizer,
		Events:     evResolver,
	}
	rawSrc := &pgadapter.RawMeteringSource{
		DB:         s.DB,
		Authorizer: authorizer,
		Events:     evResolver,
	}

	inputs := make([]buildingenergy.CategoryPointResult, 0, len(points))
	traces := make([]LiveSourceTrace, 0, len(points))

	for _, pm := range points {
		point := buildingenergy.PointRef{Project: s.Project, Key: pm.Key}
		// Resolve which authorized objects this point actually binds to in this window.
		var pointObjs []buildingenergy.ObjectKey
		bindVer := ""
		for _, br := range pm.Timeline.Bindings {
			if br.View != view {
				continue
			}
			var effTo *time.Time
			if !br.Meta.Effective.To.IsZero() {
				t := br.Meta.Effective.To
				effTo = &t
			}
			if !rangeOverlaps(br.Meta.Effective.From, effTo, win.Range) {
				continue
			}
			bindVer = br.Meta.Version
			for _, a := range br.Allocations {
				if (len(authorizedObjects) == 0 || slices.Contains(authorizedObjects, a.Target)) && !slices.Contains(pointObjs, a.Target) {
					pointObjs = append(pointObjs, a.Target)
				}
			}
		}
		if len(pointObjs) == 0 {
			continue
		}

		// Determine active source_table & profile metadata for win.Range.
		srcTable := pgadapter.SourceTableSnapshotCoarse
		activeEpoch := ""
		var activeMod *float64
		for _, pr := range pm.Profiles {
			if rangeOverlaps(pr.EffectiveFrom, pr.EffectiveTo, win.Range) {
				srcTable = pr.SourceTable
				activeEpoch = pr.RegisterEpoch
				activeMod = pr.RegisterModulus
				break
			}
		}
		var meteringSrc buildingenergy.MeteringSource = snapSrc
		sourceSystem := "iot-snapshot-coarse"
		if srcTable == pgadapter.SourceTableRawItem {
			meteringSrc = rawSrc
			sourceSystem = "iot-raw-item"
		}

		readCtx := pgadapter.WithPointReadBinding(ctx, pgadapter.PointReadBinding{
			View:       view,
			Generation: pm.Generation,
			Objects:    pointObjs,
		})
		res, err := buildingenergy.CalculateFromSource(readCtx, meteringSrc, point, win)
		if err != nil {
			return nil, nil, err
		}
		inputs = append(inputs, buildingenergy.CategoryPointResult{Result: res, Mapping: pm.Timeline})

		catBindVer := ""
		for _, cb := range st.categoryBindings {
			if cb.TerminalID == pm.Key.TerminalID && cb.ItemCode == pm.Key.ItemCode && rangeOverlaps(cb.EffectiveFrom, cb.EffectiveTo, win.Range) {
				catBindVer = cb.Revision
			}
		}

		traces = append(traces, LiveSourceTrace{
			TerminalID:             pm.Key.TerminalID,
			TerminalName:           st.terminalName(pm.Key),
			ItemCode:               pm.Key.ItemCode,
			SourceTable:            srcTable,
			SourceSystem:           sourceSystem,
			MeasurementKind:        res.Profile.Kind,
			Usage:                  res.Usage,
			Unit:                   res.Unit,
			Multiplier:             res.Profile.Multiplier,
			CadenceSeconds:         int(res.Profile.Cadence.Seconds()),
			RegisterEpoch:          activeEpoch,
			RegisterModulus:        activeMod,
			Valid:                  res.Valid,
			Expected:               res.Expected,
			Missing:                res.Missing,
			Suspect:                res.Suspect,
			ProfileVersion:         res.Profile.Meta.Version,
			ObjectBindingVersion:   bindVer,
			CategoryBindingVersion: catBindVer,
			MappingGeneration:      pm.Generation,
		})
	}
	return inputs, traces, nil
}
