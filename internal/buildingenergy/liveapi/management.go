package liveapi

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/pgadapter"
)

// Inventory returns the full building-energy governance inventory: P0 channel
// candidates (configured / disabled / unconfigured / excluded_non_energy),
// buildings, spaces, organizations (with revision history), categories, category
// bindings, physical topology, register events, and F05/F10/F12/F13/F25 configs.
func (s *Service) Inventory(ctx context.Context) (InventoryResponse, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return InventoryResponse{}, err
	}
	if s.Guard == nil {
		return InventoryResponse{}, buildingenergy.ErrAccessDenied
	}
	now := s.now().UTC()
	if _, err := s.Guard.AuthorizeScope(ctx, st.catalog, buildingenergy.ReadUsage, buildingenergy.Scope{
		Project: s.Project,
		Period:  buildingenergy.Range{From: now.Add(-time.Hour), To: now},
	}); err != nil {
		return InventoryResponse{}, err
	}
	var cfgCnt, uncfgCnt, exclCnt, disCnt int
	for _, m := range st.meta.Meters {
		switch m.ChannelStatus {
		case pgadapter.ChannelConfigured:
			cfgCnt++
		case pgadapter.ChannelDisabled:
			disCnt++
		case pgadapter.ChannelExcludedNonEnergy:
			exclCnt++
		default:
			uncfgCnt++
		}
	}
	return InventoryResponse{
		DataMode:              "live_database",
		DataOriginNotice:      st.dataOriginNotice,
		Dataset:               st.dataset,
		Project:               s.Project,
		Timezone:              s.timezone(),
		EnergyDomainReady:     st.energyDomainReady,
		ConfiguredMeterCount:  cfgCnt,
		UnconfiguredCount:     uncfgCnt,
		ExcludedChannelCount:  exclCnt,
		DisabledMeterCount:    disCnt,
		Buildings:             st.buildings,
		BuildingHistory:       st.buildingHistory,
		Spaces:                st.spaces,
		SpaceHistory:          st.spaceHistory,
		Organizations:         st.organizations,
		OrganizationHistory:   st.orgHistory,
		Meters:                st.meta.Meters,
		Categories:            st.categories,
		CategoryBindings:      st.categoryBindings,
		Topology:              st.topology,
		RegisterEvents:        st.registerEvents,
		Targets:               st.targets,
		AnomalyRules:          st.anomalyRules,
		Denominators:          st.denominators,
		Benchmarks:            st.benchmarks,
		CustomReportTemplates: st.customTemplates,
		Exceptions:            st.exceptions,
	}, nil
}

// MeteringPointHistory returns the full versioned generation/profile/binding/
// category/event history for one metering point (P1).
func (s *Service) MeteringPointHistory(ctx context.Context, terminalID, itemCode string) (MeteringPointHistoryResponse, error) {
	terminalID = strings.TrimSpace(terminalID)
	itemCode = strings.TrimSpace(itemCode)
	if terminalID == "" || itemCode == "" {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: terminalId and itemCode are required", ErrBadRequest)
	}
	st, err := s.loadState(ctx)
	if err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	if s.Guard == nil {
		return MeteringPointHistoryResponse{}, buildingenergy.ErrAccessDenied
	}
	now := s.now().UTC()
	if _, err := s.Guard.AuthorizeScope(ctx, st.catalog, buildingenergy.ReadUsage, buildingenergy.Scope{
		Project: s.Project,
		Period:  buildingenergy.Range{From: now.Add(-time.Hour), To: now},
	}); err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	resp := MeteringPointHistoryResponse{
		TerminalID: terminalID,
		ItemCode:   itemCode,
	}
	pk := pointKey{TerminalID: terminalID, ItemCode: itemCode}
	if pm, ok := st.configuredPoints[pk]; ok {
		resp.CurrentGen = pm.Generation
		resp.Enabled = pm.Enabled
		resp.StatusReason = pm.StatusReason
	}
	for _, cb := range st.categoryBindings {
		if cb.TerminalID == terminalID && cb.ItemCode == itemCode {
			resp.CategoryBindings = append(resp.CategoryBindings, cb)
		}
	}
	for _, ev := range st.registerEvents {
		if ev.TerminalID == terminalID && ev.ItemCode == itemCode {
			resp.RegisterEvents = append(resp.RegisterEvents, ev)
		}
	}
	if !st.energyDomainReady {
		return resp, nil
	}

	const selectPointGensSQL = `
SELECT generation, previous_generation, recorded_at, published_by_issuer || ':' || published_by_sub, reason
FROM energy_domain.metering_point_generation
WHERE tenant_id = $1 AND project_id = $2 AND terminal_id = $3 AND item_code = $4
ORDER BY generation DESC`

	genMap := map[uint64]int{}
	if rows, err := s.DB.Query(ctx, selectPointGensSQL, s.Project.TenantID, s.Project.ProjectID, terminalID, itemCode); err == nil {
		for rows.Next() {
			var g MeteringGenerationRecord
			if err := rows.Scan(&g.Generation, &g.PreviousGen, &g.RecordedAt, &g.PublishedBy, &g.Reason); err == nil {
				genMap[g.Generation] = len(resp.Generations)
				resp.Generations = append(resp.Generations, g)
			}
		}
		rows.Close()
	}

	const selectPointAllProfilesSQL = `
SELECT generation, revision, lower(effective), upper(effective), source_table, energy_type, measurement_kind,
       unit, multiplier::double precision, register_modulus::double precision, coalesce(register_epoch, ''), coalesce(cadence_seconds, 3600)
FROM energy_domain.metering_point_profile
WHERE tenant_id = $1 AND project_id = $2 AND terminal_id = $3 AND item_code = $4
ORDER BY generation DESC, lower(effective) ASC`

	if rows, err := s.DB.Query(ctx, selectPointAllProfilesSQL, s.Project.TenantID, s.Project.ProjectID, terminalID, itemCode); err == nil {
		for rows.Next() {
			var pr MeteringProfileRevisionRecord
			var effFrom time.Time
			var effTo *time.Time
			var srcStr, eStr, kStr string
			var mult *float64
			if err := rows.Scan(&pr.Generation, &pr.Revision, &effFrom, &effTo, &srcStr, &eStr, &kStr, &pr.Unit, &mult, &pr.RegisterModulus, &pr.RegisterEpoch, &pr.CadenceSeconds); err == nil {
				pr.EffectiveFrom = effFrom.UTC()
				if effTo != nil && !effTo.IsZero() {
					t := effTo.UTC()
					pr.EffectiveTo = &t
				}
				pr.SourceTable = pgadapter.SourceTable(srcStr)
				pr.Energy = buildingenergy.EnergyType(eStr)
				pr.MeasurementKind = buildingenergy.MeasurementKind(kStr)
				if mult != nil {
					pr.Multiplier = *mult
				}
				if idx, ok := genMap[pr.Generation]; ok {
					resp.Generations[idx].Profiles = append(resp.Generations[idx].Profiles, pr)
				}
			}
		}
		rows.Close()
	}

	const selectPointAllBindingsSQL = `
SELECT b.generation, b.view_kind, b.revision, lower(b.effective), upper(b.effective),
       a.target_id, a.target_revision, a.weight_ppm
FROM energy_domain.metering_point_binding b
JOIN energy_domain.metering_point_binding_allocation a
  ON a.tenant_id = b.tenant_id AND a.project_id = b.project_id
 AND a.terminal_id = b.terminal_id AND a.item_code = b.item_code
 AND a.generation = b.generation AND a.view_kind = b.view_kind
 AND a.binding_revision = b.revision
WHERE b.tenant_id = $1 AND b.project_id = $2 AND b.terminal_id = $3 AND b.item_code = $4
ORDER BY b.generation DESC, b.view_kind ASC, lower(b.effective) ASC, a.target_id ASC`

	type bgKey struct {
		gen  uint64
		view string
		rev  string
	}
	bgIdx := map[bgKey]int{}
	if rows, err := s.DB.Query(ctx, selectPointAllBindingsSQL, s.Project.TenantID, s.Project.ProjectID, terminalID, itemCode); err == nil {
		for rows.Next() {
			var gen uint64
			var viewStr, rev, targetID, targetRev string
			var effFrom time.Time
			var effTo *time.Time
			var wPPM int64
			if err := rows.Scan(&gen, &viewStr, &rev, &effFrom, &effTo, &targetID, &targetRev, &wPPM); err == nil {
				gi, ok := genMap[gen]
				if !ok {
					continue
				}
				k := bgKey{gen, viewStr, rev}
				bi, exists := bgIdx[k]
				if !exists {
					bi = len(resp.Generations[gi].Bindings)
					bgIdx[k] = bi
					rec := MeteringBindingRevisionRecord{
						Generation:    gen,
						ViewKind:      buildingenergy.ObjectKind(viewStr),
						Revision:      rev,
						EffectiveFrom: effFrom.UTC(),
					}
					if effTo != nil && !effTo.IsZero() {
						t := effTo.UTC()
						rec.EffectiveTo = &t
					}
					resp.Generations[gi].Bindings = append(resp.Generations[gi].Bindings, rec)
				}
				resp.Generations[gi].Bindings[bi].Allocations = append(resp.Generations[gi].Bindings[bi].Allocations, MeteringBindingAllocationRecord{
					TargetID: targetID, TargetRevision: targetRev, WeightPPM: wPPM,
				})
			}
		}
		rows.Close()
	}

	return resp, nil
}

