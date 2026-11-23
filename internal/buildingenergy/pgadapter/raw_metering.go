package pgadapter

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

// PointReadBinding specifies the explicit mapping view, mapping generation, and
// requested objects used to verify BOTH caller authorization AND historical
// project/terminal/point ownership via buildingenergy.ReadAuthorizedMapping
// before any SQL query reads raw_item.
type PointReadBinding struct {
	View       buildingenergy.ObjectKind
	Generation uint64
	Objects    []buildingenergy.ObjectKey
}

type pointReadBindingKey struct{}

// WithPointReadBinding attaches a PointReadBinding to ctx for MappingPointAuthorizer.
func WithPointReadBinding(ctx context.Context, binding PointReadBinding) context.Context {
	return context.WithValue(ctx, pointReadBindingKey{}, binding)
}

// PointReadBindingFromContext retrieves the PointReadBinding attached to ctx.
func PointReadBindingFromContext(ctx context.Context) (PointReadBinding, bool) {
	if ctx == nil {
		return PointReadBinding{}, false
	}
	binding, ok := ctx.Value(pointReadBindingKey{}).(PointReadBinding)
	return binding, ok
}

// PointBindingSelector optionally selects the PointReadBinding for a point and period
// when callers do not pass one in context.
type PointBindingSelector interface {
	SelectPointBinding(ctx context.Context, point buildingenergy.PointRef, period buildingenergy.Range) (PointReadBinding, error)
}

// PointReadAuthorizer resolves and verifies both caller authorization and
// historical project/terminal/point ownership before raw_item is queried.
//
// Because raw_item (migrations/0024_raw_ingest.sql) has no tenant_id or project_id
// column, RawMeteringSource MUST never trust point.Project from a caller request
// without an authoritative verified ResolvedMapping covering the full read window.
type PointReadAuthorizer interface {
	AuthorizePointRead(ctx context.Context, point buildingenergy.PointRef, period buildingenergy.Range) (buildingenergy.ResolvedMapping, error)
}

// MappingPointAuthorizer is the canonical PointReadAuthorizer backed by
// buildingenergy.ReadAuthorizedMapping: it authenticates the caller, checks
// ReadUsage authorization on project/objects/point/period, verifies authoritative
// catalog coverage, loads the immutable M1-B mapping generation, and verifies that
// every active allocation target in the window is authorized and owned by point.Project.
type MappingPointAuthorizer struct {
	Identity buildingenergy.IdentitySource
	Policy   buildingenergy.AccessPolicy
	Catalog  buildingenergy.Catalog
	Mappings buildingenergy.MappingReader
	Selector PointBindingSelector
}

// AuthorizePointRead implements PointReadAuthorizer.
func (a *MappingPointAuthorizer) AuthorizePointRead(ctx context.Context, point buildingenergy.PointRef, period buildingenergy.Range) (buildingenergy.ResolvedMapping, error) {
	if a == nil || isNilInterface(a.Identity) || isNilInterface(a.Policy) {
		return buildingenergy.ResolvedMapping{}, buildingenergy.ErrAccessDenied
	}
	if isNilInterface(a.Catalog) {
		return buildingenergy.ResolvedMapping{}, buildingenergy.ErrCatalogScope
	}
	if isNilInterface(a.Mappings) {
		return buildingenergy.ResolvedMapping{}, buildingenergy.ErrMappingUnavailable
	}
	var (
		binding PointReadBinding
		err     error
	)
	if !isNilInterface(a.Selector) {
		binding, err = a.Selector.SelectPointBinding(ctx, point, period)
		if err != nil {
			return buildingenergy.ResolvedMapping{}, err
		}
	} else {
		var ok bool
		binding, ok = PointReadBindingFromContext(ctx)
		if !ok {
			return buildingenergy.ResolvedMapping{}, buildingenergy.ErrAccessDenied
		}
	}
	if binding.Generation == 0 || len(binding.Objects) == 0 {
		return buildingenergy.ResolvedMapping{}, buildingenergy.ErrAccessDenied
	}

	resolved, err := buildingenergy.ReadAuthorizedMapping(
		ctx,
		a.Identity,
		a.Policy,
		a.Catalog,
		a.Mappings,
		buildingenergy.MappingQuery{
			Point:      point,
			View:       binding.View,
			Period:     period,
			Generation: binding.Generation,
			Objects:    binding.Objects,
		},
	)
	if err != nil {
		return buildingenergy.ResolvedMapping{}, err
	}
	if err := verifyResolvedOwnership(resolved, point, period); err != nil {
		return buildingenergy.ResolvedMapping{}, err
	}
	return resolved, nil
}

