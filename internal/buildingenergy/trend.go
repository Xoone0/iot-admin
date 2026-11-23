package buildingenergy

import (
	"fmt"
	"slices"
	"time"
)

const TrendRuleVersion = "f08-v1"

// TrendRequest selects one project-local reporting period. Through, when
// present, is an exclusive hour boundary within that period. Lookback includes
// the selected period; earlier trend buckets are complete natural periods.
type TrendRequest struct {
	At, Through time.Time
	Timezone    string
	Granularity Granularity
	Lookback    int
}

type TrendPlan struct {
	Current, Previous, PriorYear        Range
	Trend                               []Range
	PreviousAdjusted, PriorYearAdjusted bool
	Timezone                            string
	Granularity                         Granularity
}

// PlanTrend uses adjacent natural periods for preceding-period comparisons
// and the corresponding local calendar period for prior-year comparisons.
func PlanTrend(request TrendRequest) (TrendPlan, error) {
	location, err := projectLocation(request.Timezone)
	if err != nil {
		return TrendPlan{}, err
	}
	if request.At.IsZero() || request.Lookback < 1 {
		return TrendPlan{}, fmt.Errorf("%w: trend requires an anchor and positive lookback", ErrInvalidWindow)
	}
	grain := request.Granularity
	anchor := request.At.In(location)
	year, month, day := anchor.Date()
	var start time.Time
	switch grain {
	case Hour:
		start = anchor.Add(-time.Duration(anchor.Minute())*time.Minute - time.Duration(anchor.Second())*time.Second - time.Duration(anchor.Nanosecond()))
	case Day:
		start = time.Date(year, month, day, 0, 0, 0, 0, location)
	case Month:
		start = time.Date(year, month, 1, 0, 0, 0, 0, location)
	default:
		return TrendPlan{}, fmt.Errorf("%w: unsupported trend granularity %q", ErrInvalidWindow, grain)
	}
	end := bucketEnd(start, grain)
	if !aligned(start, grain) || !aligned(end, grain) || anchor.Before(start) || !anchor.Before(end) {
		return TrendPlan{}, fmt.Errorf("%w: the anchor has no aligned local period", ErrInvalidWindow)
	}
	through := end
	if !request.Through.IsZero() {
		through = request.Through.In(location)
		if !aligned(through, Hour) || !through.After(start) || through.After(end) {
			return TrendPlan{}, fmt.Errorf("%w: trend cutoff must be an hour boundary within the current period", ErrInvalidWindow)
		}
	}

	previous, err := precedingTrendPeriod(start, grain)
	if err != nil {
		return TrendPlan{}, err
	}
	priorYear, yearAdjusted := trendPriorYear(start, grain)
	if !aligned(priorYear.From, grain) || !aligned(priorYear.To, grain) {
		return TrendPlan{}, fmt.Errorf("%w: no aligned prior-year period", ErrInvalidWindow)
	}
	plan := TrendPlan{
		Current: Range{From: start, To: through}, Previous: previous, PriorYear: priorYear,
		Timezone: request.Timezone, Granularity: grain, PriorYearAdjusted: yearAdjusted,
		Trend: make([]Range, request.Lookback),
	}
	plan.Trend[len(plan.Trend)-1] = plan.Current
	for i := len(plan.Trend) - 2; i >= 0; i-- {
		plan.Trend[i], err = precedingTrendPeriod(plan.Trend[i+1].From, grain)
		if err != nil {
			return TrendPlan{}, err
		}
	}
	if through.Before(end) {
		plan.Previous, plan.PreviousAdjusted = cutoffTrendPeriod(previous, through, grain)
		var cutoffAdjusted bool
		plan.PriorYear, cutoffAdjusted = cutoffTrendPeriod(priorYear, through, grain)
		plan.PriorYearAdjusted = plan.PriorYearAdjusted || cutoffAdjusted
	}
	return plan, nil
}

func precedingTrendPeriod(end time.Time, grain Granularity) (Range, error) {
	start := end.Add(-time.Hour)
	switch grain {
	case Day:
		start = end.AddDate(0, 0, -1)
	case Month:
		start = end.AddDate(0, -1, 0)
	}
	if !aligned(start, grain) || !bucketEnd(start, grain).Equal(end) {
		return Range{}, fmt.Errorf("%w: preceding local period is not aligned", ErrInvalidWindow)
	}
	return Range{From: start, To: end}, nil
}