// PublishMeteringPoint creates a new immutable generation for a metering point in
// energy_domain (P0 + P1), preserving historical revisions when PreserveHistory=true
// and rejecting non-energy electrical monitoring channels.
func (s *Service) PublishMeteringPoint(ctx context.Context, req PublishMeteringPointRequest) (MeteringPointHistoryResponse, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	if !st.energyDomainReady {
		return MeteringPointHistoryResponse{}, ErrEnergySchemaNotReady
	}
	req.TerminalID = strings.TrimSpace(req.TerminalID)
	req.ItemCode = strings.TrimSpace(req.ItemCode)
	req.Unit = strings.TrimSpace(req.Unit)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.TerminalID == "" || req.ItemCode == "" || req.Energy == "" || req.MeasurementKind == "" || req.Unit == "" {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: terminalId, itemCode, energy, measurementKind, and unit are required", ErrBadRequest)
	}
	if req.MeasurementKind == buildingenergy.Instantaneous {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: instantaneous channels cannot be published as formal energy consumption points (P0-5)", ErrBadRequest)
	}
	if req.MeasurementKind != buildingenergy.Cumulative && req.MeasurementKind != buildingenergy.Interval {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: measurementKind must be cumulative or interval", ErrBadRequest)
	}
	if req.SourceTable == "" {
		req.SourceTable = pgadapter.SourceTableSnapshotCoarse
	}
	if req.SourceTable != pgadapter.SourceTableSnapshotCoarse && req.SourceTable != pgadapter.SourceTableRawItem {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: sourceTable must be snapshot_coarse or raw_item", ErrBadRequest)
	}
	if req.SourceTable == pgadapter.SourceTableSnapshotCoarse && (req.MeasurementKind != buildingenergy.Cumulative || (req.CadenceSeconds != 0 && req.CadenceSeconds != 3600)) {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: snapshot_coarse requires cumulative measurementKind and 3600s cadence (use raw_item for interval or sub-hourly cadence)", ErrBadRequest)
	}
	if req.Multiplier <= 0 || math.IsNaN(req.Multiplier) || math.IsInf(req.Multiplier, 0) {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: multiplier must be > 0", ErrBadRequest)
	}
	if req.RegisterModulus != nil && (*req.RegisterModulus <= 0 || math.IsNaN(*req.RegisterModulus) || math.IsInf(*req.RegisterModulus, 0)) {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: registerModulus must be > 0", ErrBadRequest)
	}
	if req.CadenceSeconds == 0 {
		req.CadenceSeconds = 3600
	}
	if req.CadenceSeconds < 60 || req.CadenceSeconds > 3600 || req.CadenceSeconds%60 != 0 || 3600%req.CadenceSeconds != 0 {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: cadenceSeconds must divide 3600 evenly in [60, 3600]", ErrBadRequest)
	}
	if req.EffectiveFrom.IsZero() {
		req.EffectiveFrom = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if req.EffectiveTo != nil && !req.EffectiveTo.IsZero() && !req.EffectiveFrom.Before(*req.EffectiveTo) {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: effectiveTo must be after effectiveFrom", ErrBadRequest)
	}
	if req.Reason == "" {
		req.Reason = "metering point configuration published"
	}

	// Verify channel exists in iot-admin and is NOT an excluded electrical monitoring channel (P0-2).
	var foundMeter *pgadapter.IOTMeterRecord
	for i := range st.meta.Meters {
		m := &st.meta.Meters[i]
		if m.TerminalID == req.TerminalID && m.ItemCode == req.ItemCode {
			foundMeter = m
			break
		}
	}
	if foundMeter == nil {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: terminal/item_code %s/%s not found in iot-admin device catalog", ErrNotFound, req.TerminalID, req.ItemCode)
	}
	cls := pgadapter.ClassifyChannelCandidate(foundMeter.DomainCode, foundMeter.DomainName, foundMeter.TypeCode, foundMeter.TypeName, foundMeter.ItemCode, foundMeter.ItemName, foundMeter.Unit)
	if cls.Status == pgadapter.ChannelExcludedNonEnergy {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: %s", ErrBadRequest, cls.ExclusionReason)
	}

	// Resolve authoritative target_revision slices across [req.EffectiveFrom, req.EffectiveTo) (P0-1 & P3).
	// When a target object has multiple historical revisions (e.g., area updated mid-year),
	// we automatically split the binding into contiguous non-overlapping slices so every slice's
	// target_revision matches the authoritative catalog revision active in that sub-interval.
	type resolvedAlloc struct {
		targetID  string
		targetRev string
		weightPPM int64
	}
	type bindingSlice struct {
		eff    buildingenergy.Range
		allocs []resolvedAlloc
	}
	resolveViewSlices := func(kind buildingenergy.ObjectKind, inputs []ObjectAllocationInput) ([]bindingSlice, error) {
		if len(inputs) == 0 {
			return nil, nil
		}
		var sum int64
		seen := map[string]bool{}
		normalized := make([]ObjectAllocationInput, 0, len(inputs))
		for _, in := range inputs {
			id := strings.TrimSpace(in.TargetID)
			if id == "" || seen[id] {
				return nil, fmt.Errorf("%w: invalid or duplicate targetId %q", ErrBadRequest, in.TargetID)
			}
			w := in.WeightPPM
			if w <= 0 {
				w = buildingenergy.FullWeightPPM
			}
			sum += w
			if sum > buildingenergy.FullWeightPPM {
				return nil, fmt.Errorf("%w: sum of %s allocation weights exceeds 1,000,000 ppm", ErrBadRequest, kind)
			}
			seen[id] = true
			normalized = append(normalized, ObjectAllocationInput{TargetID: id, WeightPPM: w})
		}

		// Collect all revision boundary instants within (effStart, effEnd).
		effStart := req.EffectiveFrom.UTC()
		var effEnd time.Time
		if req.EffectiveTo != nil && !req.EffectiveTo.IsZero() {
			effEnd = req.EffectiveTo.UTC()
		}
		cutSet := map[int64]time.Time{effStart.UnixNano(): effStart}
		addBound := func(t time.Time) {
			if t.IsZero() {
				return
			}
			t = t.UTC()
			if t.After(effStart) && (effEnd.IsZero() || t.Before(effEnd)) {
				cutSet[t.UnixNano()] = t
			}
		}
		for _, in := range normalized {
			switch kind {
			case buildingenergy.Building:
				for _, b := range st.buildingHistory {
					if b.BuildingID == in.TargetID {
						addBound(b.EffectiveFrom)
						if b.EffectiveTo != nil {
							addBound(*b.EffectiveTo)
						}
					}
				}
			case buildingenergy.Space:
				for _, sp := range st.spaceHistory {
					if sp.SpaceID == in.TargetID {
						addBound(sp.EffectiveFrom)
						if sp.EffectiveTo != nil {
							addBound(*sp.EffectiveTo)
						}
					}
				}
			case buildingenergy.Organization:
				for _, o := range st.orgHistory {
					if o.OrgID == in.TargetID {
						addBound(o.EffectiveFrom)
						if o.EffectiveTo != nil {
							addBound(*o.EffectiveTo)
						}
					}
				}
			}
		}
		cuts := make([]time.Time, 0, len(cutSet))
		for _, t := range cutSet {
			cuts = append(cuts, t)
		}
		for i := 0; i < len(cuts); i++ {
			for j := i + 1; j < len(cuts); j++ {
				if cuts[j].Before(cuts[i]) {
					cuts[i], cuts[j] = cuts[j], cuts[i]
				}
			}
		}

		slicesOut := make([]bindingSlice, 0, len(cuts))
		for i, from := range cuts {
			var to time.Time
			if i+1 < len(cuts) {
				to = cuts[i+1]
			} else {
				to = effEnd
			}
			allocs := make([]resolvedAlloc, 0, len(normalized))
			for _, in := range normalized {
				obj, err := st.catalog.Object(ctx, s.Project, buildingenergy.ObjectKey{Kind: kind, ID: in.TargetID}, from)
				if err != nil {
					return nil, fmt.Errorf("%w: authoritative %s %q not effective at %s", ErrBadRequest, kind, in.TargetID, from.Format(time.RFC3339))
				}
				allocs = append(allocs, resolvedAlloc{targetID: in.TargetID, targetRev: obj.Meta.Version, weightPPM: in.WeightPPM})
			}
			slicesOut = append(slicesOut, bindingSlice{
				eff:    buildingenergy.Range{From: from, To: to},
				allocs: allocs,
			})
		}
		return slicesOut, nil
	}

	bldgSlices, err := resolveViewSlices(buildingenergy.Building, req.BuildingAlloc)
	if err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	spaceSlices, err := resolveViewSlices(buildingenergy.Space, req.SpaceAlloc)
	if err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	orgSlices, err := resolveViewSlices(buildingenergy.Organization, req.OrganizationAlloc)
	if err != nil {
		return MeteringPointHistoryResponse{}, err
	}

	sub, err := s.Guard.Identity.AuthenticatedSubject(ctx)
	if err != nil {
		return MeteringPointHistoryResponse{}, buildingenergy.ErrAccessDenied
	}

	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const initPointHeadSQL = `
INSERT INTO energy_domain.metering_point_head (tenant_id, project_id, terminal_id, item_code, current_generation)
VALUES ($1, $2, $3, $4, NULL)
ON CONFLICT (tenant_id, project_id, terminal_id, item_code) DO NOTHING`
	if _, err := tx.Exec(ctx, initPointHeadSQL, s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode); err != nil {
		return MeteringPointHistoryResponse{}, err
	}

	const lockPointHeadSQL = `
SELECT current_generation
FROM energy_domain.metering_point_head
WHERE tenant_id = $1 AND project_id = $2 AND terminal_id = $3 AND item_code = $4
FOR UPDATE`
	var prevGen *int64
	if err := tx.QueryRow(ctx, lockPointHeadSQL, s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode).Scan(&prevGen); err != nil {
		return MeteringPointHistoryResponse{}, err
	}

	nextGen := int64(1)
	if prevGen != nil && *prevGen > 0 {
		nextGen = *prevGen + 1
	}

	const insertPointGenSQL = `
INSERT INTO energy_domain.metering_point_generation
  (tenant_id, project_id, terminal_id, item_code, generation, previous_generation, published_by_issuer, published_by_sub, approved_at, approval_system, approval_id, reason)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'energy-admin', $10, $11)`
	now := s.now().UTC()
	approvalID := fmt.Sprintf("mp-%s-%s-g%d", req.TerminalID, req.ItemCode, nextGen)
	if _, err := tx.Exec(ctx, insertPointGenSQL,
		s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode,
		nextGen, prevGen, sub.Issuer, sub.ID, now, approvalID, req.Reason,
	); err != nil {
		return MeteringPointHistoryResponse{}, err
	}

	const insertPointProfileSQL = `
INSERT INTO energy_domain.metering_point_profile
  (tenant_id, project_id, terminal_id, item_code, generation, revision, effective,
   source_table, source_system, source_id, channel_system, channel_id, register_epoch,
   measurement_kind, energy_type, unit, multiplier, register_modulus, cadence_seconds)
VALUES ($1, $2, $3, $4, $5, $6, $7::tstzrange, $8, 'energy-admin', $9, 'iot-terminal', $10, $11, $12, $13, $14, $15, $16, $17)`

	const insertPointBindingSQL = `
INSERT INTO energy_domain.metering_point_binding
  (tenant_id, project_id, terminal_id, item_code, generation, view_kind, revision, effective, source_system, source_id, catalog_snapshot_ref)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::tstzrange, 'energy-admin', $9, $10)`

	const insertPointAllocSQL = `
INSERT INTO energy_domain.metering_point_binding_allocation
  (tenant_id, project_id, terminal_id, item_code, generation, view_kind, binding_revision, target_id, target_revision, weight_ppm)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	// Preserve non-overlapping historical slices from previous generation strictly before req.EffectiveFrom.
	pk := pointKey{TerminalID: req.TerminalID, ItemCode: req.ItemCode}
	cutAt := req.EffectiveFrom.UTC()
	if req.PreserveHistory && prevGen != nil {
		if prevPM, ok := st.configuredPoints[pk]; ok {
			for i, pr := range prevPM.Profiles {
				if !pr.EffectiveFrom.Before(cutAt) {
					continue
				}
				end := cutAt
				if pr.EffectiveTo != nil && pr.EffectiveTo.Before(end) {
					end = *pr.EffectiveTo
				}
				effStr, err := pgadapter.FormatTstzrange(buildingenergy.Range{From: pr.EffectiveFrom, To: end})
				if err != nil {
					return MeteringPointHistoryResponse{}, err
				}
				var ep *string
				if pr.RegisterEpoch != "" {
					e := pr.RegisterEpoch
					ep = &e
				}
				rev := fmt.Sprintf("%s-h%d", pr.Revision, i+1)
				if _, err := tx.Exec(ctx, insertPointProfileSQL,
					s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode, nextGen, rev, effStr,
					string(pr.SourceTable), req.TerminalID+":"+req.ItemCode, req.TerminalID+":"+req.ItemCode, ep,
					string(pr.MeasurementKind), string(pr.Energy), pr.Unit, pr.Multiplier, pr.RegisterModulus, pr.CadenceSeconds,
				); err != nil {
					return MeteringPointHistoryResponse{}, err
				}
			}
			for i, br := range prevPM.Bindings {
				if !br.EffectiveFrom.Before(cutAt) {
					continue
				}
				end := cutAt
				if br.EffectiveTo != nil && br.EffectiveTo.Before(end) {
					end = *br.EffectiveTo
				}
				effStr, err := pgadapter.FormatTstzrange(buildingenergy.Range{From: br.EffectiveFrom, To: end})
				if err != nil {
					return MeteringPointHistoryResponse{}, err
				}
				rev := fmt.Sprintf("%s-h%d", br.Revision, i+1)
				if _, err := tx.Exec(ctx, insertPointBindingSQL,
					s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode, nextGen,
					string(br.ViewKind), rev, effStr, req.TerminalID, approvalID,
				); err != nil {
					return MeteringPointHistoryResponse{}, err
				}
				for _, al := range br.Allocations {
					if _, err := tx.Exec(ctx, insertPointAllocSQL,
						s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode, nextGen,
						string(br.ViewKind), rev, al.TargetID, al.TargetRevision, al.WeightPPM,
					); err != nil {
						return MeteringPointHistoryResponse{}, err
					}
				}
			}
		}
	}

	// Insert new profile revision.
	newEff := rangeFromBounds(req.EffectiveFrom, req.EffectiveTo)
	effStr, err := pgadapter.FormatTstzrange(newEff)
	if err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	epoch := strings.TrimSpace(req.RegisterEpoch)
	if epoch == "" {
		epoch = fmt.Sprintf("epoch-g%d", nextGen)
	}
	profRev := fmt.Sprintf("prof-%s-g%d", req.TerminalID, nextGen)
	if _, err := tx.Exec(ctx, insertPointProfileSQL,
		s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode, nextGen, profRev, effStr,
		string(req.SourceTable), req.TerminalID+":"+req.ItemCode, req.TerminalID+":"+req.ItemCode, &epoch,
		string(req.MeasurementKind), string(req.Energy), req.Unit, req.Multiplier, req.RegisterModulus, req.CadenceSeconds,
	); err != nil {
		return MeteringPointHistoryResponse{}, err
	}

	// Insert multi-view object bindings (building / space / organization) split cleanly by authoritative object revisions.
	writeView := func(kind buildingenergy.ObjectKind, slices []bindingSlice) error {
		for idx, sl := range slices {
			if len(sl.allocs) == 0 {
				continue
			}
			slEffStr, err := pgadapter.FormatTstzrange(sl.eff)
			if err != nil {
				return err
			}
			bindRev := fmt.Sprintf("bind-%s-%s-g%d-s%d", kind, req.TerminalID, nextGen, idx+1)
			if _, err := tx.Exec(ctx, insertPointBindingSQL,
				s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode, nextGen,
				string(kind), bindRev, slEffStr, req.TerminalID, approvalID,
			); err != nil {
				return err
			}
			for _, al := range sl.allocs {
				if _, err := tx.Exec(ctx, insertPointAllocSQL,
					s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode, nextGen,
					string(kind), bindRev, al.targetID, al.targetRev, al.weightPPM,
				); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := writeView(buildingenergy.Building, bldgSlices); err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	if err := writeView(buildingenergy.Space, spaceSlices); err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	if err := writeView(buildingenergy.Organization, orgSlices); err != nil {
		return MeteringPointHistoryResponse{}, err
	}

	const updatePointHeadSQL = `
UPDATE energy_domain.metering_point_head
SET current_generation = $5
WHERE tenant_id = $1 AND project_id = $2 AND terminal_id = $3 AND item_code = $4`
	if _, err := tx.Exec(ctx, updatePointHeadSQL, s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode, nextGen); err != nil {
		return MeteringPointHistoryResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	return s.MeteringPointHistory(ctx, req.TerminalID, req.ItemCode)
}

// SetMeteringPointStatus enables or disables a configured metering point (P1).
func (s *Service) SetMeteringPointStatus(ctx context.Context, req SetMeteringPointStatusRequest) (MeteringPointHistoryResponse, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	if !st.energyDomainReady || !st.governanceReady {
		return MeteringPointHistoryResponse{}, ErrEnergySchemaNotReady
	}
	req.TerminalID = strings.TrimSpace(req.TerminalID)
	req.ItemCode = strings.TrimSpace(req.ItemCode)
	if req.TerminalID == "" || req.ItemCode == "" {
		return MeteringPointHistoryResponse{}, fmt.Errorf("%w: terminalId and itemCode are required", ErrBadRequest)
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const setHeadStatusSQL = `
UPDATE energy_domain.metering_point_head
SET enabled = $5, status_reason = $6, updated_at = clock_timestamp()
WHERE tenant_id = $1 AND project_id = $2 AND terminal_id = $3 AND item_code = $4`
	tag, err := tx.Exec(ctx, setHeadStatusSQL, s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode, req.Enabled, strings.TrimSpace(req.Reason))
	if err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	if tag.RowsAffected() == 0 {
		return MeteringPointHistoryResponse{}, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return MeteringPointHistoryResponse{}, err
	}
	return s.MeteringPointHistory(ctx, req.TerminalID, req.ItemCode)
}

// UpsertBuilding creates/updates an effective-dated building revision (P3).
func (s *Service) UpsertBuilding(ctx context.Context, req UpsertBuildingRequest) (BuildingProfile, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return BuildingProfile{}, err
	}
	if !st.energyDomainReady {
		return BuildingProfile{}, ErrEnergySchemaNotReady
	}
	req.BuildingID = strings.TrimSpace(req.BuildingID)
	req.Name = strings.TrimSpace(req.Name)
	if req.BuildingID == "" || req.Name == "" {
		return BuildingProfile{}, fmt.Errorf("%w: buildingId and name are required", ErrBadRequest)
	}
	if strings.TrimSpace(req.Revision) == "" {
		req.Revision = fmt.Sprintf("%s-rev-%d", req.BuildingID, s.now().UTC().UnixNano()/1e6)
	}
	if strings.TrimSpace(req.BuildingType) == "" {
		req.BuildingType = "office"
	}
	if strings.TrimSpace(req.Timezone) == "" {
		req.Timezone = s.timezone()
	}
	effFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if req.EffectiveFrom != nil && !req.EffectiveFrom.IsZero() {
		effFrom = req.EffectiveFrom.UTC()
	}
	eff := rangeFromBounds(effFrom, req.EffectiveTo)
	effStr, err := pgadapter.FormatTstzrange(eff)
	if err != nil {
		return BuildingProfile{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return BuildingProfile{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if st.governanceReady {
		const upsertBldgWithRegionSQL = `
INSERT INTO energy_domain.building_revision
  (tenant_id, project_id, building_id, revision, effective, name, building_type, region, gross_area_sqm, occupied_headcount, timezone, legacy_building_id, source_system, source_id)
VALUES ($1, $2, $3, $4, $5::tstzrange, $6, $7, $8, $9, $10, $11, $12, 'energy-admin', $3)
ON CONFLICT (tenant_id, project_id, building_id, revision) DO UPDATE
SET effective = EXCLUDED.effective, name = EXCLUDED.name, building_type = EXCLUDED.building_type, region = EXCLUDED.region,
    gross_area_sqm = EXCLUDED.gross_area_sqm, occupied_headcount = EXCLUDED.occupied_headcount,
    timezone = EXCLUDED.timezone, legacy_building_id = EXCLUDED.legacy_building_id`
		if _, err := tx.Exec(ctx, upsertBldgWithRegionSQL,
			s.Project.TenantID, s.Project.ProjectID, req.BuildingID, req.Revision, effStr,
			req.Name, req.BuildingType, strings.TrimSpace(req.Region), req.GrossAreaSqm, req.OccupiedHeadcount, req.Timezone, req.LegacyBuildingID); err != nil {
			return BuildingProfile{}, err
		}
	} else {
		const upsertBldgBaseSQL = `
INSERT INTO energy_domain.building_revision
  (tenant_id, project_id, building_id, revision, effective, name, building_type, gross_area_sqm, occupied_headcount, timezone, legacy_building_id, source_system, source_id)
VALUES ($1, $2, $3, $4, $5::tstzrange, $6, $7, $8, $9, $10, $11, 'energy-admin', $3)
ON CONFLICT (tenant_id, project_id, building_id, revision) DO UPDATE
SET effective = EXCLUDED.effective, name = EXCLUDED.name, building_type = EXCLUDED.building_type,
    gross_area_sqm = EXCLUDED.gross_area_sqm, occupied_headcount = EXCLUDED.occupied_headcount,
    timezone = EXCLUDED.timezone, legacy_building_id = EXCLUDED.legacy_building_id`
		if _, err := tx.Exec(ctx, upsertBldgBaseSQL,
			s.Project.TenantID, s.Project.ProjectID, req.BuildingID, req.Revision, effStr,
			req.Name, req.BuildingType, req.GrossAreaSqm, req.OccupiedHeadcount, req.Timezone, req.LegacyBuildingID); err != nil {
			return BuildingProfile{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return BuildingProfile{}, err
	}
	return BuildingProfile{
		BuildingID: req.BuildingID, Revision: req.Revision, Name: req.Name, BuildingType: req.BuildingType,
		Region: strings.TrimSpace(req.Region), GrossAreaSqm: req.GrossAreaSqm, OccupiedHeadcount: req.OccupiedHeadcount,
		Timezone: req.Timezone, EffectiveFrom: effFrom, EffectiveTo: req.EffectiveTo,
		LegacyBuildingID: req.LegacyBuildingID, SourceSystem: "energy-admin",
	}, nil
}

// UpsertSpace creates/updates an effective-dated space revision (P3).
func (s *Service) UpsertSpace(ctx context.Context, req UpsertSpaceRequest) (SpaceProfile, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return SpaceProfile{}, err
	}
	if !st.energyDomainReady {
		return SpaceProfile{}, ErrEnergySchemaNotReady
	}
	req.SpaceID = strings.TrimSpace(req.SpaceID)
	req.BuildingID = strings.TrimSpace(req.BuildingID)
	req.Name = strings.TrimSpace(req.Name)
	if req.SpaceID == "" || req.BuildingID == "" || req.Name == "" {
		return SpaceProfile{}, fmt.Errorf("%w: spaceId, buildingId, and name are required", ErrBadRequest)
	}
	if strings.TrimSpace(req.Revision) == "" {
		req.Revision = fmt.Sprintf("%s-rev-%d", req.SpaceID, s.now().UTC().UnixNano()/1e6)
	}
	effFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if req.EffectiveFrom != nil && !req.EffectiveFrom.IsZero() {
		effFrom = req.EffectiveFrom.UTC()
	}
	effStr, err := pgadapter.FormatTstzrange(rangeFromBounds(effFrom, req.EffectiveTo))
	if err != nil {
		return SpaceProfile{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	var parent *string
	if p := strings.TrimSpace(req.ParentSpaceID); p != "" {
		parent = &p
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return SpaceProfile{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upsertSpaceSQL = `
INSERT INTO energy_domain.space_revision
  (tenant_id, project_id, space_id, revision, effective, building_id, parent_space_id, name, area_sqm, legacy_area_id, source_system, source_id)
VALUES ($1, $2, $3, $4, $5::tstzrange, $6, $7, $8, $9, $10, 'energy-admin', $3)
ON CONFLICT (tenant_id, project_id, space_id, revision) DO UPDATE
SET effective = EXCLUDED.effective, building_id = EXCLUDED.building_id, parent_space_id = EXCLUDED.parent_space_id,
    name = EXCLUDED.name, area_sqm = EXCLUDED.area_sqm, legacy_area_id = EXCLUDED.legacy_area_id`
	if _, err := tx.Exec(ctx, upsertSpaceSQL,
		s.Project.TenantID, s.Project.ProjectID, req.SpaceID, req.Revision, effStr,
		req.BuildingID, parent, req.Name, req.AreaSqm, req.LegacyAreaID); err != nil {
		return SpaceProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SpaceProfile{}, err
	}
	return SpaceProfile{
		SpaceID: req.SpaceID, Revision: req.Revision, BuildingID: req.BuildingID, ParentSpaceID: req.ParentSpaceID,
		Name: req.Name, AreaSqm: req.AreaSqm, EffectiveFrom: effFrom, EffectiveTo: req.EffectiveTo,
		LegacyAreaID: req.LegacyAreaID, SourceSystem: "energy-admin",
	}, nil
}

// UpsertOrganization creates/updates an effective-dated organization revision (P3).
func (s *Service) UpsertOrganization(ctx context.Context, req UpsertOrganizationRequest) (OrganizationProfile, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return OrganizationProfile{}, err
	}
	if !st.energyDomainReady {
		return OrganizationProfile{}, ErrEnergySchemaNotReady
	}
	req.OrgID = strings.TrimSpace(req.OrgID)
	req.Name = strings.TrimSpace(req.Name)
	if req.OrgID == "" || req.Name == "" {
		return OrganizationProfile{}, fmt.Errorf("%w: orgId and name are required", ErrBadRequest)
	}
	if strings.TrimSpace(req.Revision) == "" {
		req.Revision = fmt.Sprintf("%s-rev-%d", req.OrgID, s.now().UTC().UnixNano()/1e6)
	}
	var parent *string
	if p := strings.TrimSpace(req.ParentOrgID); p != "" {
		parent = &p
	}
	effFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if req.EffectiveFrom != nil && !req.EffectiveFrom.IsZero() {
		effFrom = req.EffectiveFrom.UTC()
	}
	effStr, err := pgadapter.FormatTstzrange(rangeFromBounds(effFrom, req.EffectiveTo))
	if err != nil {
		return OrganizationProfile{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return OrganizationProfile{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upsertOrgSQL = `
INSERT INTO energy_domain.organization_revision
  (tenant_id, project_id, org_id, revision, effective, parent_org_id, name, headcount, source_system, source_id)
VALUES ($1, $2, $3, $4, $5::tstzrange, $6, $7, $8, 'energy-admin', $3)
ON CONFLICT (tenant_id, project_id, org_id, revision) DO UPDATE
SET effective = EXCLUDED.effective, parent_org_id = EXCLUDED.parent_org_id, name = EXCLUDED.name, headcount = EXCLUDED.headcount`
	if _, err := tx.Exec(ctx, upsertOrgSQL,
		s.Project.TenantID, s.Project.ProjectID, req.OrgID, req.Revision, effStr, parent, req.Name, req.Headcount); err != nil {
		return OrganizationProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrganizationProfile{}, err
	}
	return OrganizationProfile{
		OrgID: req.OrgID, Revision: req.Revision, ParentOrgID: req.ParentOrgID, Name: req.Name,
		Headcount: req.Headcount, EffectiveFrom: effFrom, EffectiveTo: req.EffectiveTo, SourceSystem: "energy-admin",
	}, nil
}

// UpsertCategory creates/updates a formal energy category tree node (P2).
func (s *Service) UpsertCategory(ctx context.Context, req UpsertCategoryRequest) (CategoryNodeRecord, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return CategoryNodeRecord{}, err
	}
	if !st.energyDomainReady {
		return CategoryNodeRecord{}, ErrEnergySchemaNotReady
	}
	req.CategoryID = strings.TrimSpace(req.CategoryID)
	req.ParentID = strings.TrimSpace(req.ParentID)
	req.Name = strings.TrimSpace(req.Name)
	if req.CategoryID == "" || req.Name == "" || req.Energy == "" {
		return CategoryNodeRecord{}, fmt.Errorf("%w: categoryId, name, and energy are required", ErrBadRequest)
	}
	if req.ParentID == req.CategoryID {
		return CategoryNodeRecord{}, fmt.Errorf("%w: category cannot be its own parent", ErrBadRequest)
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return CategoryNodeRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upsertCatSQL = `
INSERT INTO energy_domain.energy_category (tenant_id, project_id, category_id, parent_id, name, energy_type)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, project_id, category_id) DO UPDATE
SET parent_id = EXCLUDED.parent_id, name = EXCLUDED.name, energy_type = EXCLUDED.energy_type`
	if _, err := tx.Exec(ctx, upsertCatSQL, s.Project.TenantID, s.Project.ProjectID, req.CategoryID, req.ParentID, req.Name, string(req.Energy)); err != nil {
		return CategoryNodeRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CategoryNodeRecord{}, err
	}
	return CategoryNodeRecord{CategoryID: req.CategoryID, ParentID: req.ParentID, Name: req.Name, Energy: req.Energy}, nil
}

// PublishCategoryBinding publishes a versioned, effective-dated point-to-category
// weighted allocation (P2: supports multi-category splits & historical versions).
func (s *Service) PublishCategoryBinding(ctx context.Context, req PublishCategoryBindingRequest) (PointCategoryBindingRecord, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return PointCategoryBindingRecord{}, err
	}
	if !st.energyDomainReady {
		return PointCategoryBindingRecord{}, ErrEnergySchemaNotReady
	}
	req.TerminalID = strings.TrimSpace(req.TerminalID)
	req.ItemCode = strings.TrimSpace(req.ItemCode)
	if req.TerminalID == "" || req.ItemCode == "" || len(req.Allocations) == 0 {
		return PointCategoryBindingRecord{}, fmt.Errorf("%w: terminalId, itemCode, and at least one category allocation are required", ErrBadRequest)
	}
	if strings.TrimSpace(req.Revision) == "" {
		req.Revision = fmt.Sprintf("catbind-%s-%d", req.TerminalID, s.now().UTC().UnixNano()/1e6)
	}
	var sum int64
	seen := map[string]bool{}
	for i := range req.Allocations {
		req.Allocations[i].CategoryID = strings.TrimSpace(req.Allocations[i].CategoryID)
		if req.Allocations[i].CategoryID == "" || seen[req.Allocations[i].CategoryID] || req.Allocations[i].WeightPPM <= 0 {
			return PointCategoryBindingRecord{}, fmt.Errorf("%w: duplicate or invalid category allocation", ErrBadRequest)
		}
		seen[req.Allocations[i].CategoryID] = true
		sum += req.Allocations[i].WeightPPM
	}
	if sum > buildingenergy.FullWeightPPM {
		return PointCategoryBindingRecord{}, fmt.Errorf("%w: sum of category weights (%d) exceeds 1,000,000 ppm", ErrBadRequest, sum)
	}
	effFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if req.EffectiveFrom != nil && !req.EffectiveFrom.IsZero() {
		effFrom = req.EffectiveFrom.UTC()
	}
	effStr, err := pgadapter.FormatTstzrange(rangeFromBounds(effFrom, req.EffectiveTo))
	if err != nil {
		return PointCategoryBindingRecord{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return PointCategoryBindingRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertCatBindSQL = `
INSERT INTO energy_domain.point_category_binding
  (tenant_id, project_id, terminal_id, item_code, category_id, revision, effective, weight_ppm)
VALUES ($1, $2, $3, $4, $5, $6, $7::tstzrange, $8)
ON CONFLICT (tenant_id, project_id, terminal_id, item_code, category_id, revision) DO UPDATE
SET effective = EXCLUDED.effective, weight_ppm = EXCLUDED.weight_ppm`
	for _, al := range req.Allocations {
		if _, err := tx.Exec(ctx, insertCatBindSQL,
			s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode,
			al.CategoryID, req.Revision, effStr, al.WeightPPM); err != nil {
			return PointCategoryBindingRecord{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PointCategoryBindingRecord{}, err
	}
	return PointCategoryBindingRecord{
		TerminalID: req.TerminalID, ItemCode: req.ItemCode, Revision: req.Revision,
		EffectiveFrom: effFrom, EffectiveTo: req.EffectiveTo, Allocations: req.Allocations,
	}, nil
}

// PublishTopology publishes a versioned, effective-dated physical total-to-submeter topology (P3).
func (s *Service) PublishTopology(ctx context.Context, req PublishTopologyRequest) ([]TopologyEdge, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return nil, err
	}
	if !st.energyDomainReady {
		return nil, ErrEnergySchemaNotReady
	}
	req.BuildingID = strings.TrimSpace(req.BuildingID)
	req.ParentTerminalID = strings.TrimSpace(req.ParentTerminalID)
	req.ParentItemCode = strings.TrimSpace(req.ParentItemCode)
	if req.BuildingID == "" || req.Energy == "" || req.ParentTerminalID == "" || req.ParentItemCode == "" || len(req.Children) == 0 {
		return nil, fmt.Errorf("%w: buildingId, energy, parent meter, and at least one child submeter are required", ErrBadRequest)
	}
	if strings.TrimSpace(req.TopologyVersion) == "" {
		req.TopologyVersion = fmt.Sprintf("topo-%s-%d", req.BuildingID, s.now().UTC().UnixNano()/1e6)
	}
	effFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if req.EffectiveFrom != nil && !req.EffectiveFrom.IsZero() {
		effFrom = req.EffectiveFrom.UTC()
	}
	effStr, err := pgadapter.FormatTstzrange(rangeFromBounds(effFrom, req.EffectiveTo))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertTopoSQL = `
INSERT INTO energy_domain.meter_topology
  (tenant_id, project_id, topology_version, effective, building_id, energy_type,
   parent_terminal_id, parent_item_code, child_terminal_id, child_item_code, zone_code, source_system, source_id)
VALUES ($1, $2, $3, $4::tstzrange, $5, $6, $7, $8, $9, $10, $11, 'energy-admin', $3)
ON CONFLICT (tenant_id, project_id, topology_version, building_id, energy_type, parent_terminal_id, child_terminal_id) DO UPDATE
SET effective = EXCLUDED.effective, parent_item_code = EXCLUDED.parent_item_code,
    child_item_code = EXCLUDED.child_item_code, zone_code = EXCLUDED.zone_code`

	edges := make([]TopologyEdge, 0, len(req.Children))
	for _, ch := range req.Children {
		ctid := strings.TrimSpace(ch.TerminalID)
		citem := strings.TrimSpace(ch.ItemCode)
		zone := strings.TrimSpace(ch.ZoneCode)
		if ctid == "" || citem == "" || zone == "" || ctid == req.ParentTerminalID {
			return nil, fmt.Errorf("%w: invalid child submeter %+v", ErrBadRequest, ch)
		}
		if _, err := tx.Exec(ctx, insertTopoSQL,
			s.Project.TenantID, s.Project.ProjectID, req.TopologyVersion, effStr,
			req.BuildingID, string(req.Energy), req.ParentTerminalID, req.ParentItemCode, ctid, citem, zone); err != nil {
			return nil, err
		}
		edges = append(edges, TopologyEdge{
			TopologyVersion: req.TopologyVersion, EffectiveFrom: effFrom, EffectiveTo: req.EffectiveTo,
			BuildingID: req.BuildingID, Energy: req.Energy,
			ParentTerminalID: req.ParentTerminalID, ParentItemCode: req.ParentItemCode,
			ChildTerminalID: ctid, ChildItemCode: citem, ZoneCode: zone,
		})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return edges, nil
}

// RecordRegisterEvent appends a confirmed meter swap/rollover/quality event (P1).
func (s *Service) RecordRegisterEvent(ctx context.Context, req RecordEventRequest) (RegisterEventRecord, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return RegisterEventRecord{}, err
	}
	if !st.energyDomainReady {
		return RegisterEventRecord{}, ErrEnergySchemaNotReady
	}
	if strings.TrimSpace(req.TerminalID) == "" || strings.TrimSpace(req.ItemCode) == "" ||
		strings.TrimSpace(req.EventKind) == "" || req.EffectiveAt.IsZero() || strings.TrimSpace(req.Reason) == "" {
		return RegisterEventRecord{}, fmt.Errorf("%w: terminalId, itemCode, eventKind, effectiveAt, and reason are required", ErrBadRequest)
	}
	sub, err := s.Guard.Identity.AuthenticatedSubject(ctx)
	if err != nil {
		return RegisterEventRecord{}, buildingenergy.ErrAccessDenied
	}
	var newEpoch *string
	if e := strings.TrimSpace(req.NewEpoch); e != "" {
		newEpoch = &e
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return RegisterEventRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const insertEventSQL = `
INSERT INTO energy_domain.meter_register_event
  (tenant_id, project_id, terminal_id, item_code, event_kind, effective_at, new_epoch, reason, actor_issuer, actor_sub)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id`
	var id int64
	if err := tx.QueryRow(ctx, insertEventSQL,
		s.Project.TenantID, s.Project.ProjectID, req.TerminalID, req.ItemCode, req.EventKind,
		req.EffectiveAt.UTC(), newEpoch, req.Reason, sub.Issuer, sub.ID).Scan(&id); err != nil {
		return RegisterEventRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RegisterEventRecord{}, err
	}
	return RegisterEventRecord{
		ID: id, TerminalID: req.TerminalID, ItemCode: req.ItemCode, EventKind: req.EventKind,
		EffectiveAt: req.EffectiveAt.UTC(), NewEpoch: req.NewEpoch, Reason: req.Reason, Actor: sub.Issuer + ":" + sub.ID,
	}, nil
}

// PublishTarget stores a versioned annual energy target for F05/F14 (P4).
func (s *Service) PublishTarget(ctx context.Context, req PublishTargetRequest) (TargetConfigRecord, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return TargetConfigRecord{}, err
	}
	if !st.governanceReady {
		return TargetConfigRecord{}, ErrEnergySchemaNotReady
	}
	if req.ObjectKind == "" {
		req.ObjectKind = buildingenergy.Building
	}
	req.ObjectID = strings.TrimSpace(req.ObjectID)
	req.Unit = strings.TrimSpace(req.Unit)
	if req.ObjectID == "" || req.Energy == "" || req.Year < 2000 || req.Year > 2100 || req.Unit == "" || req.AnnualValue < 0 {
		return TargetConfigRecord{}, fmt.Errorf("%w: invalid target request", ErrBadRequest)
	}
	if req.Method == "" {
		req.Method = buildingenergy.TargetLinear
	}
	loc, err := time.LoadLocation(s.timezone())
	if err != nil {
		return TargetConfigRecord{}, err
	}
	yearStart := time.Date(req.Year, 1, 1, 0, 0, 0, 0, loc)
	yearEnd := time.Date(req.Year+1, 1, 1, 0, 0, 0, 0, loc)
	effFrom := yearStart
	if req.EffectiveFrom != nil && !req.EffectiveFrom.IsZero() {
		effFrom = req.EffectiveFrom.UTC()
	}
	effTo := yearEnd
	if req.EffectiveTo != nil && !req.EffectiveTo.IsZero() {
		effTo = req.EffectiveTo.UTC()
	}
	if strings.TrimSpace(req.Revision) == "" {
		req.Revision = fmt.Sprintf("tgt-%s-%d-%d", req.ObjectID, req.Year, s.now().UTC().UnixNano()/1e6)
	}
	var weights32 []int32
	if req.Method == buildingenergy.TargetWeighted {
		if len(req.WeightPPM) != 12 {
			return TargetConfigRecord{}, fmt.Errorf("%w: weighted target requires 12 monthly weights summing to 1,000,000", ErrBadRequest)
		}
		var sum int64
		weights32 = make([]int32, 12)
		for i, w := range req.WeightPPM {
			if w < 0 {
				return TargetConfigRecord{}, fmt.Errorf("%w: negative weight", ErrBadRequest)
			}
			sum += w
			weights32[i] = int32(w)
		}
		if sum != buildingenergy.FullWeightPPM {
			return TargetConfigRecord{}, fmt.Errorf("%w: weighted target monthly weights must sum to 1,000,000 (got %d)", ErrBadRequest, sum)
		}
	}
	annualMilli := int64(math.Round(req.AnnualValue * 1000))
	effStr, err := pgadapter.FormatTstzrange(buildingenergy.Range{From: effFrom.UTC(), To: effTo.UTC()})
	if err != nil {
		return TargetConfigRecord{}, err
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return TargetConfigRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upsertTargetSQL = `
INSERT INTO energy_domain.energy_target_revision
  (tenant_id, project_id, object_kind, object_id, energy_type, target_year, revision, effective, unit, annual_milli, method, weight_ppm, source_system, source_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::tstzrange, $9, $10, $11, $12, 'energy-admin', $7)
ON CONFLICT (tenant_id, project_id, object_kind, object_id, energy_type, target_year, revision) DO UPDATE
SET effective = EXCLUDED.effective, unit = EXCLUDED.unit, annual_milli = EXCLUDED.annual_milli,
    method = EXCLUDED.method, weight_ppm = EXCLUDED.weight_ppm`
	if _, err := tx.Exec(ctx, upsertTargetSQL,
		s.Project.TenantID, s.Project.ProjectID, string(req.ObjectKind), req.ObjectID, string(req.Energy),
		req.Year, req.Revision, effStr, req.Unit, annualMilli, string(req.Method), weights32); err != nil {
		return TargetConfigRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TargetConfigRecord{}, err
	}
	tEnd := effTo.UTC()
	return TargetConfigRecord{
		ObjectKind: req.ObjectKind, ObjectID: req.ObjectID, Energy: req.Energy, Year: req.Year, Revision: req.Revision,
		EffectiveFrom: effFrom.UTC(), EffectiveTo: &tEnd, Unit: req.Unit, AnnualMilli: annualMilli,
		Method: req.Method, WeightPPM: req.WeightPPM, SourceSystem: "energy-admin",
	}, nil
}

// PublishAnomalyRule stores a versioned anomaly detection rule for F10/F17 (P4).
func (s *Service) PublishAnomalyRule(ctx context.Context, req PublishAnomalyRuleRequest) (AnomalyRuleConfigRecord, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return AnomalyRuleConfigRecord{}, err
	}
	if !st.governanceReady {
		return AnomalyRuleConfigRecord{}, ErrEnergySchemaNotReady
	}
	if req.ObjectKind == "" {
		req.ObjectKind = buildingenergy.Building
	}
	req.ObjectID = strings.TrimSpace(req.ObjectID)
	if req.ObjectID == "" || req.Energy == "" || req.MinimumHistory < 2 || req.MinimumAbsoluteIncrease <= 0 || req.MinimumRelativeIncrease <= 0 {
		return AnomalyRuleConfigRecord{}, fmt.Errorf("%w: invalid anomaly rule parameters", ErrBadRequest)
	}
	if strings.TrimSpace(req.Revision) == "" {
		req.Revision = fmt.Sprintf("anom-%s-%d", req.ObjectID, s.now().UTC().UnixNano()/1e6)
	}
	effFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if req.EffectiveFrom != nil && !req.EffectiveFrom.IsZero() {
		effFrom = req.EffectiveFrom.UTC()
	}
	effStr, err := pgadapter.FormatTstzrange(rangeFromBounds(effFrom, req.EffectiveTo))
	if err != nil {
		return AnomalyRuleConfigRecord{}, err
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return AnomalyRuleConfigRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upsertAnomalyRuleSQL = `
INSERT INTO energy_domain.anomaly_rule_revision
  (tenant_id, project_id, object_kind, object_id, energy_type, revision, effective, minimum_history, minimum_absolute_increase, minimum_relative_increase, source_system, source_id)
VALUES ($1, $2, $3, $4, $5, $6, $7::tstzrange, $8, $9, $10, 'energy-admin', $6)
ON CONFLICT (tenant_id, project_id, object_kind, object_id, energy_type, revision) DO UPDATE
SET effective = EXCLUDED.effective, minimum_history = EXCLUDED.minimum_history,
    minimum_absolute_increase = EXCLUDED.minimum_absolute_increase, minimum_relative_increase = EXCLUDED.minimum_relative_increase`
	if _, err := tx.Exec(ctx, upsertAnomalyRuleSQL,
		s.Project.TenantID, s.Project.ProjectID, string(req.ObjectKind), req.ObjectID, string(req.Energy),
		req.Revision, effStr, req.MinimumHistory, req.MinimumAbsoluteIncrease, req.MinimumRelativeIncrease); err != nil {
		return AnomalyRuleConfigRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AnomalyRuleConfigRecord{}, err
	}
	return AnomalyRuleConfigRecord{
		ObjectKind: req.ObjectKind, ObjectID: req.ObjectID, Energy: req.Energy, Revision: req.Revision,
		EffectiveFrom: effFrom, EffectiveTo: req.EffectiveTo, MinimumHistory: req.MinimumHistory,
		MinimumAbsoluteIncrease: req.MinimumAbsoluteIncrease, MinimumRelativeIncrease: req.MinimumRelativeIncrease,
		SourceSystem: "energy-admin",
	}, nil
}

// PublishDenominator stores a versioned business denominator for F11/F12/F13/F22/F25 (P4).
func (s *Service) PublishDenominator(ctx context.Context, req PublishDenominatorRequest) (DenominatorConfigRecord, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return DenominatorConfigRecord{}, err
	}
	if !st.governanceReady {
		return DenominatorConfigRecord{}, ErrEnergySchemaNotReady
	}
	if req.ObjectKind == "" {
		req.ObjectKind = buildingenergy.Building
	}
	req.ObjectID = strings.TrimSpace(req.ObjectID)
	req.Unit = strings.TrimSpace(req.Unit)
	if req.ObjectID == "" || req.DenominatorKind == "" || req.Unit == "" || req.Value <= 0 || req.PeriodFrom.IsZero() || !req.PeriodFrom.Before(req.PeriodTo) {
		return DenominatorConfigRecord{}, fmt.Errorf("%w: invalid denominator parameters", ErrBadRequest)
	}
	if strings.TrimSpace(req.Revision) == "" {
		req.Revision = fmt.Sprintf("denom-%s-%s-%d", req.ObjectID, req.DenominatorKind, s.now().UTC().UnixNano()/1e6)
	}
	if strings.TrimSpace(req.Basis) == "" {
		req.Basis = "manual_archive"
	}
	effFrom := req.PeriodFrom.UTC()
	if req.EffectiveFrom != nil && !req.EffectiveFrom.IsZero() {
		effFrom = req.EffectiveFrom.UTC()
	}
	pStr, err := pgadapter.FormatTstzrange(buildingenergy.Range{From: req.PeriodFrom.UTC(), To: req.PeriodTo.UTC()})
	if err != nil {
		return DenominatorConfigRecord{}, err
	}
	effStr, err := pgadapter.FormatTstzrange(rangeFromBounds(effFrom, req.EffectiveTo))
	if err != nil {
		return DenominatorConfigRecord{}, err
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return DenominatorConfigRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upsertDenomSQL = `
INSERT INTO energy_domain.business_denominator_revision
  (tenant_id, project_id, object_kind, object_id, denominator_kind, revision, period_range, effective, value, unit, basis, source_system, source_id)
VALUES ($1, $2, $3, $4, $5, $6, $7::tstzrange, $8::tstzrange, $9, $10, $11, 'energy-admin', $6)
ON CONFLICT (tenant_id, project_id, object_kind, object_id, denominator_kind, revision) DO UPDATE
SET period_range = EXCLUDED.period_range, effective = EXCLUDED.effective, value = EXCLUDED.value, unit = EXCLUDED.unit, basis = EXCLUDED.basis`
	if _, err := tx.Exec(ctx, upsertDenomSQL,
		s.Project.TenantID, s.Project.ProjectID, string(req.ObjectKind), req.ObjectID, string(req.DenominatorKind),
		req.Revision, pStr, effStr, req.Value, req.Unit, req.Basis); err != nil {
		return DenominatorConfigRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DenominatorConfigRecord{}, err
	}
	return DenominatorConfigRecord{
		ObjectKind: req.ObjectKind, ObjectID: req.ObjectID, DenominatorKind: req.DenominatorKind, Revision: req.Revision,
		PeriodFrom: req.PeriodFrom.UTC(), PeriodTo: req.PeriodTo.UTC(), EffectiveFrom: effFrom, EffectiveTo: req.EffectiveTo,
		Value: req.Value, Unit: req.Unit, Basis: req.Basis, SourceSystem: "energy-admin",
	}, nil
}

// PublishBenchmark stores a versioned quota/benchmark rule for F13/F22/F25 (P4).
func (s *Service) PublishBenchmark(ctx context.Context, req PublishBenchmarkRequest) (BenchmarkConfigRecord, error) {
	st, err := s.requireManageAuth(ctx)
	if err != nil {
		return BenchmarkConfigRecord{}, err
	}
	if !st.governanceReady {
		return BenchmarkConfigRecord{}, ErrEnergySchemaNotReady
	}
	req.BenchmarkID = strings.TrimSpace(req.BenchmarkID)
	req.Name = strings.TrimSpace(req.Name)
	req.Publisher = strings.TrimSpace(req.Publisher)
	req.Region = strings.TrimSpace(req.Region)
	req.MetricID = strings.TrimSpace(req.MetricID)
	req.TemplateVersion = strings.TrimSpace(req.TemplateVersion)
	req.Unit = strings.TrimSpace(req.Unit)
	if req.ObjectKind == "" {
		req.ObjectKind = buildingenergy.Building
	}
	if req.PeriodGranularity == "" {
		req.PeriodGranularity = buildingenergy.Day
	}
	if req.BenchmarkID == "" || req.Name == "" || req.Publisher == "" || req.Region == "" ||
		req.MetricID == "" || req.TemplateVersion == "" || req.Unit == "" ||
		(req.Kind != buildingenergy.BenchmarkDemo && req.Kind != buildingenergy.BenchmarkExternal) ||
		req.MaximumValue < 0 {
		return BenchmarkConfigRecord{}, fmt.Errorf("%w: invalid benchmark parameters", ErrBadRequest)
	}
	if strings.TrimSpace(req.Revision) == "" {
		req.Revision = fmt.Sprintf("%s-rev-%d", req.BenchmarkID, s.now().UTC().UnixNano()/1e6)
	}
	effFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if req.EffectiveFrom != nil && !req.EffectiveFrom.IsZero() {
		effFrom = req.EffectiveFrom.UTC()
	}
	effStr, err := pgadapter.FormatTstzrange(rangeFromBounds(effFrom, req.EffectiveTo))
	if err != nil {
		return BenchmarkConfigRecord{}, err
	}
	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return BenchmarkConfigRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upsertBenchmarkSQL = `
INSERT INTO energy_domain.benchmark_rule_revision
  (tenant_id, project_id, benchmark_id, revision, effective, name, publisher, region, metric_id, template_version, unit, kind,
   building_type, object_kind, period_granularity, maximum_value, minimum_denominator, source_system, source_id)
VALUES ($1, $2, $3, $4, $5::tstzrange, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, 'energy-admin', $3)
ON CONFLICT (tenant_id, project_id, benchmark_id, revision) DO UPDATE
SET effective = EXCLUDED.effective, name = EXCLUDED.name, publisher = EXCLUDED.publisher, region = EXCLUDED.region,
    metric_id = EXCLUDED.metric_id, template_version = EXCLUDED.template_version, unit = EXCLUDED.unit,
    kind = EXCLUDED.kind, building_type = EXCLUDED.building_type, object_kind = EXCLUDED.object_kind,
    period_granularity = EXCLUDED.period_granularity, maximum_value = EXCLUDED.maximum_value,
    minimum_denominator = EXCLUDED.minimum_denominator`
	if _, err := tx.Exec(ctx, upsertBenchmarkSQL,
		s.Project.TenantID, s.Project.ProjectID, req.BenchmarkID, req.Revision, effStr,
		req.Name, req.Publisher, req.Region, req.MetricID, req.TemplateVersion, req.Unit, string(req.Kind),
		string(req.BuildingType), string(req.ObjectKind), string(req.PeriodGranularity), req.MaximumValue, req.MinimumDenominator); err != nil {
		return BenchmarkConfigRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BenchmarkConfigRecord{}, err
	}
	return BenchmarkConfigRecord{
		BenchmarkID: req.BenchmarkID, Revision: req.Revision, EffectiveFrom: effFrom, EffectiveTo: req.EffectiveTo,
		Name: req.Name, Publisher: req.Publisher, Region: req.Region, MetricID: req.MetricID,
		TemplateVersion: req.TemplateVersion, Unit: req.Unit, Kind: req.Kind, BuildingType: req.BuildingType,
		ObjectKind: req.ObjectKind, PeriodGranularity: req.PeriodGranularity, MaximumValue: req.MaximumValue,
		MinimumDenominator: req.MinimumDenominator, SourceSystem: "energy-admin",
	}, nil
}

func (s *Service) requireManageAuth(ctx context.Context) (*liveState, error) {
	if s.Guard == nil {
		return nil, buildingenergy.ErrAccessDenied
	}
	st, err := s.loadState(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if _, err := s.Guard.AuthorizeScope(ctx, st.catalog, buildingenergy.ManageBindings, buildingenergy.Scope{
		Project: s.Project,
		Period:  buildingenergy.Range{From: now.Add(-time.Hour), To: now},
	}); err != nil {
		return nil, err
	}
	return st, nil
}