// RegisterEventResolver optionally supplies confirmed register_epoch and rollover
// flags for cumulative readings (which raw_item does not currently record).
type RegisterEventResolver interface {
	ResolveRegisterState(ctx context.Context, point buildingenergy.PointRef, valueTime, receivedAt time.Time, fallbackEpoch string) (epoch string, rollover bool, err error)
}

// RawMeteringSource implements buildingenergy.MeteringSource on top of the
// existing iot-admin raw_item table (migrations/0024_raw_ingest.sql).
//
// Every Load call fails closed unless Authorizer proves BOTH caller ReadUsage
// authorization AND historical project/terminal/point/object ownership over the
// entire requested period before executing any SQL query against raw_item.
type RawMeteringSource struct {
	DB         Querier
	Authorizer PointReadAuthorizer
	Events     RegisterEventResolver
	// IntervalTimeIsEnd indicates whether upstream raw_item.value_time marks the
	// right end of an interval quantity rather than its start. Default false (start).
	IntervalTimeIsEnd bool
}

const (
	selectCumulativeRawItemsSQL = `
SELECT id, received_at, terminal_id, item_code, value_num, coalesce(unit, ''), value_time, quality, status
FROM raw_item
WHERE terminal_id = $1 AND item_code = $2
  AND received_at <= $3
  AND value_time IS NOT NULL
  AND value_time >= $4 AND value_time <= $5
ORDER BY value_time ASC, received_at ASC, id ASC`

	selectIntervalRawItemsSQL = `
SELECT id, received_at, terminal_id, item_code, value_num, coalesce(unit, ''), value_time, quality, status
FROM raw_item
WHERE terminal_id = $1 AND item_code = $2
  AND received_at <= $3
  AND value_time IS NOT NULL
  AND value_time >= $4 AND value_time < $5
ORDER BY value_time ASC, received_at ASC, id ASC`

	selectIntervalEndRawItemsSQL = `
SELECT id, received_at, terminal_id, item_code, value_num, coalesce(unit, ''), value_time, quality, status
FROM raw_item
WHERE terminal_id = $1 AND item_code = $2
  AND received_at <= $3
  AND value_time IS NOT NULL
  AND value_time > $4 AND value_time <= $5
ORDER BY value_time ASC, received_at ASC, id ASC`
)