func trendPriorYear(start time.Time, grain Granularity) (Range, bool) {
	year, month, day := start.Date()
	targetDay := min(day, daysInTrendMonth(year-1, month))
	from := time.Date(year-1, month, targetDay, 0, 0, 0, 0, start.Location())
	shifted := from.Day() != targetDay
	if grain == Hour {
		from, shifted = trendWallHour(year-1, month, targetDay, start)
	}
	return Range{From: from, To: bucketEnd(from, grain)}, shifted || targetDay != day
}

func cutoffTrendPeriod(period Range, cutoff time.Time, grain Granularity) (Range, bool) {
	year, month, day := period.From.Date()
	clamped := false
	if grain == Month {
		day = min(cutoff.Day(), daysInTrendMonth(year, month))
		clamped = day != cutoff.Day()
	}
	target, shifted := trendWallHour(year, month, day, cutoff)
	if !target.After(period.From) || target.After(period.To) || !aligned(target, Hour) {
		return period, true
	}
	return Range{From: period.From, To: target}, clamped || shifted
}

func daysInTrendMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// A time.Time carries the chosen offset for an ambiguous local cutoff. When
// the target wall hour repeats, prefer that offset if the target supports it.
// Nonexistent hours are explicitly marked as adjusted.
func trendWallHour(year int, month time.Month, day int, reference time.Time) (time.Time, bool) {
	hour := reference.Hour()
	candidate := time.Date(year, month, day, hour, 0, 0, 0, reference.Location())
	matches := func(at time.Time) bool {
		y, m, d := at.Date()
		return y == year && m == month && d == day && at.Hour() == hour && aligned(at, Hour)
	}
	if !matches(candidate) {
		return candidate, true
	}
	_, offset := candidate.Zone()
	_, referenceOffset := reference.Zone()
	if offset == referenceOffset {
		return candidate, false
	}
	preferred := candidate.Add(time.Duration(offset-referenceOffset) * time.Second)
	if matches(preferred) {
		return preferred, false
	}
	begin, end := candidate.ZoneBounds()
	for _, neighbor := range []time.Time{begin, end} {
		if neighbor.IsZero() {
			continue
		}
		if neighbor.Equal(begin) {
			neighbor = neighbor.Add(-time.Nanosecond)
		}
		_, otherOffset := neighbor.Zone()
		other := candidate.Add(time.Duration(offset-otherOffset) * time.Second)
		if !other.Equal(candidate) && matches(other) {
			return candidate, true
		}
	}
	return candidate, false
}

type TrendStatus string

const (
	TrendComparable          TrendStatus = "comparable"
	TrendUnequalPeriods      TrendStatus = "unequal_periods"
	TrendMissingData         TrendStatus = "missing_data"
	TrendInsufficientQuality TrendStatus = "insufficient_quality"
	TrendInconsistentScope   TrendStatus = "inconsistent_scope"
	TrendZeroBaseline        TrendStatus = "zero_baseline"
)

type TrendComparison struct {
	Status            TrendStatus
	Delta, ChangeRate *float64
}

type TrendComparisonSet struct {
	Previous, PriorYear TrendComparison
}

// CompareClassifiedPeriods compares one selected energy from three scoped
// M1-C reports. It retains absolute delta for a proven zero baseline and only
// reports a relative change for equal, fully covered and comparable periods.
func CompareClassifiedPeriods(plan TrendPlan, energy EnergyType, current, previous, priorYear ClassifiedUsageReport) (TrendComparisonSet, error) {
	for _, item := range []struct {
		period Range
		report ClassifiedUsageReport
	}{{plan.Current, current}, {plan.Previous, previous}, {plan.PriorYear, priorYear}} {
		if !item.period.valid() || !sameRange(item.period, item.report.Query.Window.Range) || item.report.Query.Window.Timezone != plan.Timezone {
			return TrendComparisonSet{}, fmt.Errorf("%w: classified report does not match its comparison period", ErrInvalidWindow)
		}
	}
	prior, err := compareTrendPair(energy, current, previous, plan.PreviousAdjusted)
	if err != nil {
		return TrendComparisonSet{}, err
	}
	annual, err := compareTrendPair(energy, current, priorYear, plan.PriorYearAdjusted)
	if err != nil {
		return TrendComparisonSet{}, err
	}
	return TrendComparisonSet{Previous: prior, PriorYear: annual}, nil
}

