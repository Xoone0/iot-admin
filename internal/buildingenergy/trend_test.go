package buildingenergy

import (
	"errors"
	"testing"
	"time"
)

func TestF08CalendarPlans(t *testing.T) {
	for _, tc := range []struct {
		name, timezone, at, through    string
		grain                          Granularity
		currentFrom, currentTo         string
		previousFrom, previousTo       string
		yearFrom, yearTo               string
		previousAdjusted, yearAdjusted bool
	}{
		{
			name: "hour crossing year", timezone: "Asia/Shanghai", grain: Hour, at: "2026-01-01T00:30:00+08:00",
			currentFrom: "2026-01-01T00:00:00+08:00", currentTo: "2026-01-01T01:00:00+08:00",
			previousFrom: "2025-12-31T23:00:00+08:00", previousTo: "2026-01-01T00:00:00+08:00",
			yearFrom: "2025-01-01T00:00:00+08:00", yearTo: "2025-01-01T01:00:00+08:00",
		},
		{
			name: "month crossing year", timezone: "UTC", grain: Month, at: "2026-01-09T17:24:00Z",
			currentFrom: "2026-01-01T00:00:00Z", currentTo: "2026-02-01T00:00:00Z",
			previousFrom: "2025-12-01T00:00:00Z", previousTo: "2026-01-01T00:00:00Z",
			yearFrom: "2025-01-01T00:00:00Z", yearTo: "2025-02-01T00:00:00Z",
		},
		{
			name: "leap day month to date", timezone: "UTC", grain: Month,
			at: "2024-02-29T00:00:00Z", through: "2024-02-29T12:00:00Z",
			currentFrom: "2024-02-01T00:00:00Z", currentTo: "2024-02-29T12:00:00Z",
			previousFrom: "2024-01-01T00:00:00Z", previousTo: "2024-01-29T12:00:00Z",
			yearFrom: "2023-02-01T00:00:00Z", yearTo: "2023-02-28T12:00:00Z", yearAdjusted: true,
		},
		{
			name: "month to date across short previous month", timezone: "UTC", grain: Month,
			at: "2026-03-30T00:00:00Z", through: "2026-03-30T12:00:00Z",
			currentFrom: "2026-03-01T00:00:00Z", currentTo: "2026-03-30T12:00:00Z",
			previousFrom: "2026-02-01T00:00:00Z", previousTo: "2026-02-28T12:00:00Z", previousAdjusted: true,
			yearFrom: "2025-03-01T00:00:00Z", yearTo: "2025-03-30T12:00:00Z",
		},
		{
			name: "leap day daily counterpart", timezone: "UTC", grain: Day, at: "2024-02-29T11:34:00Z",
			currentFrom: "2024-02-29T00:00:00Z", currentTo: "2024-03-01T00:00:00Z",
			previousFrom: "2024-02-28T00:00:00Z", previousTo: "2024-02-29T00:00:00Z",
			yearFrom: "2023-02-28T00:00:00Z", yearTo: "2023-03-01T00:00:00Z", yearAdjusted: true,
		},
		{
			name: "spring forward natural day", timezone: "America/New_York", grain: Day, at: "2024-03-10T12:12:00-04:00",
			currentFrom: "2024-03-10T00:00:00-05:00", currentTo: "2024-03-11T00:00:00-04:00",
			previousFrom: "2024-03-09T00:00:00-05:00", previousTo: "2024-03-10T00:00:00-05:00",
			yearFrom: "2023-03-10T00:00:00-05:00", yearTo: "2023-03-11T00:00:00-05:00",
		},
		{
			name: "fall back natural day", timezone: "America/New_York", grain: Day, at: "2024-11-03T12:00:00-05:00",
			currentFrom: "2024-11-03T00:00:00-04:00", currentTo: "2024-11-04T00:00:00-05:00",
			previousFrom: "2024-11-02T00:00:00-04:00", previousTo: "2024-11-03T00:00:00-04:00",
			yearFrom: "2023-11-03T00:00:00-04:00", yearTo: "2023-11-04T00:00:00-04:00",
		},
		{
			name: "fall back repeated hours remain distinct", timezone: "America/New_York", grain: Hour, at: "2024-11-03T01:15:00-05:00",
			currentFrom: "2024-11-03T01:00:00-05:00", currentTo: "2024-11-03T02:00:00-05:00",
			previousFrom: "2024-11-03T01:00:00-04:00", previousTo: "2024-11-03T01:00:00-05:00",
			yearFrom: "2023-11-03T01:00:00-04:00", yearTo: "2023-11-03T02:00:00-04:00",
		},
		{
			name: "partial fall back resolves the matching occurrence", timezone: "America/New_York", grain: Day,
			at: "2024-11-05T08:00:00-05:00", through: "2024-11-05T01:00:00-05:00",
			currentFrom: "2024-11-05T00:00:00-05:00", currentTo: "2024-11-05T01:00:00-05:00",
			previousFrom: "2024-11-04T00:00:00-05:00", previousTo: "2024-11-04T01:00:00-05:00",
			yearFrom: "2023-11-05T00:00:00-04:00", yearTo: "2023-11-05T01:00:00-05:00",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := TrendRequest{At: trendTime(t, tc.at), Timezone: tc.timezone, Granularity: tc.grain, Lookback: 3}
			if tc.through != "" {
				req.Through = trendTime(t, tc.through)
			}
			got, err := PlanTrend(req)
			if err != nil {
				t.Fatal(err)
			}
			requireTrendRange(t, got.Current, tc.currentFrom, tc.currentTo)
			requireTrendRange(t, got.Previous, tc.previousFrom, tc.previousTo)
			requireTrendRange(t, got.PriorYear, tc.yearFrom, tc.yearTo)
			if got.PreviousAdjusted != tc.previousAdjusted || got.PriorYearAdjusted != tc.yearAdjusted ||
				len(got.Trend) != 3 || !sameRange(got.Trend[2], got.Current) ||
				!got.Trend[0].To.Equal(got.Trend[1].From) || !got.Trend[1].To.Equal(got.Current.From) {
				t.Fatalf("plan adjustment or consecutive buckets: %+v", got)
			}
		})
	}
}

