package buildingenergy

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

const DimensionRuleVersion = "f07-f08-v1"

// DimensionUsageQuery is one view of an authorized subset of a project.
// Scope must be resolved through M1-B before loading the selected inputs. The
// caller selects peer objects within the view using the authorized directory.
type DimensionUsageQuery struct {
	Scope  Scope
	View   ObjectKind
	Window Window
	Energy EnergyType
}

type DimensionUsageReport struct {
	Unit                      string
	ClassificationRuleVersion string
	Rows                      []DimensionUsageRow
}

type DimensionUsageRow struct {
	Object ObjectKey
	Value  CategoryValue
	Rank   *int
	Status TrendStatus
	// Evidence is for internal tracing. An API must project authorized fields
	// before returning any source metadata to the caller.
	Evidence []ClassifiedIntervalEvidence
}

// CalculateDimensionUsage applies each object's effective M1-B allocations to
// the same M1-C and physical-boundary selection. It only reports the authorized
// objects, including objects with no usage, and does not combine across views.
func CalculateDimensionUsage(query DimensionUsageQuery, catalog ClassificationSnapshot, topology CategoryTopologySnapshot, inputs []CategoryPointResult) (DimensionUsageReport, error) {
	if err := validateDimensionScope(query, inputs); err != nil {
		return DimensionUsageReport{}, err
	}

	report := DimensionUsageReport{Unit: inputs[0].Result.Unit, Rows: make([]DimensionUsageRow, 0, len(query.Scope.Objects))}
	for _, object := range query.Scope.Objects {
		classified, err := CalculateClassifiedUsage(ClassifiedUsageQuery{
			Project: query.Scope.Project, Window: query.Window, Object: object,
		}, catalog, topology, inputs)
		if err != nil {
			return DimensionUsageReport{}, err
		}
		report.ClassificationRuleVersion = classified.RuleVersion
		energy, _ := trendEnergyTotal(classified, query.Energy)
		row := DimensionUsageRow{Object: object, Value: energy.Total, Evidence: classified.Intervals}
		row.Status = dimensionRankStatus(query, row)
		report.Rows = append(report.Rows, row)
	}
	rankDimensionRows(report.Rows)
	return report, nil
}

func validateDimensionScope(query DimensionUsageQuery, inputs []CategoryPointResult) error {
	scope := query.Scope
	if !scope.Project.valid() || !scope.Period.valid() || !sameRange(scope.Period, query.Window.Range) ||
		!validMappingView(query.View) || len(scope.Objects) == 0 || len(scope.Points) == 0 {
		return ErrInvalidScope
	}
	objects := make(map[ObjectKey]bool, len(scope.Objects))
	for _, object := range scope.Objects {
		if !object.valid() || object.Kind != query.View || objects[object] {
			return ErrInvalidScope
		}
		objects[object] = true
	}
	points := make(map[PointKey]bool, len(scope.Points))
	for _, point := range scope.Points {
		if _, listed := points[point]; !point.valid() || listed {
			return ErrInvalidScope
		}
		points[point] = false
	}
	if len(inputs) != len(points) {
		return fmt.Errorf("%w: dimension inputs do not match the selected points", ErrInvalidScope)
	}
	for _, input := range inputs {
		point := input.Mapping.Point
		visited, allowed := points[point.Key]
		if point.Project != scope.Project || !allowed {
			return ErrAccessDenied
		}
		if visited || input.Result.Profile.Energy != query.Energy {
			return ErrInvalidScope
		}
		points[point.Key] = true
	}
	return nil
}

func dimensionRankStatus(query DimensionUsageQuery, row DimensionUsageRow) TrendStatus {
	if trendMissing(row.Value) {
		return TrendMissingData
	}
	if !stableDimensionScope(query, row.Evidence) {
		return TrendInconsistentScope
	}
	if !trendComplete(row.Value) {
		return TrendInsufficientQuality
	}
	return TrendComparable
}

func stableDimensionScope(query DimensionUsageQuery, evidence []ClassifiedIntervalEvidence) bool {
	points, consistent := trendPointScope(query.Energy, evidence)
	if !consistent || len(points) == 0 {
		return false
	}

	// Expected coverage is weighted by actual assignments. A meter that was
	// assigned only for half the window may have 100% coverage of that half.
	// Such a change in boundaries still makes the object ineligible to rank.
	ends := make(map[PointRef]time.Time, len(points))
	for _, interval := range evidence {
		start, seen := ends[interval.Point]
		if !seen {
			start = query.Window.From
		}
		if !interval.Trace.From.Equal(start) {
			return false
		}
		ends[interval.Point] = interval.Trace.To
	}
	for _, end := range ends {
		if !end.Equal(query.Window.To) {
			return false
		}
	}
	return true
}

func rankDimensionRows(rows []DimensionUsageRow) {
	slices.SortFunc(rows, func(a, b DimensionUsageRow) int {
		aComparable, bComparable := a.Status == TrendComparable, b.Status == TrendComparable
		switch {
		case aComparable && bComparable:
			return cmp.Or(cmp.Compare(*b.Value.Usage, *a.Value.Usage), cmp.Compare(a.Object.ID, b.Object.ID))
		case aComparable:
			return -1
		case bComparable:
			return 1
		default:
			return cmp.Compare(a.Object.ID, b.Object.ID)
		}
	})
	var rank int
	for i := range rows {
		if rows[i].Status != TrendComparable {
			break
		}
		if i == 0 || *rows[i].Value.Usage != *rows[i-1].Value.Usage {
			rank = i + 1
		}
		value := rank
		rows[i].Rank = &value
	}
}
