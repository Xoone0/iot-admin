package buildingenergy

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

const TargetRuleVersion = "f05-f14-v1"

var ErrInvalidTarget = errors.New("invalid building energy target")

// TargetScope isolates one natural year's plan within a metering boundary.
// Unit is the canonical metering unit for Energy and Timezone is the project location.
type TargetScope struct {
	Project  ProjectKey
	Object   ObjectKey
	Energy   EnergyType
	Unit     string
	Year     int
	Timezone string
}

type TargetMethod string

const (
	TargetLinear   TargetMethod = "linear"
	TargetWeighted TargetMethod = "weighted"
)

// TargetVersion is an approved, immutable plan revision as provided by the
// caller. Its bounded effective interval is half-open within the scoped year.
// Amounts count thousandths of Unit. Weighted plans use 12 weights in month
// order that sum to FullWeightPPM. Linear plans leave the weights unset.
type TargetVersion struct {
	Scope       TargetScope
	Meta        VersionMeta
	AnnualMilli int64
	Method      TargetMethod
	WeightPPM   [12]int64
}

type TargetStatus string

const (
	TargetReady                  TargetStatus = "ready"
	TargetComparable             TargetStatus = "comparable"
	TargetUnconfigured           TargetStatus = "unconfigured"
	TargetOutsideEffectivePeriod TargetStatus = "outside_effective_period"
	TargetIncompletePeriod       TargetStatus = "incomplete_period"
	TargetMissingUsage           TargetStatus = "missing_usage"
	TargetPartialCoverage        TargetStatus = "partial_coverage"
	TargetIncompatibleUsage      TargetStatus = "incompatible_usage"
	TargetZero                   TargetStatus = "zero_target"
)

type TargetPeriod struct {
	Range
	TargetMilli *int64
	Status      TargetStatus
}

type TargetPlan struct {
	Scope   TargetScope
	Version *TargetVersion
	Annual  TargetPeriod
	Months  [12]TargetPeriod

	validated bool
}

// PlanEnergyTarget selects the one revision effective at the given instant.
// It never stitches quantities from different versions into one target.
func PlanEnergyTarget(scope TargetScope, at time.Time, versions []TargetVersion) (TargetPlan, error) {
	canonical, _, err := unitConversion(scope.Energy, scope.Unit)
	if err != nil || scope.Unit != canonical || !scope.Project.valid() || !scope.Object.valid() || scope.Year < 1 || scope.Year > 9998 {
		return TargetPlan{}, fmt.Errorf("%w: expected a valid project, object, year and canonical energy unit", ErrInvalidTarget)
	}
	location, err := projectLocation(scope.Timezone)
	if err != nil {
		return TargetPlan{}, fmt.Errorf("%w: %w", ErrInvalidTarget, err)
	}
	plan := TargetPlan{Scope: scope, validated: true}
	for i := range plan.Months {
		month := time.Month(i + 1)
		from := time.Date(scope.Year, month, 1, 0, 0, 0, 0, location)
		to := time.Date(scope.Year, month+1, 1, 0, 0, 0, 0, location)
		if from.Month() != month || to.Month() != month%12+1 || !aligned(from, Month) || !aligned(to, Month) {
			return TargetPlan{}, fmt.Errorf("%w: local month has no aligned boundary", ErrInvalidTarget)
		}
		plan.Months[i] = TargetPeriod{Range: Range{From: from, To: to}, Status: TargetUnconfigured}
	}
	plan.Annual = TargetPeriod{Range: Range{From: plan.Months[0].From, To: plan.Months[11].To}, Status: TargetUnconfigured}
	if at.Before(plan.Annual.From) || !at.Before(plan.Annual.To) {
		return TargetPlan{}, fmt.Errorf("%w: target revision instant must be within the natural year", ErrInvalidTarget)
	}

	selected, err := selectTargetVersion(scope, plan.Annual.Range, at, versions)
	if err != nil {
		return TargetPlan{}, err
	}
	if selected == nil {
		return plan, nil
	}
	amounts := splitTarget(*selected)
	plan.Version = selected
	assignTarget(&plan.Annual, selected.Meta, selected.AnnualMilli)
	for i := range plan.Months {
		assignTarget(&plan.Months[i], selected.Meta, amounts[i])
	}
	return plan, nil
}

func selectTargetVersion(scope TargetScope, year Range, at time.Time, versions []TargetVersion) (*TargetVersion, error) {
	var candidates []TargetVersion
	seen := make(map[string]bool)
	for _, version := range versions {
		other := version.Scope
		if other.Project != scope.Project || other.Object != scope.Object || other.Energy != scope.Energy || other.Year != scope.Year {
			continue
		}
		if other != scope || !validTargetVersion(version, year) || seen[version.Meta.Version] {
			return nil, fmt.Errorf("%w: revision %q does not match its scope, effective interval or distribution method", ErrInvalidTarget, version.Meta.Version)
		}
		seen[version.Meta.Version] = true
		candidates = append(candidates, version)
	}
	slices.SortFunc(candidates, func(a, b TargetVersion) int { return a.Meta.Effective.From.Compare(b.Meta.Effective.From) })
	var selected *TargetVersion
	for i, revision := range candidates {
		effective := revision.Meta.Effective
		if i > 0 && effective.From.Before(candidates[i-1].Meta.Effective.To) {
			return nil, fmt.Errorf("%w: overlapping revisions", ErrInvalidTarget)
		}
		if !at.Before(effective.From) && at.Before(effective.To) {
			selected = &candidates[i]
		}
	}
	return selected, nil
}