func TestF08PlanExposesNonexistentBaselineHoursAndRejectsInvalidCutoff(t *testing.T) {
	req := TrendRequest{
		At: trendTime(t, "2025-03-10T02:00:00-04:00"), Timezone: "America/New_York",
		Granularity: Hour, Lookback: 1,
	}
	got, err := PlanTrend(req)
	if err != nil || !got.PriorYearAdjusted || got.PriorYear.To.Sub(got.PriorYear.From) != time.Hour {
		t.Fatalf("nonexistent baseline hour needs an explicit adjustment: %+v (%v)", got, err)
	}
	req.Through = req.At.Add(30 * time.Minute)
	if _, err := PlanTrend(req); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("unaligned cutoff accepted: %v", err)
	}
}

func TestF08PlanHandlesHalfHourDSTDayAndRejectsMisalignedHour(t *testing.T) {
	req := TrendRequest{
		At: trendTime(t, "2026-10-04T03:30:00+11:00"), Timezone: "Australia/Lord_Howe",
		Granularity: Day, Lookback: 1,
	}
	got, err := PlanTrend(req)
	if err != nil || got.Current.To.Sub(got.Current.From) != 23*time.Hour+30*time.Minute {
		t.Fatalf("half-hour DST day should preserve actual elapsed time: %+v (%v)", got, err)
	}
	req.Granularity = Hour
	if _, err := PlanTrend(req); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("misaligned half-hour DST comparison accepted: %v", err)
	}
}

