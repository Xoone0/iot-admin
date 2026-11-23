package buildingenergy

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

var ErrInvalidBalance = errors.New("invalid building energy balance")

const balanceRuleVersion = "f09-balance-v1"

type BalanceStatus string

const (
	BalanceComplete           BalanceStatus = "complete"
	BalanceBalanced           BalanceStatus = "balanced"
	BalanceUnallocated        BalanceStatus = "unallocated"
	BalanceNegativeDifference BalanceStatus = "negative_difference"
	BalanceMissingData        BalanceStatus = "missing_data"
	BalancePartial            BalanceStatus = "partial"
	BalanceSuspectData        BalanceStatus = "suspect_data"
	BalanceMeterChange        BalanceStatus = "meter_change"
	BalanceTimeMismatch       BalanceStatus = "time_mismatch"
)

type BalanceQuery struct {
	Project ProjectKey
	Object  ObjectKey
	Energy  EnergyType
	Window  Window
}

// BalanceMeter identifies one meter and its physical footprint. Proof uses
// atomic zone IDs from the same topology source for every meter. A parent has
// no Parent field. Each compared child names that exact parent.
type BalanceMeter struct {
	Point             PointRef
	Channel           SourceRef
	MappingGeneration uint64
	Parent            PointRef
	Name              string
	Zones             []string
}

// BalanceTopology is one whole, effective revision of a verified physical
// single-line or network model for one object and energy. Its source owns the
// authority for zone identities, parent containment and true disjointness.
// The calculator checks consistency of the proof supplied by that source.
type BalanceTopology struct {
	Project  ProjectKey
	Object   ObjectKey
	Energy   EnergyType
	Unit     string // Canonical unit after meter normalization.
	Meta     VersionMeta
	Parent   BalanceMeter
	Children []BalanceMeter
}

type BalanceMeterResult struct {
	Meter            BalanceMeter
	Usage            *float64
	Coverage         Coverage
	Status           BalanceStatus
	Reasons          []Reason
	Intervals        []IntervalTrace
	ProfileVersion   string
	BindingVersion   string
	MeterRuleVersion string
}

type BalanceReport struct {
	Query          BalanceQuery
	Topology       VersionMeta
	RuleVersion    string
	Unit           string
	Parent         BalanceMeterResult
	Children       []BalanceMeterResult
	ChildrenUsage  *float64
	Difference     *float64
	DifferenceRate *float64
	Status         BalanceStatus
}

