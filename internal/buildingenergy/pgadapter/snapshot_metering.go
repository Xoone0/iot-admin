package pgadapter

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

// SnapshotCoarseMeteringSource implements buildingenergy.MeteringSource on top of
// the existing iot-admin public.snapshot_coarse table (granularity = 'hour').
//
// Each hourly snapshot row at segment_start = H holds the cumulative register
// reading closing hour [H, H+1h), so it supplies the boundary reading at
// sample.At = H + 1h for M1-A hourly cumulative differencing (Cadence = 1h).
//
// Like RawMeteringSource, every Load call fails closed unless Authorizer proves
// BOTH caller ReadUsage authorization AND verified historical project/terminal/
// point ownership across period BEFORE executing any SQL query.
type SnapshotCoarseMeteringSource struct {
	DB         Querier
	Authorizer PointReadAuthorizer
	Events     RegisterEventResolver
}

const selectHourlySnapshotCoarseSQL = `
SELECT segment_start, terminal_id, item_code, value_num, coalesce(unit, ''), value_time, quality, revision, updated_at
FROM snapshot_coarse
WHERE terminal_id = $1 AND item_code = $2
  AND granularity = 'hour'
  AND segment_start >= $3 AND segment_start <= $4
ORDER BY segment_start ASC`

// Load implements buildingenergy.MeteringSource over public.snapshot_coarse (granularity='hour').
func (s *SnapshotCoarseMeteringSource) Load(ctx context.Context, point buildingenergy.PointRef, period buildingenergy.Range, asOf time.Time) (buildingenergy.MeteringData, error) {
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
		return buildingenergy.MeteringData{Profile: profile}, nil
	}
	if profile.Kind != buildingenergy.Cumulative || profile.Cadence != time.Hour {
		return buildingenergy.MeteringData{}, fmt.Errorf("%w: snapshot_coarse hourly adapter requires cumulative 1h cadence", buildingenergy.ErrInvalidProfile)
	}

	// Hour [From, From+1h) needs opening register from segment_start = From - 1h;
	// Hour [To-1h, To) needs closing register from segment_start = To - 1h.
	segFrom := period.From.UTC().Add(-time.Hour)
	segTo := period.To.UTC().Add(-time.Hour)

	rows, err := s.DB.Query(ctx, selectHourlySnapshotCoarseSQL, point.Key.TerminalID, point.Key.ItemCode, segFrom, segTo)
	if err != nil {
		return buildingenergy.MeteringData{}, err
	}
	defer rows.Close()

	var samples []buildingenergy.Sample
	for rows.Next() {
		var (
			segStart    time.Time
			rowTerminal string
			rowItemCode string
			valueNum    *float64
			unit        string
			valueTime   time.Time
			quality     int16
			rowRev      int
			updatedAt   time.Time
		)
		if err := rows.Scan(&segStart, &rowTerminal, &rowItemCode, &valueNum, &unit, &valueTime, &quality, &rowRev, &updatedAt); err != nil {
			return buildingenergy.MeteringData{}, err
		}
		if rowTerminal != point.Key.TerminalID || rowItemCode != point.Key.ItemCode {
			return buildingenergy.MeteringData{}, buildingenergy.ErrMeteringScope
		}
		// Closing boundary instant of the hourly snapshot slot [segStart, segStart+1h).
		closingAt := segStart.UTC().Add(time.Hour)
		if closingAt.After(asOf.UTC()) || updatedAt.UTC().After(asOf.UTC()) {
			// Exclude in-progress hours or snapshots written after asOf cutoff.
			continue
		}
		if !instantWithinCumulativeBoundary(closingAt, period, profile.Meta.Effective, resolved.BindingVersion.Effective) {
			continue
		}
		receivedAt := updatedAt.UTC()
		if receivedAt.Before(closingAt) {
			// A snapshot row whose last sample arrived at :55 and settled before :00 is
			// only emitted once closingAt <= asOf, making closingAt its effective boundary visibility.
			receivedAt = closingAt
		}
		sample := buildingenergy.Sample{
			At:         closingAt,
			ReceivedAt: receivedAt,
			Quality:    mapRawQuality("written", valueNum, quality, unit, profile.Unit),
			Source: buildingenergy.SourceRef{
				System: "iot-snapshot-coarse",
				ID:     fmt.Sprintf("%s:%s:hour:%s:rev%d", rowTerminal, rowItemCode, segStart.UTC().Format(time.RFC3339), rowRev),
			},
		}
		if valueNum != nil {
			sample.Value = *valueNum
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