func TestF08ComparableAndBusinessStatuses(t *testing.T) {
	plan, err := PlanTrend(TrendRequest{
		At: trendTime(t, "2026-10-15T12:00:00+08:00"), Through: trendTime(t, "2026-10-15T12:00:00+08:00"),
		Timezone: "Asia/Shanghai", Granularity: Month, Lookback: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	current := f08ClassifiedFixture(t, plan.Current, plan, 3, Normal)
	previous := f08ClassifiedFixture(t, plan.Previous, plan, 4, Normal)
	year := f08ClassifiedFixture(t, plan.PriorYear, plan, 2, Normal)
	requireUsage(t, current.Energy[0].Total.Usage, 1044)
	requireUsage(t, previous.Energy[0].Total.Usage, 1392)
	requireUsage(t, year.Energy[0].Total.Usage, 696)
	got, err := CompareClassifiedPeriods(plan, Electricity, current, previous, year)
	if err != nil || got.Previous.Status != TrendComparable || got.PriorYear.Status != TrendComparable {
		t.Fatalf("unexpected comparison: %+v (%v)", got, err)
	}
	requireUsage(t, got.Previous.Delta, -348)
	requireUsage(t, got.Previous.ChangeRate, -0.25)
	requireUsage(t, got.PriorYear.Delta, 348)
	requireUsage(t, got.PriorYear.ChangeRate, 0.5)

	for _, tc := range []struct {
		name   string
		mutate func(*ClassifiedUsageReport)
		status TrendStatus
	}{
		{"zero", func(r *ClassifiedUsageReport) { *r = f08ClassifiedFixture(t, plan.Previous, plan, 0, Normal) }, TrendZeroBaseline},
		{"missing", func(r *ClassifiedUsageReport) { *r = f08ClassifiedFixture(t, plan.Previous, plan, 4, Missing) }, TrendMissingData},
		{"all suspect", func(r *ClassifiedUsageReport) { *r = f08ClassifiedFixture(t, plan.Previous, plan, 4, Suspect) }, TrendInsufficientQuality},
		{"partial coverage", func(r *ClassifiedUsageReport) {
			r.Energy[0].Total.Coverage.Valid--
			r.Energy[0].Total.Coverage.Missing++
		}, TrendInsufficientQuality},
		{"other object", func(r *ClassifiedUsageReport) { r.Query.Object.ID = "other" }, TrendInconsistentScope},
		{"object lost all metering allocations", func(r *ClassifiedUsageReport) {
			r.Energy, r.Intervals = nil, nil
		}, TrendInconsistentScope},
		{"other unit", func(r *ClassifiedUsageReport) { r.Energy[0].Unit = "Wh" }, TrendInconsistentScope},
		{"other visible cutoff", func(r *ClassifiedUsageReport) { r.Query.Window.AsOf = r.Query.Window.AsOf.Add(time.Hour) }, TrendInconsistentScope},
		{"other calculation revision", func(r *ClassifiedUsageReport) { r.RuleVersion = "rule-changed" }, TrendInconsistentScope},
		{"meter calculation change", func(r *ClassifiedUsageReport) { r.Intervals[0].MeterRuleVersion = "meter-rule-changed" }, TrendInconsistentScope},
		{"other catalog revision", func(r *ClassifiedUsageReport) { r.CatalogVersion.Version = "catalog-changed" }, TrendInconsistentScope},
		{"other topology source", func(r *ClassifiedUsageReport) { r.TopologyVersion.Source.ID = "replacement" }, TrendInconsistentScope},
		{"mapping change", func(r *ClassifiedUsageReport) { r.Intervals[0].MappingGeneration++ }, TrendInconsistentScope},
		{"point profile change", func(r *ClassifiedUsageReport) { r.Intervals[0].ProfileVersion = "new" }, TrendInconsistentScope},
		{"object binding change", func(r *ClassifiedUsageReport) { r.Intervals[0].ObjectBindingVersion = "new" }, TrendInconsistentScope},
		{"category binding change", func(r *ClassifiedUsageReport) { r.Intervals[0].CategoryBindingVersion = "new" }, TrendInconsistentScope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline := f08ClassifiedFixture(t, plan.Previous, plan, 4, Normal)
			tc.mutate(&baseline)
			got, err := CompareClassifiedPeriods(plan, Electricity, current, baseline, year)
			if err != nil || got.Previous.Status != tc.status || got.Previous.ChangeRate != nil || got.PriorYear.Status != TrendComparable {
				t.Fatalf("wanted %s and independent YoY: %+v (%v)", tc.status, got, err)
			}
			if tc.status == TrendZeroBaseline {
				requireUsage(t, got.Previous.Delta, 1044)
			} else if got.Previous.Delta != nil {
				t.Fatal("non-comparable period returned a delta")
			}
		})
	}
}

func TestF08UnequalDSTDaysAndFullMonthsSuppressChanges(t *testing.T) {
	for _, tc := range []struct {
		at, timezone string
		grain        Granularity
		hours        float64
	}{
		{"2024-03-10T12:00:00-04:00", "America/New_York", Day, 23},
		{"2024-11-03T12:00:00-05:00", "America/New_York", Day, 25},
		{"2024-02-29T12:00:00Z", "UTC", Month, 696},
	} {
		t.Run(tc.at, func(t *testing.T) {
			plan, err := PlanTrend(TrendRequest{At: trendTime(t, tc.at), Timezone: tc.timezone, Granularity: tc.grain, Lookback: 1})
			if err != nil {
				t.Fatal(err)
			}
			current := f08ClassifiedFixture(t, plan.Current, plan, 1, Normal)
			previous := f08ClassifiedFixture(t, plan.Previous, plan, 1, Normal)
			year := f08ClassifiedFixture(t, plan.PriorYear, plan, 1, Normal)
			requireUsage(t, current.Energy[0].Total.Usage, tc.hours)
			got, err := CompareClassifiedPeriods(plan, Electricity, current, previous, year)
			if err != nil || got.Previous.Status != TrendUnequalPeriods || got.PriorYear.Status != TrendUnequalPeriods ||
				got.Previous.Delta != nil || got.Previous.ChangeRate != nil || got.PriorYear.Delta != nil || got.PriorYear.ChangeRate != nil {
				t.Fatalf("unequal periods need explicit status and no change: %+v (%v)", got, err)
			}
			previous.Query.Window.To = previous.Query.Window.To.Add(-time.Hour)
			if _, err := CompareClassifiedPeriods(plan, Electricity, current, previous, year); err == nil {
				t.Fatal("compared data from a different requested window")
			}
		})
	}
}

func trendTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func requireTrendRange(t *testing.T, got Range, from, to string) {
	t.Helper()
	if got.From.Format(time.RFC3339) != from || got.To.Format(time.RFC3339) != to {
		t.Fatalf("range [%s, %s) wanted [%s, %s)", got.From, got.To, from, to)
	}
}

func f08ClassifiedFixture(t *testing.T, period Range, plan TrendPlan, value float64, quality Quality) ClassifiedUsageReport {
	t.Helper()
	effective := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	profile := pointFixture(effective, Electricity, Interval, "kWh")
	data := MeteringData{Profile: profile}
	if quality != Missing {
		for at := period.From; at.Before(period.To); at = at.Add(time.Hour) {
			sample := intervalFixture(at, value, at.Format(time.RFC3339))
			sample.Quality = quality
			data.Samples = append(data.Samples, sample)
		}
	}
	result, err := Calculate(data, Window{Range: period, Timezone: plan.Timezone, AsOf: plan.Current.To, Granularity: Hour})
	if err != nil {
		t.Fatal(err)
	}
	point := CategoryPointResult{Result: result, Mapping: MappingTimeline{
		Point: profile.Point, Generation: 1,
		Profiles: []ProfileRevision{{Profile: profile, Channel: SourceRef{System: "fixture", ID: "channel"}}},
	}}
	query, catalog, topology := categoryInputs(point)
	query.Object = ObjectKey{Kind: Building, ID: "building-a"}
	point.Mapping.Bindings = []BindingRevision{{
		View: Building, Meta: versionFixture(effective),
		Allocations: []Allocation{{Target: query.Object, WeightPPM: FullWeightPPM}},
	}}
	catalog.Meta, topology.Meta = versionFixture(effective), versionFixture(effective)
	catalog.Bindings = []CategoryBinding{{
		Point: profile.Point, Meta: versionFixture(effective), Allocations: []CategoryAllocation{{CategoryID: "office", WeightPPM: FullWeightPPM}},
	}}
	report, err := CalculateClassifiedUsage(query, catalog, topology, []CategoryPointResult{point})
	if err != nil {
		t.Fatal(err)
	}
	return report
}