func compareTrendPair(energy EnergyType, current, baseline ClassifiedUsageReport, adjusted bool) (TrendComparison, error) {
	today, before := current.Query.Window, baseline.Query.Window
	if adjusted || today.To.Sub(today.From) != before.To.Sub(before.From) {
		return TrendComparison{Status: TrendUnequalPeriods}, nil
	}
	if current.Query.Project != baseline.Query.Project || current.Query.Object != baseline.Query.Object ||
		!today.AsOf.Equal(before.AsOf) || current.RuleVersion != baseline.RuleVersion ||
		!sameTrendVersion(current.CatalogVersion, baseline.CatalogVersion) ||
		!sameTrendVersion(current.TopologyVersion, baseline.TopologyVersion) {
		return TrendComparison{Status: TrendInconsistentScope}, nil
	}
	latest, foundCurrent := trendEnergyTotal(current, energy)
	base, foundBaseline := trendEnergyTotal(baseline, energy)
	if foundCurrent != foundBaseline || (!foundCurrent && len(current.Energy) > 0) || (!foundBaseline && len(baseline.Energy) > 0) ||
		(foundCurrent && foundBaseline && latest.Unit != base.Unit) {
		return TrendComparison{Status: TrendInconsistentScope}, nil
	}
	if !foundCurrent || !foundBaseline {
		return TrendComparison{Status: TrendMissingData}, nil
	}
	if latest.Total.Coverage.Expected != base.Total.Coverage.Expected || !sameTrendPoints(energy, current.Intervals, baseline.Intervals) {
		return TrendComparison{Status: TrendInconsistentScope}, nil
	}
	if trendMissing(latest.Total) || trendMissing(base.Total) {
		return TrendComparison{Status: TrendMissingData}, nil
	}
	if !trendComplete(latest.Total) || !trendComplete(base.Total) {
		return TrendComparison{Status: TrendInsufficientQuality}, nil
	}
	delta := *latest.Total.Usage - *base.Total.Usage
	if *base.Total.Usage == 0 {
		return TrendComparison{Status: TrendZeroBaseline, Delta: &delta}, nil
	}
	rate := delta / *base.Total.Usage
	if !finite(rate) {
		return TrendComparison{}, fmt.Errorf("%w: trend change rate exceeds numeric range", ErrInvalidClassification)
	}
	return TrendComparison{Status: TrendComparable, Delta: &delta, ChangeRate: &rate}, nil
}

func trendEnergyTotal(report ClassifiedUsageReport, energy EnergyType) (EnergyCategorySummary, bool) {
	for _, summary := range report.Energy {
		if summary.Energy == energy {
			return summary, true
		}
	}
	return EnergyCategorySummary{}, false
}

func trendMissing(value CategoryValue) bool {
	return value.Usage == nil && value.Coverage.Suspect == 0
}

func trendComplete(value CategoryValue) bool {
	c := value.Coverage
	return value.Usage != nil && c.Expected > 0 && c.Valid == c.Expected && c.Missing == 0 && c.Suspect == 0
}

func sameTrendVersion(a, b VersionMeta) bool {
	return a.Version == b.Version && a.Source == b.Source && sameRange(a.Effective, b.Effective)
}

func sameTrendPoints(energy EnergyType, a, b []ClassifiedIntervalEvidence) bool {
	latest, consistent := trendPointScope(energy, a)
	base, baselineConsistent := trendPointScope(energy, b)
	if !consistent || !baselineConsistent || len(latest) == 0 || len(latest) != len(base) {
		return false
	}
	for point, evidence := range latest {
		before, exists := base[point]
		if !exists || !sameTrendPoint(evidence, before) {
			return false
		}
	}
	return true
}

func trendPointScope(energy EnergyType, intervals []ClassifiedIntervalEvidence) (map[PointRef]ClassifiedIntervalEvidence, bool) {
	points := make(map[PointRef]ClassifiedIntervalEvidence)
	for _, evidence := range intervals {
		if evidence.Energy != energy {
			continue
		}
		if current, exists := points[evidence.Point]; exists {
			if !sameTrendPoint(evidence, current) {
				return nil, false
			}
			continue
		}
		points[evidence.Point] = evidence
	}
	return points, true
}

func sameTrendPoint(a, b ClassifiedIntervalEvidence) bool {
	return a.Unit == b.Unit && a.MappingGeneration == b.MappingGeneration && a.ProfileVersion == b.ProfileVersion &&
		a.MeterRuleVersion != "" && a.MeterRuleVersion == b.MeterRuleVersion &&
		a.ObjectBindingVersion == b.ObjectBindingVersion && a.CategoryBindingVersion == b.CategoryBindingVersion &&
		slices.Equal(a.Allocations, b.Allocations)
}
