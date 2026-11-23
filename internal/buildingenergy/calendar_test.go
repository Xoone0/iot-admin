package buildingenergy

import (
	"errors"
	"testing"
	"time"
	_ "time/tzdata"
)

func TestNaturalMonthsHaveNoOverlapAtTheSharedRegister(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, location)
	to := time.Date(2024, 3, 1, 0, 0, 0, 0, location)
	var samples []Sample
	for at, register := from, 1000.0; !at.After(to); at, register = at.Add(time.Hour), register+1 {
		samples = append(samples, readFixture(at, register, at.Format(time.RFC3339)))
	}
	window := Window{Range: Range{From: from.UTC(), To: to.UTC()}, Timezone: location.String(), AsOf: to, Granularity: Month}
	got, err := Calculate(MeteringData{Profile: pointFixture(from, Electricity, Cumulative, "kWh"), Samples: samples}, window)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 2 || !got.Buckets[0].To.Equal(got.Buckets[1].From) || got.Valid != 1440 {
		t.Fatalf("unexpected natural month boundaries: %+v", got.Buckets)
	}
	requireUsage(t, got.Buckets[0].Usage, 744)
	requireUsage(t, got.Buckets[1].Usage, 696)
	requireUsage(t, got.Usage, 1440)
	t.Logf("January %g kWh; leap February %g kWh; total %g", *got.Buckets[0].Usage, *got.Buckets[1].Usage, *got.Usage)
}

func TestHalfHourDSTUsesConfirmedCadence(t *testing.T) {
	location, err := time.LoadLocation("Australia/Lord_Howe")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 4, 0, 0, 0, 0, location)
	to := time.Date(2026, 10, 5, 0, 0, 0, 0, location)
	profile := pointFixture(from, Electricity, Cumulative, "kWh")
	window := fixtureWindow(from, to, Day)
	if _, err := Calculate(MeteringData{Profile: profile}, window); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("misaligned hourly cadence was accepted: %v", err)
	}
	profile.Cadence = 30 * time.Minute
	var samples []Sample
	for at, register := from, 0.0; !at.After(to); at, register = at.Add(30*time.Minute), register+1 {
		samples = append(samples, readFixture(at, register, at.Format(time.RFC3339)))
	}
	got, err := Calculate(MeteringData{Profile: profile, Samples: samples}, window)
	if err != nil || got.Valid != 47 {
		t.Fatalf("half-hour DST day: expected 47 slots, got %+v (%v)", got, err)
	}
	requireUsage(t, got.Usage, 47)
	t.Logf("half-hour DST day: %d slots, %g kWh", got.Valid, *got.Usage)
}

func TestAT06MonthToDateUsesProjectLocalCutoff(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 10, 15, 12, 0, 0, 0, location)
	windows, err := MonthToDate(cutoff.UTC(), location.String())
	if err != nil {
		t.Fatal(err)
	}
	if windows.Current.From.Format(time.RFC3339) != "2026-10-01T00:00:00+08:00" ||
		windows.Baseline.From.Format(time.RFC3339) != "2025-10-01T00:00:00+08:00" ||
		windows.Baseline.To.Format(time.RFC3339) != "2025-10-15T12:00:00+08:00" {
		t.Fatalf("mismatched local windows: %+v", windows)
	}
	asOf := cutoff.Add(time.Hour)
	calculated := func(span Range, value float64) Result {
		t.Helper()
		return fixedIntervalResult(t, span, windows.Timezone, asOf, value)
	}
	current, base := calculated(windows.Current, 3), calculated(windows.Baseline, 2)
	requireUsage(t, current.Usage, 1044)
	requireUsage(t, base.Usage, 696)
	got, err := CompareUsage(windows, current, base)
	if err != nil {
		t.Fatal(err)
	}
	requireUsage(t, got.Delta, 348)
	requireUsage(t, got.ChangeRate, 0.5)
	if got.CurrentHours != 348 || got.BaselineHours != 348 {
		t.Fatalf("hours not reported: %+v", got)
	}
	zero, err := CompareUsage(windows, current, calculated(windows.Baseline, 0))
	if err != nil || zero.ChangeRate != nil {
		t.Fatalf("baseline-zero rate: %+v (%v)", zero, err)
	}
	requireUsage(t, zero.Delta, 1044)
	misaligned := base
	misaligned.Window.To = base.Window.To.Add(-time.Hour)
	if _, err := CompareUsage(windows, current, misaligned); err == nil {
		t.Fatal("compared data from a different baseline window")
	}
	t.Logf("cutoff %s; baseline %s; %g vs %g kWh, change %.0f%%; zero baseline rate nil",
		windows.Current.To.Format(time.RFC3339), windows.Baseline.To.Format(time.RFC3339), *current.Usage, *base.Usage, *got.ChangeRate*100)
}

func TestAT06LeapDayAndIncompleteCoverageRemainVisible(t *testing.T) {
	cutoff := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
	windows, err := MonthToDate(cutoff, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if !windows.CutoffAdjusted || windows.Baseline.To.Format(time.RFC3339) != "2023-02-28T12:00:00Z" {
		t.Fatalf("previous February cutoff: %+v", windows)
	}
	current := fixedIntervalResult(t, windows.Current, "UTC", cutoff, 1)
	previous := fixedIntervalResult(t, windows.Baseline, "UTC", cutoff, 1)
	got, err := CompareUsage(windows, current, previous)
	if err != nil || got.CurrentHours != 684 || got.BaselineHours != 660 || got.Delta != nil || got.ChangeRate != nil || got.Reason != UnequalPeriods {
		t.Fatalf("unequal natural periods: %+v (%v)", got, err)
	}

	cutoff = time.Date(2026, 1, 2, 1, 0, 0, 0, time.UTC)
	windows, err = MonthToDate(cutoff, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	current = fixedIntervalResult(t, windows.Current, "UTC", cutoff, 1)
	data := MeteringData{Profile: pointFixture(windows.Baseline.From, Electricity, Interval, "kWh")}
	previous, err = Calculate(data, Window{Range: windows.Baseline, Timezone: "UTC", AsOf: cutoff, Granularity: Hour})
	if err != nil {
		t.Fatal(err)
	}
	got, err = CompareUsage(windows, current, previous)
	if err != nil || got.ChangeRate != nil || got.Delta != nil || got.Reason != IncompleteCoverage {
		t.Fatalf("incomplete comparison: %+v (%v)", got, err)
	}
	t.Log("leap day aligned to 2023-02-28 12:00; 684 vs 660 hours; suppressed unmatched or incomplete change")
}

func fixedIntervalResult(t *testing.T, period Range, timezone string, asOf time.Time, value float64) Result {
	t.Helper()
	var samples []Sample
	for at := period.From; at.Before(period.To); at = at.Add(time.Hour) {
		samples = append(samples, intervalFixture(at, value, at.Format(time.RFC3339)))
	}
	profile := pointFixture(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Electricity, Interval, "kWh")
	got, err := Calculate(MeteringData{Profile: profile, Samples: samples},
		Window{Range: period, Timezone: timezone, AsOf: asOf, Granularity: Hour})
	if err != nil {
		t.Fatal(err)
	}
	return got
}