// Load implements buildingenergy.MeteringSource.
// It fails closed unless Authorizer verifies identity, authorization, catalog scope,
// and historical project/terminal/point ownership across period, then strictly enforces
// received_at <= asOf cutoff, terminal/item identity match, ownership window bounds,
// composite (id, received_at) lineage, unit match, and conservative quality mapping.
func (s *RawMeteringSource) Load(ctx context.Context, point buildingenergy.PointRef, period buildingenergy.Range, asOf time.Time) (buildingenergy.MeteringData, error) {
	if s == nil || isNilInterface(s.Authorizer) {
		return buildingenergy.MeteringData{}, buildingenergy.ErrAccessDenied
	}
	if isNilInterface(s.DB) {
		return buildingenergy.MeteringData{}, buildingenergy.ErrMeteringScope
	}
	if strings.TrimSpace(point.Project.TenantID) == "" || strings.TrimSpace(point.Project.ProjectID) == "" ||
		strings.TrimSpace(point.Key.TerminalID) == "" || strings.TrimSpace(point.Key.ItemCode) == "" ||
		period.From.IsZero() || period.To.IsZero() || !period.From.Before(period.To) || asOf.IsZero() {
		return buildingenergy.MeteringData{}, buildingenergy.ErrInvalidScope
	}

	resolved, err := s.Authorizer.AuthorizePointRead(ctx, point, period)
	if err != nil {
		return buildingenergy.MeteringData{}, err
	}
	if err := verifyResolvedOwnership(resolved, point, period); err != nil {
		return buildingenergy.MeteringData{}, err
	}

	revision := resolved.Profile
	profile := revision.Profile
	if profile.Kind == buildingenergy.Instantaneous {
		// Return profile without reading raw_item so CalculateFromSource rejects with ErrUnsupportedMeasurement.
		return buildingenergy.MeteringData{Profile: profile}, nil
	}

	query := selectCumulativeRawItemsSQL
	queryFrom, queryTo := period.From.UTC(), period.To.UTC()
	if profile.Kind == buildingenergy.Interval {
		query = selectIntervalRawItemsSQL
		if s.IntervalTimeIsEnd {
			query = selectIntervalEndRawItemsSQL
		}
	}

	rows, err := s.DB.Query(ctx, query, point.Key.TerminalID, point.Key.ItemCode, asOf.UTC(), queryFrom, queryTo)
	if err != nil {
		return buildingenergy.MeteringData{}, err
	}
	defer rows.Close()

	var samples []buildingenergy.Sample
	for rows.Next() {
		var (
			id           int64
			receivedAt   time.Time
			rowTerminal  string
			rowItemCode  string
			valueNum     *float64
			unit         string
			valueTime    time.Time
			quality      int16
			status       string
		)
		if err := rows.Scan(&id, &receivedAt, &rowTerminal, &rowItemCode, &valueNum, &unit, &valueTime, &quality, &status); err != nil {
			return buildingenergy.MeteringData{}, err
		}
		if rowTerminal != point.Key.TerminalID || rowItemCode != point.Key.ItemCode {
			return buildingenergy.MeteringData{}, buildingenergy.ErrMeteringScope
		}
		if status == "ignored" {
			// Disabled terminal messages are ignored by ingest and do not represent a meter reading.
			continue
		}
		if receivedAt.UTC().After(asOf.UTC()) {
			// Defense-in-depth visibility cutoff even if a driver/query double leaks future receipts.
			continue
		}
		sample := buildingenergy.Sample{
			At:         valueTime.UTC(),
			ReceivedAt: receivedAt.UTC(),
			Quality:    mapRawQuality(status, valueNum, quality, unit, profile.Unit),
			Source: buildingenergy.SourceRef{
				System: "iot-raw-item",
				ID:     fmt.Sprintf("%d@%s", id, receivedAt.UTC().Format(time.RFC3339Nano)),
			},
		}
		if valueNum != nil {
			sample.Value = *valueNum
		}
		if profile.Kind == buildingenergy.Interval {
			if s.IntervalTimeIsEnd {
				sample.End = sample.At
				sample.At = sample.End.Add(-profile.Cadence)
			} else {
				sample.End = sample.At.Add(profile.Cadence)
			}
			// Half-open interval semantics: [sample.At, sample.End) must fit completely
			// inside period, profile effective range, and binding effective range.
			// An interval starting at effective.To belongs strictly to the next revision.
			if !intervalWithinBoundary(sample.At, sample.End, period, profile.Meta.Effective, resolved.BindingVersion.Effective) {
				continue
			}
		} else {
			// Cumulative half-open [period.From, period.To) difference semantics (M1-A):
			// the final atomic interval [period.To-cadence, period.To) requires the exact
			// closing register reading at period.To (including when period.To == effective.To
			// as the shared handoff boundary reading between adjacent revisions).
			// Readings before From or strictly after period.To / effective.To are excluded.
			// If a meter swap occurs at effective.To, RegisterEventResolver or conflicting
			// readings surface Suspect (EpochChange / ConflictingSample) inside M1-A.
			if !instantWithinCumulativeBoundary(sample.At, period, profile.Meta.Effective, resolved.BindingVersion.Effective) {
				continue
			}
			epoch := revision.RegisterEpoch
			rollover := false
			if !isNilInterface(s.Events) {
				var evErr error
				epoch, rollover, evErr = s.Events.ResolveRegisterState(ctx, point, sample.At, sample.ReceivedAt, revision.RegisterEpoch)
				if evErr != nil {
					return buildingenergy.MeteringData{}, evErr
				}
			}
			sample.Epoch = epoch
			sample.Rollover = rollover
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return buildingenergy.MeteringData{}, err
	}
	return buildingenergy.MeteringData{
		Profile: profile,
		Samples: samples,
	}, nil
}

// verifyResolvedOwnership ensures the resolved mapping proves authoritative
// project/terminal/point ownership and non-empty object allocation across the
// entire query period before raw_item rows may be read.
func verifyResolvedOwnership(resolved buildingenergy.ResolvedMapping, point buildingenergy.PointRef, period buildingenergy.Range) error {
	if resolved.Generation == 0 || resolved.Profile.Profile.Point != point {
		return buildingenergy.ErrMeteringScope
	}
	if strings.TrimSpace(resolved.Profile.Channel.System) == "" || strings.TrimSpace(resolved.Profile.Channel.ID) == "" {
		return buildingenergy.ErrMeteringScope
	}
	if strings.TrimSpace(resolved.Profile.Profile.Meta.Version) == "" ||
		!effectiveCovers(resolved.Profile.Profile.Meta.Effective, period) {
		return buildingenergy.ErrMeteringScope
	}
	if strings.TrimSpace(resolved.BindingVersion.Version) == "" ||
		len(resolved.Allocations) == 0 ||
		!effectiveCovers(resolved.BindingVersion.Effective, period) {
		return buildingenergy.ErrMeteringScope
	}
	for _, alloc := range resolved.Allocations {
		if alloc.Target.Kind != resolved.View || strings.TrimSpace(alloc.Target.ID) == "" ||
			strings.TrimSpace(alloc.TargetRevision) == "" || alloc.WeightPPM <= 0 {
			return buildingenergy.ErrMeteringScope
		}
	}
	return nil
}

func effectiveCovers(effective, period buildingenergy.Range) bool {
	if period.To.IsZero() {
		return false
	}
	return rangeCovers(effective, period)
}

func instantWithinCumulativeBoundary(at time.Time, period, profileEff, bindingEff buildingenergy.Range) bool {
	if at.Before(period.From) || at.After(period.To) {
		return false
	}
	for _, eff := range []buildingenergy.Range{profileEff, bindingEff} {
		if at.Before(eff.From) {
			return false
		}
		if !eff.To.IsZero() && at.After(eff.To) {
			return false
		}
	}
	return true
}

func intervalWithinBoundary(start, end time.Time, period, profileEff, bindingEff buildingenergy.Range) bool {
	if !start.Before(end) {
		return false
	}
	slot := buildingenergy.Range{From: start, To: end}
	return effectiveCovers(period, slot) && effectiveCovers(profileEff, slot) && effectiveCovers(bindingEff, slot)
}

// mapRawQuality maps raw_item status, value_num, quality bitmask (0=ok, 1=range,
// 2=rule, 4=unit), and unit to buildingenergy.Quality conservatively.
// Never masks dirty, dropped, null, non-finite, or mismatched-unit rows as Normal.
func mapRawQuality(status string, valueNum *float64, rawQuality int16, rawUnit, profileUnit string) buildingenergy.Quality {
	if status != "written" || valueNum == nil || math.IsNaN(*valueNum) || math.IsInf(*valueNum, 0) {
		return buildingenergy.Suspect
	}
	if rawQuality != 0 {
		return buildingenergy.Suspect
	}
	trimmedRaw := strings.TrimSpace(rawUnit)
	if trimmedRaw != "" && trimmedRaw != strings.TrimSpace(profileUnit) {
		return buildingenergy.Suspect
	}
	return buildingenergy.Normal
}