// CalculateBalance compares the selected parent with its immediate children over
// a single authorized window. The caller supplies the topology for that scope
// and one M1-A result per named meter, including results for missing data.
func CalculateBalance(query BalanceQuery, topology BalanceTopology, inputs []CategoryPointResult) (BalanceReport, error) {
	window, err := localWindow(query.Window)
	if err != nil {
		return BalanceReport{}, err
	}
	query.Window = window
	if err := validateBalanceTopology(query, topology); err != nil {
		return BalanceReport{}, err
	}
	if len(inputs) != len(topology.Children)+1 {
		return BalanceReport{}, fmt.Errorf("%w: expected one result per meter", ErrInvalidBalance)
	}
	points := make(map[PointRef]CategoryPointResult, len(inputs))
	for _, input := range inputs {
		key := input.Mapping.Point
		if _, exists := points[key]; exists {
			return BalanceReport{}, fmt.Errorf("%w: repeated meter result", ErrInvalidBalance)
		}
		points[key] = input
	}

	parent, err := balanceReading(query, topology.Unit, topology.Parent, points[topology.Parent.Point])
	if err != nil {
		return BalanceReport{}, err
	}
	report := BalanceReport{
		Query: query, Topology: topology.Meta, RuleVersion: balanceRuleVersion, Unit: topology.Unit,
		Parent: parent, Children: make([]BalanceMeterResult, 0, len(topology.Children)), Status: parent.Status,
	}
	var childrenUsage float64
	childrenComplete := true
	for _, meter := range topology.Children {
		child, err := balanceReading(query, topology.Unit, meter, points[meter.Point])
		if err != nil {
			return BalanceReport{}, err
		}
		report.Children = append(report.Children, child)
		if balanceQualityPriority(child.Status) > balanceQualityPriority(report.Status) {
			report.Status = child.Status
		}
		if child.Status != BalanceComplete {
			childrenComplete = false
			continue
		}
		childrenUsage += *child.Usage
	}
	if !childrenComplete {
		return report, nil
	}
	if !finite(childrenUsage) {
		return BalanceReport{}, fmt.Errorf("%w: child total exceeds numeric range", ErrInvalidBalance)
	}
	report.ChildrenUsage = &childrenUsage
	if parent.Status != BalanceComplete {
		return report, nil
	}

	parentUsage := *parent.Usage
	difference := parentUsage - childrenUsage
	// Two representations of the same normalized quantity can differ by one
	// representable float step. Collapse only this rounding artifact.
	peak := math.Max(parentUsage, childrenUsage)
	if parentUsage > 0 && childrenUsage > 0 && math.Abs(difference) <= peak-math.Nextafter(peak, 0) {
		difference = 0
	}
	report.Difference = &difference
	if parentUsage > 0 {
		rate := difference / parentUsage
		if !finite(rate) {
			return BalanceReport{}, fmt.Errorf("%w: difference rate exceeds numeric range", ErrInvalidBalance)
		}
		report.DifferenceRate = &rate
	}
	switch {
	case difference < 0:
		report.Status = BalanceNegativeDifference
	case difference > 0:
		report.Status = BalanceUnallocated
	default:
		report.Status = BalanceBalanced
	}
	return report, nil
}

func validateBalanceTopology(query BalanceQuery, topology BalanceTopology) error {
	canonicalUnit, _, err := unitConversion(query.Energy, topology.Unit)
	if err != nil || canonicalUnit != topology.Unit || !query.Project.valid() || !query.Object.valid() ||
		topology.Project != query.Project || topology.Object != query.Object || topology.Energy != query.Energy ||
		!topology.Meta.covers(query.Window.Range) || topology.Parent.Parent != (PointRef{}) || len(topology.Children) == 0 {
		return fmt.Errorf("%w: topology lacks proof for this project, object, energy or effective window", ErrInvalidBalance)
	}

	points := make(map[PointRef]bool, len(topology.Children)+1)
	channels := make(map[SourceRef]bool, len(topology.Children)+1)
	addMeter := func(meter BalanceMeter) error {
		if meter.Point.Project != query.Project || !meter.Point.Key.valid() || !meter.Channel.valid() ||
			meter.MappingGeneration == 0 || !named(meter.Name) || len(meter.Zones) == 0 ||
			points[meter.Point] || channels[meter.Channel] {
			return fmt.Errorf("%w: repeated, untraceable or out-of-project meter", ErrInvalidBalance)
		}
		points[meter.Point], channels[meter.Channel] = true, true
		return nil
	}
	if err := addMeter(topology.Parent); err != nil {
		return err
	}

	parentZones := make(map[string]bool, len(topology.Parent.Zones))
	for _, zone := range topology.Parent.Zones {
		if !named(zone) || parentZones[zone] {
			return fmt.Errorf("%w: blank or repeated parent zone", ErrInvalidBalance)
		}
		parentZones[zone] = true
	}
	claimed := make(map[string]bool, len(parentZones))
	for _, child := range topology.Children {
		if child.Parent != topology.Parent.Point {
			return fmt.Errorf("%w: child has no verified relationship to this parent", ErrInvalidBalance)
		}
		if err := addMeter(child); err != nil {
			return err
		}
		for _, zone := range child.Zones {
			if !parentZones[zone] || claimed[zone] {
				return fmt.Errorf("%w: overlapping or out-of-parent child zone", ErrInvalidBalance)
			}
			claimed[zone] = true
		}
	}
	return nil
}

