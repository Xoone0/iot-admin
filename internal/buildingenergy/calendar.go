package buildingenergy

import (
	"errors"
	"fmt"
	"time"
	_ "time/tzdata"
)

var ErrInvalidWindow = errors.New("invalid building energy statistics window")

type Granularity string

const (
	Hour  Granularity = "hour"
	Day   Granularity = "day"
	Month Granularity = "month"
)

type Window struct {
	Range
	Timezone    string
	Granularity Granularity
	AsOf        time.Time
}

func projectLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("%w: an explicit project timezone is required", ErrInvalidWindow)
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidWindow, err)
	}
	return location, nil
}

func localWindow(window Window) (Window, error) {
	location, err := projectLocation(window.Timezone)
	if err != nil {
		return Window{}, err
	}
	window.From, window.To = window.From.In(location), window.To.In(location)
	window.AsOf = window.AsOf.UTC()
	if !window.Range.valid() || window.AsOf.Before(window.To) ||
		!aligned(window.From, window.Granularity) || !aligned(window.To, window.Granularity) {
		return Window{}, fmt.Errorf("%w: expected aligned boundaries and an as-of at or after the end", ErrInvalidWindow)
	}
	return window, nil
}

func aligned(at time.Time, granularity Granularity) bool {
	if at.Minute() != 0 || at.Second() != 0 || at.Nanosecond() != 0 {
		return false
	}
	switch granularity {
	case Hour:
		return true
	case Day:
		return at.Hour() == 0
	case Month:
		return at.Hour() == 0 && at.Day() == 1
	default:
		return false
	}
}

func bucketEnd(start time.Time, granularity Granularity) time.Time {
	switch granularity {
	case Day:
		return start.AddDate(0, 0, 1)
	case Month:
		return start.AddDate(0, 1, 0)
	default:
		return start.Add(time.Hour)
	}
}

type MonthWindows struct {
	Current, Baseline Range
	Timezone          string
	CutoffAdjusted    bool
}

// MonthToDate uses the same local calendar day and clock hour in the
// preceding year, clamped to month end when that day is absent.
func MonthToDate(cutoff time.Time, timezone string) (MonthWindows, error) {
	location, err := projectLocation(timezone)
	if err != nil {
		return MonthWindows{}, err
	}
	cutoff = cutoff.In(location)
	year, month, day := cutoff.Date()
	priorMonthEnd := time.Date(year-1, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	priorDay := min(day, priorMonthEnd)
	currentStart := time.Date(year, month, 1, 0, 0, 0, 0, location)
	priorStart := time.Date(year-1, month, 1, 0, 0, 0, 0, location)
	priorCutoff := time.Date(year-1, month, priorDay, cutoff.Hour(), 0, 0, 0, location)
	if !aligned(cutoff, Hour) || !aligned(currentStart, Month) || !aligned(priorStart, Month) ||
		priorCutoff.Day() != priorDay || priorCutoff.Hour() != cutoff.Hour() || !aligned(priorCutoff, Hour) ||
		!currentStart.Before(cutoff) || !priorStart.Before(priorCutoff) {
		return MonthWindows{}, fmt.Errorf("%w: month-to-date expects a valid local hour after month start", ErrInvalidWindow)
	}
	return MonthWindows{Current: Range{currentStart, cutoff}, Baseline: Range{priorStart, priorCutoff},
		Timezone: timezone, CutoffAdjusted: priorDay != day}, nil
}

type ComparisonReason string

const (
	UnequalPeriods     ComparisonReason = "unequal_periods"
	IncompleteCoverage ComparisonReason = "incomplete_coverage"
	ZeroBaseline       ComparisonReason = "zero_baseline"
)

type Comparison struct {
	Windows                       MonthWindows
	CurrentHours, BaselineHours   float64
	Current, Baseline             *float64
	CurrentCoverage, BaseCoverage Coverage
	Delta, ChangeRate             *float64
	Reason                        ComparisonReason
}

// CompareUsage compares one point using two month-to-date results normalized
// to the same unit and evaluated at the same knowledge cutoff.
func CompareUsage(windows MonthWindows, current, baseline Result) (Comparison, error) {
	if !sameRange(windows.Current, current.Window.Range) || !sameRange(windows.Baseline, baseline.Window.Range) ||
		current.Window.Timezone != windows.Timezone || baseline.Window.Timezone != windows.Timezone ||
		!current.Window.AsOf.Equal(baseline.Window.AsOf) || current.Profile.Point != baseline.Profile.Point ||
		current.Profile.Energy != baseline.Profile.Energy || current.Unit != baseline.Unit {
		return Comparison{}, errors.New("comparison requires the same point, energy, unit, and cutoff in the requested windows")
	}
	result := Comparison{
		Windows: windows, CurrentHours: windows.Current.To.Sub(windows.Current.From).Hours(),
		BaselineHours: windows.Baseline.To.Sub(windows.Baseline.From).Hours(),
		Current:       current.Usage, Baseline: baseline.Usage, CurrentCoverage: current.Coverage, BaseCoverage: baseline.Coverage,
	}
	if windows.CutoffAdjusted || result.CurrentHours != result.BaselineHours {
		result.Reason = UnequalPeriods
		return result, nil
	}
	if !current.complete() || !baseline.complete() {
		result.Reason = IncompleteCoverage
		return result, nil
	}
	delta := *current.Usage - *baseline.Usage
	result.Delta = &delta
	if *baseline.Usage == 0 {
		result.Reason = ZeroBaseline
		return result, nil
	}
	rate := delta / *baseline.Usage
	if !finite(rate) {
		return Comparison{}, errors.New("comparison change rate exceeds numeric range")
	}
	result.ChangeRate = &rate
	return result, nil
}

func sameRange(left, right Range) bool {
	return left.From.Equal(right.From) && left.To.Equal(right.To)
}
