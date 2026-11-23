package buildingenergy

import (
	"fmt"
	"slices"
	"time"
)

const AnomalyRuleVersion = "f10-f17-v1"

// AnomalyRule is an approved, scope-specific rule at the observation time.
// A candidate requires both increases over the median of the comparable
// historical days. The absolute limit uses the energy's canonical unit and
// the relative limit is a fraction of the baseline.
type AnomalyRule struct {
	Project                 ProjectKey
	Object                  ObjectKey
	Energy                  EnergyType
	Meta                    VersionMeta
	MinimumHistory          int
	MinimumAbsoluteIncrease float64
	MinimumRelativeIncrease float64
}

type AnomalyStatus string

const (
	AnomalyUsageCandidate       AnomalyStatus = "usage_candidate"
	AnomalyMeteringAnomaly      AnomalyStatus = "metering_anomaly"
	AnomalyInsufficientEvidence AnomalyStatus = "insufficient_evidence"
	AnomalyNoCandidate          AnomalyStatus = "no_candidate"
)

type AnomalyReason string

const (
	anomalyMissingRule            AnomalyReason = "missing_rule"
	anomalyRuleNotApplicable      AnomalyReason = "rule_not_applicable"
	anomalyMissingUsage           AnomalyReason = "missing_usage"
	anomalyPartialCoverage        AnomalyReason = "partial_coverage"
	anomalySuspectReadings        AnomalyReason = "suspect_readings"
	anomalyInsufficientHistory    AnomalyReason = "insufficient_history"
	anomalyIncompatiblePeriod     AnomalyReason = "incompatible_period"
	anomalyIncompatibleScope      AnomalyReason = "incompatible_scope"
	anomalyInvalidBaselineQuality AnomalyReason = "invalid_baseline_quality"
	anomalyZeroBaseline           AnomalyReason = "zero_baseline"
)

type AnomalyEvaluation struct {
	Project       ProjectKey
	Object        ObjectKey
	Energy        EnergyType
	Unit          string
	Window        Window
	Status        AnomalyStatus
	Reason        AnomalyReason
	Current       *float64
	Coverage      WeightedCoverage
	BaselineCount int
	Baseline      *float64
	Delta         *float64
	ChangeRate    *float64
}

// EvaluateUsageAnomaly compares authorized Go classifications for the same
// building, local weekday, elapsed period and observed metering boundary.
// Every supplied historical day must qualify. The caller determines which
// prior dates belong to its versioned baseline selection.
func EvaluateUsageAnomaly(energy EnergyType, rule *AnomalyRule, current ClassifiedUsageReport, history []ClassifiedUsageReport) (AnomalyEvaluation, error) {
	window := current.Query.Window
	out := AnomalyEvaluation{
		Project: current.Query.Project, Object: current.Query.Object, Energy: energy, Window: window,
		Status: AnomalyInsufficientEvidence,
	}
	reject := func(reason AnomalyReason) (AnomalyEvaluation, error) {
		out.Reason = reason
		return out, nil
	}
	location, err := projectLocation(window.Timezone)
	if err != nil || !anomalyNaturalDay(window, location) {
		return reject(anomalyIncompatiblePeriod)
	}
	if !anomalyScope(current) {
		return reject(anomalyIncompatibleScope)
	}
	latest, reason := anomalyEnergySummary(current, energy)
	if reason != "" {
		return reject(reason)
	}
	out.Unit, out.Current, out.Coverage = latest.Unit, latest.Total.Usage, latest.Total.Coverage
	if quality := anomalyUsageQuality(latest.Total); quality != "" {
		if out.Coverage.Expected > 0 {
			out.Status = AnomalyMeteringAnomaly
		}
		return reject(quality)
	}
	if !anomalyBoundaryStable(current, energy, latest.Unit) {
		return reject(anomalyIncompatibleScope)
	}
	if rule == nil {
		return reject(anomalyMissingRule)
	}
	if !applicableAnomalyRule(*rule, out) {
		return reject(anomalyRuleNotApplicable)
	}
	values := make([]float64, 0, len(history))
	days := make(map[int64]bool, len(history))
	for _, historical := range history {
		past := historical.Query.Window
		key := past.From.Unix()
		if !anomalyPeerDay(window, past, location) || days[key] {
			return reject(anomalyIncompatiblePeriod)
		}
		days[key] = true
		if !anomalySameScope(current, historical) {
			return reject(anomalyIncompatibleScope)
		}
		reading, reason := anomalyEnergySummary(historical, energy)
		if reason == anomalyMissingUsage {
			return reject(anomalyInvalidBaselineQuality)
		}
		if reason != "" || latest.Unit != reading.Unit {
			return reject(anomalyIncompatibleScope)
		}
		if !trendComplete(reading.Total) {
			return reject(anomalyInvalidBaselineQuality)
		}
		if latest.Total.Coverage.Expected != reading.Total.Coverage.Expected ||
			!anomalyBoundaryStable(historical, energy, reading.Unit) || !sameTrendPoints(energy, current.Intervals, historical.Intervals) {
			return reject(anomalyIncompatibleScope)
		}
		values = append(values, *reading.Total.Usage)
	}
	out.BaselineCount = len(values)
	if len(values) < rule.MinimumHistory {
		return reject(anomalyInsufficientHistory)
	}

	baseline := anomalyMedian(values)
	out.Baseline = &baseline
	if baseline == 0 {
		return reject(anomalyZeroBaseline)
	}
	delta := *out.Current - baseline
	change := delta / baseline
	if !finite(change) {
		return AnomalyEvaluation{}, fmt.Errorf("%w: anomaly change rate exceeds numeric range", ErrInvalidClassification)
	}
	out.Delta, out.ChangeRate = &delta, &change
	out.Status = AnomalyNoCandidate
	if delta >= rule.MinimumAbsoluteIncrease && change >= rule.MinimumRelativeIncrease {
		out.Status = AnomalyUsageCandidate
	}
	return out, nil
}