func validTargetVersion(version TargetVersion, year Range) bool {
	// Keep the integer quantity exact across integrations using JSON numbers.
	const maxTargetMilli = 1<<53 - 1
	effective := version.Meta.Effective
	if !named(version.Meta.Version) || !version.Meta.Source.valid() || !effective.valid() ||
		effective.From.Before(year.From) || effective.To.After(year.To) ||
		version.AnnualMilli < 0 || version.AnnualMilli > maxTargetMilli {
		return false
	}
	switch version.Method {
	case TargetLinear:
		return version.WeightPPM == [12]int64{}
	case TargetWeighted:
		var sum int64
		for _, weight := range version.WeightPPM {
			if weight < 0 || weight > FullWeightPPM-sum {
				return false
			}
			sum += weight
		}
		return sum == FullWeightPPM
	default:
		return false
	}
}

// splitTarget uses the largest remainder method, breaking ties by calendar
// order. Splitting the product before multiplication prevents overflow.
func splitTarget(version TargetVersion) [12]int64 {
	weights, divisor := version.WeightPPM, int64(FullWeightPPM)
	if version.Method == TargetLinear {
		weights, divisor = [12]int64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, 12
	}
	whole, fraction := version.AnnualMilli/divisor, version.AnnualMilli%divisor
	remaining := version.AnnualMilli
	var targets, remainders [12]int64
	months := [12]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	for i, weight := range weights {
		product := fraction * weight
		targets[i] = whole*weight + product/divisor
		remainders[i] = product % divisor
		remaining -= targets[i]
	}
	slices.SortStableFunc(months[:], func(a, b int) int {
		switch {
		case remainders[a] > remainders[b]:
			return -1
		case remainders[a] < remainders[b]:
			return 1
		default:
			return 0
		}
	})
	for _, i := range months[:remaining] {
		targets[i]++
	}
	return targets
}

func assignTarget(period *TargetPeriod, meta VersionMeta, amount int64) {
	if !meta.covers(period.Range) {
		period.Status = TargetOutsideEffectivePeriod
		return
	}
	period.TargetMilli, period.Status = &amount, TargetReady
}

type TargetComparison struct {
	Status              TargetStatus
	Usage, Delta, Ratio *float64
	Coverage            WeightedCoverage
}

// CompareEnergyTarget uses an existing authorized classified metering result.
// Month zero selects the entire natural year. Only an exact, complete period
// may be compared. Ratio is usage divided by the target with no pass/fail meaning.
func CompareEnergyTarget(plan TargetPlan, month time.Month, report *ClassifiedUsageReport) (TargetComparison, error) {
	if !plan.validated || month < 0 || month > time.December {
		return TargetComparison{}, fmt.Errorf("%w: comparison requires a planned period", ErrInvalidTarget)
	}
	period := plan.Annual
	if month != 0 {
		period = plan.Months[month-1]
	}
	if !period.Range.valid() || (period.Status == TargetReady && (period.TargetMilli == nil || *period.TargetMilli < 0)) {
		return TargetComparison{}, fmt.Errorf("%w: invalid planned period", ErrInvalidTarget)
	}
	result := TargetComparison{Status: period.Status}
	if report == nil {
		if period.Status == TargetReady {
			result.Status = TargetMissingUsage
		}
		return result, nil
	}
	window, err := localWindow(report.Query.Window)
	scope := plan.Scope
	if report.Query.Project != scope.Project || report.Query.Object != scope.Object || window.Timezone != scope.Timezone ||
		!named(report.RuleVersion) || err != nil {
		return TargetComparison{Status: TargetIncompatibleUsage}, nil
	}
	if !sameRange(window.Range, period.Range) {
		status := TargetIncompatibleUsage
		if !window.From.Before(period.From) && !window.To.After(period.To) {
			status = TargetIncompletePeriod
		}
		return TargetComparison{Status: status}, nil
	}

	actual, valid := targetUsage(*report, scope)
	if !valid {
		return TargetComparison{Status: TargetIncompatibleUsage}, nil
	}
	result.Usage, result.Coverage = actual.Usage, actual.Coverage
	if period.Status != TargetReady {
		return result, nil
	}
	if trendMissing(actual) {
		result.Status = TargetMissingUsage
		return result, nil
	}
	if !trendComplete(actual) {
		result.Status = TargetPartialCoverage
		return result, nil
	}

	target := float64(*period.TargetMilli) / 1000
	delta := *actual.Usage - target
	result.Delta = &delta
	if target == 0 {
		result.Status = TargetZero
		return result, nil
	}
	ratio := *actual.Usage / target
	if !finite(ratio) {
		return TargetComparison{}, fmt.Errorf("%w: comparison exceeds numeric range", ErrInvalidTarget)
	}
	result.Status, result.Ratio = TargetComparable, &ratio
	return result, nil
}

func targetUsage(report ClassifiedUsageReport, scope TargetScope) (CategoryValue, bool) {
	if len(report.Energy) == 0 {
		return CategoryValue{}, true
	}
	var summary *EnergyCategorySummary
	for i := range report.Energy {
		if report.Energy[i].Energy != scope.Energy {
			continue
		}
		if summary != nil {
			return CategoryValue{}, false
		}
		summary = &report.Energy[i]
	}
	if summary == nil || summary.Unit != scope.Unit {
		return CategoryValue{}, false
	}
	value := summary.Total
	coverage := value.Coverage
	for _, count := range []float64{coverage.Expected, coverage.Valid, coverage.Missing, coverage.Suspect} {
		if !finite(count) || count < 0 || count > coverage.Expected {
			return CategoryValue{}, false
		}
	}
	if value.Usage != nil && (!finite(*value.Usage) || *value.Usage < 0 || coverage.Valid == 0) {
		return CategoryValue{}, false
	}
	if value.Usage == nil && coverage.Valid > 0 {
		return CategoryValue{}, false
	}
	return value, true
}
