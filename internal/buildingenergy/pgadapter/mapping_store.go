package pgadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

// MappingStore implements buildingenergy.MappingReader and buildingenergy.MappingWriter
// against the candidate 5-table schema in docs/energy-analysis/review/m1-b-mapping.sql.
type MappingStore struct {
	DB Transactor
	// Catalog is the authoritative platform directory used during CompareAndSwapMapping
	// to verify every allocation target belongs to the project and that each
	// Allocation.TargetRevision matches the real platform_directory.object_revision.revision.
	Catalog buildingenergy.Catalog
	// LockOnlyCheck adds explicit pre-insert overlap/weight checks for the
	// DBA lock-only variant where GiST EXCLUDE constraints are removed.
	LockOnlyCheck bool
}

// NewMappingStore creates a PostgreSQL mapping repository adapter backed by an
// authoritative directory catalog for target_revision verification on write.
func NewMappingStore(db Transactor, catalog buildingenergy.Catalog) *MappingStore {
	return &MappingStore{DB: db, Catalog: catalog}
}

const (
	selectGenerationExistsSQL = `
SELECT generation
FROM energy_mapping.energy_mapping_generation
WHERE tenant_id = $1 AND project_id = $2 AND terminal_id = $3 AND item_code = $4 AND generation = $5`

	selectProfilesByGenSQL = `
SELECT revision, lower(effective), upper(effective),
       source_system, source_id, channel_system, channel_id,
       register_epoch, measurement_kind, energy_type, unit,
       multiplier::double precision, register_modulus::double precision, cadence_seconds
FROM energy_mapping.energy_point_profile
WHERE tenant_id = $1 AND project_id = $2 AND terminal_id = $3 AND item_code = $4 AND generation = $5
ORDER BY lower(effective) ASC, revision ASC`

	selectBindingsByGenSQL = `
SELECT view_kind, revision, lower(effective), upper(effective),
       source_system, source_id
FROM energy_mapping.energy_point_binding
WHERE tenant_id = $1 AND project_id = $2 AND terminal_id = $3 AND item_code = $4 AND generation = $5
ORDER BY view_kind ASC, lower(effective) ASC, revision ASC`

	selectAllocationsByGenSQL = `
SELECT view_kind, binding_revision, target_id, target_revision, weight_ppm
FROM energy_mapping.energy_point_binding_allocation
WHERE tenant_id = $1 AND project_id = $2 AND terminal_id = $3 AND item_code = $4 AND generation = $5
ORDER BY view_kind ASC, binding_revision ASC, target_id ASC`

	initHeadSQL = `
INSERT INTO energy_mapping.energy_mapping_head (tenant_id, project_id, terminal_id, item_code)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING`

	lockHeadSQL = `
SELECT current_generation
FROM energy_mapping.energy_mapping_head
WHERE tenant_id = $1 AND project_id = $2 AND terminal_id = $3 AND item_code = $4
FOR UPDATE`

	insertGenerationSQL = `
INSERT INTO energy_mapping.energy_mapping_generation
  (tenant_id, project_id, terminal_id, item_code, generation, previous_generation,
   published_by_issuer, published_by_sub, approved_at, approval_system, approval_id, reason)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

	insertProfileSQL = `
INSERT INTO energy_mapping.energy_point_profile
  (tenant_id, project_id, terminal_id, item_code, generation, revision, effective,
   source_system, source_id, channel_system, channel_id, register_epoch,
   measurement_kind, energy_type, unit, multiplier, register_modulus, cadence_seconds)
VALUES ($1, $2, $3, $4, $5, $6, $7::tstzrange, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`

	insertBindingSQL = `
INSERT INTO energy_mapping.energy_point_binding
  (tenant_id, project_id, terminal_id, item_code, generation, view_kind, view_key,
   revision, effective, source_system, source_id, catalog_snapshot_ref)
VALUES ($1, $2, $3, $4, $5, $6, 'default', $7, $8::tstzrange, $9, $10, $11)`

	insertAllocationSQL = `
INSERT INTO energy_mapping.energy_point_binding_allocation
  (tenant_id, project_id, terminal_id, item_code, generation, view_kind, view_key,
   binding_revision, target_id, target_revision, weight_ppm)
VALUES ($1, $2, $3, $4, $5, $6, 'default', $7, $8, $9, $10)`

	casUpdateHeadSQL = `
UPDATE energy_mapping.energy_mapping_head
SET current_generation = $5
WHERE tenant_id = $1 AND project_id = $2 AND terminal_id = $3 AND item_code = $4
  AND current_generation IS NOT DISTINCT FROM $6`

	checkProfileOverlapSQL = `
SELECT EXISTS (
  SELECT 1
  FROM energy_mapping.energy_point_profile a
  JOIN energy_mapping.energy_point_profile b
    ON a.tenant_id = b.tenant_id AND a.project_id = b.project_id
   AND a.terminal_id = b.terminal_id AND a.item_code = b.item_code
   AND a.generation = b.generation AND a.revision < b.revision
   AND a.effective && b.effective
  WHERE a.tenant_id = $1 AND a.project_id = $2 AND a.terminal_id = $3
    AND a.item_code = $4 AND a.generation = $5
)`

	checkBindingOverlapSQL = `
SELECT EXISTS (
  SELECT 1
  FROM energy_mapping.energy_point_binding a
  JOIN energy_mapping.energy_point_binding b
    ON a.tenant_id = b.tenant_id AND a.project_id = b.project_id
   AND a.terminal_id = b.terminal_id AND a.item_code = b.item_code
   AND a.generation = b.generation AND a.view_kind = b.view_kind
   AND a.view_key = b.view_key AND a.revision < b.revision
   AND a.effective && b.effective
  WHERE a.tenant_id = $1 AND a.project_id = $2 AND a.terminal_id = $3
    AND a.item_code = $4 AND a.generation = $5
)`

	checkOverallocationSQL = `
SELECT EXISTS (
  SELECT 1
  FROM energy_mapping.energy_point_binding b
  LEFT JOIN energy_mapping.energy_point_binding_allocation a
    ON a.tenant_id = b.tenant_id AND a.project_id = b.project_id
   AND a.terminal_id = b.terminal_id AND a.item_code = b.item_code
   AND a.generation = b.generation AND a.view_kind = b.view_kind
   AND a.view_key = b.view_key AND a.binding_revision = b.revision
  WHERE b.tenant_id = $1 AND b.project_id = $2 AND b.terminal_id = $3
    AND b.item_code = $4 AND b.generation = $5
  GROUP BY b.view_kind, b.view_key, b.revision
  HAVING COALESCE(SUM(a.weight_ppm), 0) > 1000000
)`
)

// ReadMapping implements buildingenergy.MappingReader.
func (s *MappingStore) ReadMapping(ctx context.Context, point buildingenergy.PointRef, generation uint64) (buildingenergy.MappingTimeline, error) {
	if s == nil || isNilInterface(s.DB) {
		return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
	}
	genInt, err := ValidatePositiveGeneration(generation)
	if err != nil {
		return buildingenergy.MappingTimeline{}, buildingenergy.ErrInvalidMapping
	}
	var foundGen int64
	err = s.DB.QueryRow(ctx, selectGenerationExistsSQL,
		point.Project.TenantID, point.Project.ProjectID, point.Key.TerminalID, point.Key.ItemCode, genInt).Scan(&foundGen)
	if errors.Is(err, pgx.ErrNoRows) {
		return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
	}
	if err != nil {
		return buildingenergy.MappingTimeline{}, err
	}

	timeline := buildingenergy.MappingTimeline{Point: point, Generation: generation}

	pRows, err := s.DB.Query(ctx, selectProfilesByGenSQL,
		point.Project.TenantID, point.Project.ProjectID, point.Key.TerminalID, point.Key.ItemCode, genInt)
	if err != nil {
		return buildingenergy.MappingTimeline{}, err
	}
	defer pRows.Close()
	for pRows.Next() {
		var (
			revision, srcSys, srcID, chSys, chID, kind, energy, unit string
			effFrom                                                  time.Time
			effTo                                                    *time.Time
			epoch                                                    *string
			multiplier, modulus                                      *float64
			cadenceSeconds                                           *int
		)
		if err := pRows.Scan(&revision, &effFrom, &effTo, &srcSys, &srcID, &chSys, &chID,
			&epoch, &kind, &energy, &unit, &multiplier, &modulus, &cadenceSeconds); err != nil {
			return buildingenergy.MappingTimeline{}, err
		}
		eff, err := RangeFromBounds(effFrom, effTo)
		if err != nil {
			return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
		}
		prof := buildingenergy.ProfileRevision{
			Profile: buildingenergy.PointProfile{
				Point:  point,
				Kind:   buildingenergy.MeasurementKind(kind),
				Energy: buildingenergy.EnergyType(energy),
				Unit:   unit,
				Meta: buildingenergy.VersionMeta{
					Version:   revision,
					Effective: eff,
					Source:    buildingenergy.SourceRef{System: srcSys, ID: srcID},
				},
			},
			Channel: buildingenergy.SourceRef{System: chSys, ID: chID},
		}
		if epoch != nil {
			prof.RegisterEpoch = *epoch
		}
		if multiplier != nil {
			prof.Profile.Multiplier = *multiplier
		}
		if modulus != nil {
			prof.Profile.Modulus = *modulus
		}
		if cadenceSeconds != nil {
			prof.Profile.Cadence = time.Duration(*cadenceSeconds) * time.Second
		}
		timeline.Profiles = append(timeline.Profiles, prof)
	}
	if err := pRows.Err(); err != nil {
		return buildingenergy.MappingTimeline{}, err
	}

	bRows, err := s.DB.Query(ctx, selectBindingsByGenSQL,
		point.Project.TenantID, point.Project.ProjectID, point.Key.TerminalID, point.Key.ItemCode, genInt)
	if err != nil {
		return buildingenergy.MappingTimeline{}, err
	}
	defer bRows.Close()
	type bindingKey struct {
		view     buildingenergy.ObjectKind
		revision string
	}
	bindingIndex := make(map[bindingKey]int)
	for bRows.Next() {
		var (
			view, revision, srcSys, srcID string
			effFrom                       time.Time
			effTo                         *time.Time
		)
		if err := bRows.Scan(&view, &revision, &effFrom, &effTo, &srcSys, &srcID); err != nil {
			return buildingenergy.MappingTimeline{}, err
		}
		eff, err := RangeFromBounds(effFrom, effTo)
		if err != nil {
			return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
		}
		bk := bindingKey{view: buildingenergy.ObjectKind(view), revision: revision}
		bindingIndex[bk] = len(timeline.Bindings)
		timeline.Bindings = append(timeline.Bindings, buildingenergy.BindingRevision{
			View: bk.view,
			Meta: buildingenergy.VersionMeta{
				Version:   revision,
				Effective: eff,
				Source:    buildingenergy.SourceRef{System: srcSys, ID: srcID},
			},
		})
	}
	if err := bRows.Err(); err != nil {
		return buildingenergy.MappingTimeline{}, err
	}

	aRows, err := s.DB.Query(ctx, selectAllocationsByGenSQL,
		point.Project.TenantID, point.Project.ProjectID, point.Key.TerminalID, point.Key.ItemCode, genInt)
	if err != nil {
		return buildingenergy.MappingTimeline{}, err
	}
	defer aRows.Close()
	for aRows.Next() {
		var (
			view, bindingRev, targetID, targetRev string
			weightPPM                             int
		)
		if err := aRows.Scan(&view, &bindingRev, &targetID, &targetRev, &weightPPM); err != nil {
			return buildingenergy.MappingTimeline{}, err
		}
		if strings.TrimSpace(targetRev) == "" {
			return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
		}
		bk := bindingKey{view: buildingenergy.ObjectKind(view), revision: bindingRev}
		idx, ok := bindingIndex[bk]
		if !ok {
			return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
		}
		timeline.Bindings[idx].Allocations = append(timeline.Bindings[idx].Allocations, buildingenergy.Allocation{
			Target:         buildingenergy.ObjectKey{Kind: bk.view, ID: targetID},
			TargetRevision: targetRev,
			WeightPPM:      weightPPM,
		})
	}
	if err := aRows.Err(); err != nil {
		return buildingenergy.MappingTimeline{}, err
	}

	if err := timeline.Validate(); err != nil {
		return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
	}
	return timeline, nil
}

// CompareAndSwapMapping implements buildingenergy.MappingWriter using the exact
// READ COMMITTED CAS sequence from HANDOFF.md §B:
//   1. Validate timeline, uint64->bigint, microsecond timestamps, finite numeric.
//   2. INSERT head ON CONFLICT DO NOTHING -> SELECT current_generation FOR UPDATE.
//   3. Compare observed generation == expected; conflict rolls back with ErrMappingConflict.
//   4. Insert generation, profiles, bindings, allocations.
//   5. UPDATE head ... WHERE current_generation IS NOT DISTINCT FROM expected; require RowsAffected == 1.
//   6. Commit transaction; rollback on any error.
func (s *MappingStore) CompareAndSwapMapping(ctx context.Context, expected uint64, next buildingenergy.MappingTimeline, audit buildingenergy.MappingAudit) error {
	if s == nil || isNilInterface(s.DB) {
		return buildingenergy.ErrMappingUnavailable
	}
	if next.Generation == 0 || next.Generation-1 != expected {
		return buildingenergy.ErrInvalidMapping
	}
	if err := next.Validate(); err != nil {
		return err
	}
	nextGen, err := ValidatePositiveGeneration(next.Generation)
	if err != nil {
		return err
	}
	expectedPtr, err := ValidateExpectedGeneration(expected)
	if err != nil {
		return err
	}
	if err := ValidateMicrosecondTime(audit.Approval.At); err != nil {
		return err
	}
	// Verify every allocation carries an authoritative TargetRevision matching
	// platform_directory.object_revision.revision and covering the binding window.
	// Never substitute BindingRevision.Meta.Version for TargetRevision.
	for _, br := range next.Bindings {
		if len(br.Allocations) == 0 {
			continue
		}
		if isNilInterface(s.Catalog) {
			return buildingenergy.ErrCatalogScope
		}
		for _, alloc := range br.Allocations {
			if strings.TrimSpace(alloc.TargetRevision) == "" {
				return fmt.Errorf("%w: allocation target %s missing authoritative target_revision", buildingenergy.ErrInvalidMapping, alloc.Target.ID)
			}
			obj, err := s.Catalog.Object(ctx, next.Point.Project, alloc.Target, br.Meta.Effective.From)
			if err != nil {
				return err
			}
			if obj.Project != next.Point.Project || obj.Key != alloc.Target ||
				strings.TrimSpace(obj.Meta.Version) == "" || obj.Meta.Version != alloc.TargetRevision ||
				!rangeCovers(obj.Meta.Effective, br.Meta.Effective) {
				return fmt.Errorf("%w: allocation target %s revision %q does not match authoritative object_revision %q",
					buildingenergy.ErrCatalogScope, alloc.Target.ID, alloc.TargetRevision, obj.Meta.Version)
			}
		}
	}
	// Pre-encode and validate all ranges and numerics before opening a DB transaction.
	profileRanges := make([]string, len(next.Profiles))
	for i, pr := range next.Profiles {
		r, err := FormatTstzrange(pr.Profile.Meta.Effective)
		if err != nil {
			return err
		}
		if pr.Profile.Kind != buildingenergy.Instantaneous {
			if err := ValidateFiniteFloat64(pr.Profile.Multiplier); err != nil {
				return err
			}
			if pr.Profile.Modulus > 0 {
				if err := ValidateFiniteFloat64(pr.Profile.Modulus); err != nil {
					return err
				}
			}
		}
		profileRanges[i] = r
	}
	bindingRanges := make([]string, len(next.Bindings))
	for i, br := range next.Bindings {
		r, err := FormatTstzrange(br.Meta.Effective)
		if err != nil {
			return err
		}
		bindingRanges[i] = r
	}

	tx, err := s.DB.BeginReadCommitted(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	tenant := next.Point.Project.TenantID
	project := next.Point.Project.ProjectID
	terminal := next.Point.Key.TerminalID
	itemCode := next.Point.Key.ItemCode

	if _, err := tx.Exec(ctx, initHeadSQL, tenant, project, terminal, itemCode); err != nil {
		return fmt.Errorf("init mapping head: %w", err)
	}

	var currentGen *int64
	if err := tx.QueryRow(ctx, lockHeadSQL, tenant, project, terminal, itemCode).Scan(&currentGen); err != nil {
		return fmt.Errorf("lock mapping head: %w", err)
	}
	var observed uint64
	if currentGen != nil {
		if *currentGen < 0 {
			return buildingenergy.ErrMappingUnavailable
		}
		observed = uint64(*currentGen)
	}
	if observed != expected {
		return buildingenergy.ErrMappingConflict
	}

	if _, err := tx.Exec(ctx, insertGenerationSQL,
		tenant, project, terminal, itemCode, nextGen, expectedPtr,
		audit.Actor.Issuer, audit.Actor.ID, audit.Approval.At.UTC(),
		audit.Approval.Ref.System, audit.Approval.Ref.ID, audit.Approval.Reason); err != nil {
		return fmt.Errorf("insert mapping generation: %w", err)
	}

	for i, pr := range next.Profiles {
		var epoch *string
		var mult, mod *float64
		var cadenceSec *int
		if pr.Profile.Kind == buildingenergy.Cumulative {
			if pr.RegisterEpoch != "" {
				v := pr.RegisterEpoch
				epoch = &v
			}
			if pr.Profile.Modulus > 0 {
				mm := pr.Profile.Modulus
				mod = &mm
			}
		}
		if pr.Profile.Kind != buildingenergy.Instantaneous {
			m := pr.Profile.Multiplier
			mult = &m
			cs := int(pr.Profile.Cadence / time.Second)
			cadenceSec = &cs
		}
		if _, err := tx.Exec(ctx, insertProfileSQL,
			tenant, project, terminal, itemCode, nextGen, pr.Profile.Meta.Version, profileRanges[i],
			pr.Profile.Meta.Source.System, pr.Profile.Meta.Source.ID,
			pr.Channel.System, pr.Channel.ID, epoch,
			string(pr.Profile.Kind), string(pr.Profile.Energy), pr.Profile.Unit,
			mult, mod, cadenceSec); err != nil {
			return fmt.Errorf("insert profile revision %s: %w", pr.Profile.Meta.Version, err)
		}
	}

	for i, br := range next.Bindings {
		if _, err := tx.Exec(ctx, insertBindingSQL,
			tenant, project, terminal, itemCode, nextGen, string(br.View), br.Meta.Version, bindingRanges[i],
			br.Meta.Source.System, br.Meta.Source.ID, br.Meta.Source.ID); err != nil {
			return fmt.Errorf("insert binding revision %s: %w", br.Meta.Version, err)
		}
		for _, alloc := range br.Allocations {
			if _, err := tx.Exec(ctx, insertAllocationSQL,
				tenant, project, terminal, itemCode, nextGen, string(br.View), br.Meta.Version,
				alloc.Target.ID, alloc.TargetRevision, alloc.WeightPPM); err != nil {
				return fmt.Errorf("insert allocation %s: %w", alloc.Target.ID, err)
			}
		}
	}

	if s.LockOnlyCheck {
		for _, check := range []struct {
			name string
			sql  string
		}{
			{name: "profile overlap", sql: checkProfileOverlapSQL},
			{name: "binding overlap", sql: checkBindingOverlapSQL},
			{name: "overallocation", sql: checkOverallocationSQL},
		} {
			var violated bool
			if err := tx.QueryRow(ctx, check.sql, tenant, project, terminal, itemCode, nextGen).Scan(&violated); err != nil {
				return fmt.Errorf("lock-only %s check: %w", check.name, err)
			}
			if violated {
				return fmt.Errorf("%w: lock-only %s violated", buildingenergy.ErrInvalidMapping, check.name)
			}
		}
	}

	tag, err := tx.Exec(ctx, casUpdateHeadSQL, tenant, project, terminal, itemCode, nextGen, expectedPtr)
	if err != nil {
		return fmt.Errorf("cas update head: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return buildingenergy.ErrMappingConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit mapping generation: %w", err)
	}
	committed = true
	return nil
}