func applicableAnomalyRule(rule AnomalyRule, result AnomalyEvaluation) bool {
	return rule.Project == result.Project && rule.Object == result.Object && rule.Energy == result.Energy &&
		rule.Meta.Effective.valid() && rule.Meta.covers(result.Window.Range) && rule.MinimumHistory >= 2 &&
		finite(rule.MinimumAbsoluteIncrease) && rule.MinimumAbsoluteIncrease > 0 &&
		finite(rule.MinimumRelativeIncrease) && rule.MinimumRelativeIncrease > 0
}

func anomalyNaturalDay(window Window, location *time.Location) bool {
	from, to := window.From.In(location), window.To.In(location)
	return (window.Granularity == Hour || window.Granularity == Day) && window.Range.valid() &&
		aligned(from, Day) && aligned(to, Day) && bucketEnd(from, Day).Equal(to) && !window.AsOf.Before(to)
}

func anomalyPeerDay(current, past Window, location *time.Location) bool {
	if !anomalyNaturalDay(past, location) || past.Timezone != current.Timezone || past.Granularity != current.Granularity ||
		past.To.After(current.From) || current.To.Sub(current.From) != past.To.Sub(past.From) ||
		past.From.In(location).Weekday() != current.From.In(location).Weekday() {
		return false
	}
	_, fromOffset := current.From.In(location).Zone()
	_, toOffset := current.To.In(location).Zone()
	_, pastFromOffset := past.From.In(location).Zone()
	_, pastToOffset := past.To.In(location).Zone()
	return fromOffset == pastFromOffset && toOffset == pastToOffset
}

func anomalyScope(report ClassifiedUsageReport) bool {
	window := report.Query.Window.Range
	return report.Query.Project.valid() && report.Query.Object.valid() && named(report.RuleVersion) &&
		report.CatalogVersion.covers(window) && report.TopologyVersion.covers(window)
}

func anomalySameScope(current, past ClassifiedUsageReport) bool {
	return anomalyScope(past) && current.Query.Project == past.Query.Project && current.Query.Object == past.Query.Object &&
		current.Query.Window.AsOf.Equal(past.Query.Window.AsOf) && current.RuleVersion == past.RuleVersion &&
		sameTrendVersion(current.CatalogVersion, past.CatalogVersion) && sameTrendVersion(current.TopologyVersion, past.TopologyVersion)
}

func anomalyEnergySummary(report ClassifiedUsageReport, energy EnergyType) (EnergyCategorySummary, AnomalyReason) {
	if len(report.Energy) == 0 {
		return EnergyCategorySummary{}, anomalyMissingUsage
	}
	var selected *EnergyCategorySummary
	for i := range report.Energy {
		if report.Energy[i].Energy != energy {
			continue
		}
		if selected != nil {
			return EnergyCategorySummary{}, anomalyIncompatibleScope
		}
		selected = &report.Energy[i]
	}
	if selected == nil {
		return EnergyCategorySummary{}, anomalyIncompatibleScope
	}
	unit, _, err := unitConversion(energy, selected.Unit)
	if err != nil || unit != selected.Unit || !validAnomalyValue(selected.Total) {
		return EnergyCategorySummary{}, anomalyIncompatibleScope
	}
	return *selected, ""
}

func validAnomalyValue(value CategoryValue) bool {
	c := value.Coverage
	for _, count := range []float64{c.Expected, c.Valid, c.Missing, c.Suspect} {
		if !finite(count) || count < 0 || count > c.Expected {
			return false
		}
	}
	if value.Usage == nil {
		return c.Valid == 0
	}
	return c.Valid > 0 && finite(*value.Usage) && *value.Usage >= 0
}

func anomalyUsageQuality(value CategoryValue) AnomalyReason {
	switch {
	case value.Coverage.Suspect > 0:
		return anomalySuspectReadings
	case value.Usage == nil:
		return anomalyMissingUsage
	case !trendComplete(value):
		return anomalyPartialCoverage
	default:
		return ""
	}
}

