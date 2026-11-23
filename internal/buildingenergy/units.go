package buildingenergy

import (
	"errors"
	"fmt"
)

type EnergyType string

const (
	Electricity EnergyType = "electricity"
	Water       EnergyType = "water"
	Gas         EnergyType = "gas"
	Cooling     EnergyType = "cooling"
	Heat        EnergyType = "heat"
	Steam       EnergyType = "steam"
)

var ErrInvalidUnit = errors.New("unit is not supported for this energy type")

func unitConversion(energy EnergyType, input string) (string, float64, error) {
	switch energy {
	case Electricity:
		switch input {
		case "Wh":
			return "kWh", 0.001, nil
		case "kWh":
			return "kWh", 1, nil
		case "MWh":
			return "kWh", 1000, nil
		}
	case Water:
		switch input {
		case "L":
			return "m³", 0.001, nil
		case "m³", "m3":
			return "m³", 1, nil
		}
	case Gas:
		switch input {
		case "Nm³", "Nm3":
			return "Nm³", 1, nil
		}
	case Cooling, Heat, Steam:
		switch input {
		case "MJ":
			return "GJ", 0.001, nil
		case "GJ":
			return "GJ", 1, nil
		case "kWh":
			return "GJ", 0.0036, nil
		}
	}
	return "", 0, fmt.Errorf("%w: %s %q", ErrInvalidUnit, energy, input)
}

type CoalFactor struct {
	Project     ProjectKey
	Energy      EnergyType
	Unit        string
	KgcePerUnit float64
	Approved    bool
	Meta        VersionMeta
}

type CoalSummary struct {
	Kilograms   *float64
	Unavailable []EnergyType
	Factors     []CoalFactor
}

// FoldCoal combines complete energy categories from a single verified accounting
// boundary. The caller supplies at most one result per category; water is separate.
func FoldCoal(results []Result, factors []CoalFactor) (CoalSummary, error) {
	if len(results) == 0 {
		return CoalSummary{}, nil
	}
	first := results[0]
	project, window := first.Profile.Point.Project, first.Window
	if !project.valid() || !window.Range.valid() {
		return CoalSummary{}, errors.New("coal conversion requires a valid project and window")
	}
	seen := make(map[EnergyType]bool)
	var summary CoalSummary
	var total float64
	for _, result := range results {
		energy := result.Profile.Energy
		if result.Profile.Point.Project != project || !sameRange(result.Window.Range, window.Range) ||
			result.Window.Timezone != window.Timezone || !result.Window.AsOf.Equal(window.AsOf) || seen[energy] {
			return CoalSummary{}, errors.New("coal conversion requires unique energy categories from the same project and data window")
		}
		seen[energy] = true
		if energy == Water {
			continue
		}
		if !result.complete() {
			summary.Unavailable = append(summary.Unavailable, energy)
			continue
		}
		var match *CoalFactor
		for idx := range factors {
			factor := &factors[idx]
			if factor.Project != project || factor.Energy != energy || factor.Unit != result.Unit ||
				!factor.Approved || !factor.Meta.covers(window.Range) || factor.KgcePerUnit <= 0 || !finite(factor.KgcePerUnit) {
				continue
			}
			if match != nil {
				return CoalSummary{}, fmt.Errorf("overlapping approved coal factors for %s", energy)
			}
			match = factor
		}
		if match == nil {
			summary.Unavailable = append(summary.Unavailable, energy)
			continue
		}
		summary.Factors = append(summary.Factors, *match)
		total += *result.Usage * match.KgcePerUnit
	}
	if !finite(total) {
		return CoalSummary{}, errors.New("coal equivalent exceeds numeric range")
	}
	if len(summary.Unavailable) == 0 && len(summary.Factors) > 0 {
		summary.Kilograms = &total
	}
	return summary, nil
}