func balanceReading(query BalanceQuery, unit string, meter BalanceMeter, input CategoryPointResult) (BalanceMeterResult, error) {
	result, mapping := input.Result, input.Mapping
	if mapping.Point != meter.Point || result.Profile.Point != meter.Point || mapping.Generation != meter.MappingGeneration ||
		result.Profile.Energy != query.Energy || result.Unit != unit || result.RuleVersion != calculationVersion {
		return BalanceMeterResult{}, fmt.Errorf("%w: selected meter, mapping generation, energy or unit mismatch", ErrInvalidBalance)
	}
	approved, err := mapping.Resolve(query.Object.Kind, query.Window.Range)
	if err != nil {
		return BalanceMeterResult{}, fmt.Errorf("%w: mapping %w", ErrInvalidBalance, err)
	}
	if !sameCategoryProfile(approved.Profile.Profile, result.Profile) || approved.Profile.Channel != meter.Channel ||
		len(approved.Allocations) != 1 || approved.Allocations[0].Target != query.Object || approved.Allocations[0].WeightPPM != FullWeightPPM {
		return BalanceMeterResult{}, fmt.Errorf("%w: channel or full object allocation differs from the approved topology", ErrInvalidBalance)
	}
	meter.Zones = slices.Clone(meter.Zones)
	reading := BalanceMeterResult{Meter: meter, Status: BalanceTimeMismatch, ProfileVersion: result.Profile.Meta.Version,
		BindingVersion: approved.BindingVersion.Version, MeterRuleVersion: result.RuleVersion}
	window := query.Window
	if !sameRange(result.Window.Range, window.Range) || result.Window.Timezone != window.Timezone ||
		result.Window.Granularity != window.Granularity || !result.Window.AsOf.Equal(window.AsOf) {
		return reading, nil
	}
	// F09 shares the M1-C trace validation and quality counting. Every physical
	// meter is scoped at full weight, without multiplying category allocations.
	if _, err := validateCategoryPoint(input, window); err != nil {
		return BalanceMeterResult{}, fmt.Errorf("%w: %w", ErrInvalidBalance, err)
	}
	var usage CategoryValue
	for _, trace := range result.Intervals {
		if trace.Usage != nil && len(trace.Sources) == 0 {
			return BalanceMeterResult{}, fmt.Errorf("%w: usable interval has no source", ErrInvalidBalance)
		}
		for _, source := range trace.Sources {
			if !source.valid() {
				return BalanceMeterResult{}, fmt.Errorf("%w: invalid trace source", ErrInvalidBalance)
			}
		}
		amount, err := usage.add(trace, 1)
		if err != nil {
			return BalanceMeterResult{}, fmt.Errorf("%w: %w", ErrInvalidBalance, err)
		}
		trace.Usage, trace.Sources = amount, slices.Clone(trace.Sources)
		reading.Intervals = append(reading.Intervals, trace)
		if trace.Reason != "" && !slices.Contains(reading.Reasons, trace.Reason) {
			reading.Reasons = append(reading.Reasons, trace.Reason)
		}
	}
	reading.Usage = usage.Usage
	reading.Coverage = Coverage{
		Expected: int(usage.Coverage.Expected), Valid: int(usage.Coverage.Valid),
		Missing: int(usage.Coverage.Missing), Suspect: int(usage.Coverage.Suspect),
	}
	switch {
	case slices.Contains(reading.Reasons, EpochChange):
		reading.Status = BalanceMeterChange
	case reading.Coverage.Suspect > 0:
		reading.Status = BalanceSuspectData
	case reading.Coverage.Valid == 0:
		reading.Status = BalanceMissingData
	case reading.Coverage.Valid < reading.Coverage.Expected:
		reading.Status = BalancePartial
	default:
		reading.Status = BalanceComplete
	}
	return reading, nil
}

func balanceQualityPriority(status BalanceStatus) int {
	switch status {
	case BalanceTimeMismatch:
		return 5
	case BalanceMeterChange:
		return 4
	case BalanceSuspectData:
		return 3
	case BalanceMissingData:
		return 2
	case BalancePartial:
		return 1
	default:
		return 0
	}
}