func anomalyBoundaryStable(report ClassifiedUsageReport, energy EnergyType, unit string) bool {
	selected := make([]ClassifiedIntervalEvidence, 0, len(report.Intervals))
	for _, interval := range report.Intervals {
		if interval.Energy != energy {
			continue
		}
		if interval.Unit != unit || interval.Point.Project != report.Query.Project || !interval.Point.Key.valid() ||
			interval.MappingGeneration == 0 || !named(interval.MeterRuleVersion) || !named(interval.ProfileVersion) ||
			!named(interval.ObjectBindingVersion) || interval.Trace.Usage == nil || !interval.Trace.Quality.usable() {
			return false
		}
		selected = append(selected, interval)
	}
	return stableDimensionScope(DimensionUsageQuery{Energy: energy, Window: report.Query.Window}, selected)
}

func anomalyMedian(values []float64) float64 {
	slices.Sort(values)
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	lower, upper := values[middle-1], values[middle]
	return lower + (upper-lower)/2
}

// OpportunityPeers is a scoped projection of the existing F16 comparable
// ranking. Authorized sources and the exact comparison window remain attached
// to that ranking in the API adapter.
type OpportunityPeers struct {
	Project ProjectKey
	Object  ObjectKey
	Energy  EnergyType
	Unit    string
	Window  Window
	Rows    []OpportunityPeer
}

type OpportunityPeer struct {
	Object ObjectKey
	Value  CategoryValue
	Status TrendStatus
	Rank   *int
}

type OpportunityKind string
type OpportunityStatus string

const (
	opportunityBaselineIncrease    OpportunityKind   = "baseline_increase"
	opportunityHighUsage           OpportunityKind   = "high_usage_observation"
	opportunityPendingManualReview OpportunityStatus = "pending_manual_review"
)

type OpportunityClue struct {
	Kind            OpportunityKind
	Status          OpportunityStatus
	Object          ObjectKey
	Usage           *float64
	Baseline        *float64
	ComparablePeers int
	Evidence        string
	Limitations     []string
	Confirm         []string
}

// BuildEnergyOpportunities emits only auditable clues for manual review.
// Meter quality findings are handled separately in F10.
func BuildEnergyOpportunities(evaluation AnomalyEvaluation, peers OpportunityPeers) []OpportunityClue {
	clues := make([]OpportunityClue, 0)
	if !trendComplete(CategoryValue{Usage: evaluation.Current, Coverage: evaluation.Coverage}) {
		return clues
	}
	if evaluation.Status == AnomalyUsageCandidate && evaluation.Baseline != nil && evaluation.Delta != nil && evaluation.ChangeRate != nil {
		clues = append(clues, OpportunityClue{
			Kind: opportunityBaselineIncrease, Status: opportunityPendingManualReview,
			Object: evaluation.Object, Usage: evaluation.Current, Baseline: evaluation.Baseline,
			Evidence:    "当前用量相对同星期历史完整日的中位数，达到规则的两项增幅门槛。",
			Limitations: []string{"基线按同星期对齐，营业安排、占用和天气差异仍需核查。"},
			Confirm:     []string{"核对异常候选日期与基线日期的营业时段、占用人数和运行记录。", "核对同窗计量来源与计量边界。"},
		})
	}
	if peers.Project != evaluation.Project || peers.Object != evaluation.Object || peers.Energy != evaluation.Energy ||
		peers.Unit != evaluation.Unit || !sameRange(peers.Window.Range, evaluation.Window.Range) ||
		peers.Window.Timezone != evaluation.Window.Timezone || !peers.Window.AsOf.Equal(evaluation.Window.AsOf) {
		return clues
	}
	top, count := comparableTopPeers(peers.Rows)
	for _, row := range top {
		clues = append(clues, OpportunityClue{
			Kind: opportunityHighUsage, Status: opportunityPendingManualReview, Object: row.Object,
			Usage: row.Value.Usage, ComparablePeers: count,
			Evidence:    "当前用量在同一建筑的可比区域中居首，且有用量较低的对照区域。",
			Limitations: []string{"区域用途、使用面积、占用和运行时长均可能影响用量。"},
			Confirm:     []string{"核查区域运行时长、面积、用途与占用情况。", "沿计量来源复核设备负荷和计量归属。"},
		})
	}
	return clues
}

func comparableTopPeers(rows []OpportunityPeer) ([]OpportunityPeer, int) {
	var top []OpportunityPeer
	seen := make(map[ObjectKey]bool, len(rows))
	var count int
	var peak, minimum float64
	for _, row := range rows {
		if !row.Object.valid() || row.Object.Kind != Space || seen[row.Object] {
			return nil, 0
		}
		seen[row.Object] = true
		if row.Status != TrendComparable || row.Rank == nil || *row.Rank < 1 ||
			!validAnomalyValue(row.Value) || !trendComplete(row.Value) {
			continue
		}
		use := *row.Value.Usage
		if count == 0 || use < minimum {
			minimum = use
		}
		peak = max(peak, use)
		count++
		if *row.Rank == 1 {
			top = append(top, row)
		}
	}
	if count < 2 || len(top) == 0 || peak <= minimum {
		return nil, 0
	}
	for _, row := range top {
		if *row.Value.Usage != peak {
			return nil, 0
		}
	}
	return top, count
}
