package buildingenergy

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

func dimensionQuery(view ObjectKind, keys []ObjectKey, points ...CategoryPointResult) DimensionUsageQuery {
	window := points[0].Result.Window
	scope := Scope{Project: points[0].Mapping.Point.Project, Period: window.Range, Objects: keys}
	for _, point := range points {
		scope.Points = append(scope.Points, point.Mapping.Point.Key)
	}
	return DimensionUsageQuery{Scope: scope, View: view, Window: window, Energy: Electricity}
}

func TestF07DimensionsReconcileWithClassification(t *testing.T) {
	cases := []struct {
		view         ObjectKind
		first        [2]int
		second       [2]int
		wantA, wantB float64
	}{
		{Building, [2]int{FullWeightPPM, 0}, [2]int{0, FullWeightPPM}, 100, 60},
		{Space, [2]int{250_000, 750_000}, [2]int{500_000, 500_000}, 55, 105},
		{Organization, [2]int{750_000, 0}, [2]int{250_000, 750_000}, 90, 45},
	}
	points := []CategoryPointResult{
		categoryFixture(t, Electricity, "kWh", "one", 60, 40),
		categoryFixture(t, Electricity, "Wh", "two", 20_000, 40_000),
	}
	base, catalog, topology := categoryInputs(points...)
	catalog.Bindings = []CategoryBinding{categoryAssignment(points[0], CategoryAllocation{CategoryID: "lighting", WeightPPM: FullWeightPPM})}
	for _, tc := range cases {
		objects := [2]ObjectKey{{Kind: tc.view, ID: "a"}, {Kind: tc.view, ID: "b"}}
		for point, weights := range [2][2]int{tc.first, tc.second} {
			binding := BindingRevision{View: tc.view, Meta: versionFixture(base.Window.From)}
			for i, object := range objects {
				if weight := weights[i]; weight > 0 {
					binding.Allocations = append(binding.Allocations, Allocation{Target: object, WeightPPM: weight})
				}
			}
			if tc.view == Organization && point == 0 {
				binding.Allocations = append(binding.Allocations, Allocation{Target: ObjectKey{Kind: tc.view, ID: "outside"}, WeightPPM: 250_000})
			}
			points[point].Mapping.Bindings = append(points[point].Mapping.Bindings, binding)
		}
	}

	for _, tc := range cases {
		t.Run(string(tc.view), func(t *testing.T) {
			a, b := ObjectKey{Kind: tc.view, ID: "a"}, ObjectKey{Kind: tc.view, ID: "b"}
			query := dimensionQuery(tc.view, []ObjectKey{a, b}, points...)
			result, err := CalculateDimensionUsage(query, catalog, topology, points)
			if err != nil || result.Unit != "kWh" || result.ClassificationRuleVersion != classificationRuleVersion || len(result.Rows) != 2 {
				t.Fatalf("unexpected dimension report: %+v (%v)", result, err)
			}
			for i, row := range result.Rows {
				if row.Rank == nil || *row.Rank != i+1 || row.Status != TrendComparable {
					t.Fatalf("unexpected rank: %+v", row)
				}
				objectQuery := base
				objectQuery.Object = row.Object
				direct, err := CalculateClassifiedUsage(objectQuery, catalog, topology, points)
				if err != nil || !reflect.DeepEqual(direct.Energy[0].Total, row.Value) ||
					!reflect.DeepEqual(direct.Intervals, row.Evidence) {
					t.Fatalf("result differs from M1-C: %+v (%v)", row, err)
				}
				switch row.Object {
				case a:
					requireCategoryNumber(t, row.Value.Usage, tc.wantA)
				case b:
					requireCategoryNumber(t, row.Value.Usage, tc.wantB)
				default:
					t.Fatalf("out-of-scope target %v escaped its allocation", row.Object)
				}
			}
		})
	}
}

func TestF07RejectsUnauthorizedDuplicateAndMismatchedInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*DimensionUsageQuery, []CategoryPointResult, *CategoryTopologySnapshot)
		want   error
	}{
		{"unauthorized point", func(q *DimensionUsageQuery, _ []CategoryPointResult, _ *CategoryTopologySnapshot) {
			q.Scope.Points[0].TerminalID = "unauthorized"
		}, ErrAccessDenied},
		{"foreign project", func(q *DimensionUsageQuery, _ []CategoryPointResult, _ *CategoryTopologySnapshot) {
			q.Scope.Project.ProjectID = "other"
		}, ErrAccessDenied},
		{"duplicate point", func(_ *DimensionUsageQuery, inputs []CategoryPointResult, _ *CategoryTopologySnapshot) {
			inputs[1] = inputs[0]
		}, ErrInvalidScope},
		{"duplicate authorized point", func(q *DimensionUsageQuery, _ []CategoryPointResult, _ *CategoryTopologySnapshot) {
			q.Scope.Points = append(q.Scope.Points, q.Scope.Points[0])
		}, ErrInvalidScope},
		{"empty object scope", func(q *DimensionUsageQuery, _ []CategoryPointResult, _ *CategoryTopologySnapshot) {
			q.Scope.Objects = nil
		}, ErrInvalidScope},
		{"duplicate object", func(q *DimensionUsageQuery, _ []CategoryPointResult, _ *CategoryTopologySnapshot) {
			q.Scope.Objects = append(q.Scope.Objects, q.Scope.Objects[0])
		}, ErrInvalidScope},
		{"mixed view", func(q *DimensionUsageQuery, _ []CategoryPointResult, _ *CategoryTopologySnapshot) {
			q.Scope.Objects[0].Kind = Organization
		}, ErrInvalidScope},
		{"different window", func(q *DimensionUsageQuery, _ []CategoryPointResult, _ *CategoryTopologySnapshot) {
			q.Scope.Period.To = q.Scope.Period.To.Add(time.Hour)
		}, ErrInvalidScope},
		{"different cutoff", func(q *DimensionUsageQuery, _ []CategoryPointResult, _ *CategoryTopologySnapshot) {
			q.Window.AsOf = q.Window.AsOf.Add(time.Hour)
		}, ErrInvalidClassification},
		{"mixed energy", func(q *DimensionUsageQuery, _ []CategoryPointResult, _ *CategoryTopologySnapshot) {
			q.Energy = Water
		}, ErrInvalidScope},
		{"shared physical channel", func(_ *DimensionUsageQuery, inputs []CategoryPointResult, topo *CategoryTopologySnapshot) {
			channel := inputs[0].Mapping.Profiles[0].Channel
			inputs[1].Mapping.Profiles[0].Channel = channel
			topo.Boundaries[1][0].Channel = channel
		}, ErrInvalidClassification},
		{"duplicate allocation", func(q *DimensionUsageQuery, inputs []CategoryPointResult, _ *CategoryTopologySnapshot) {
			inputs[0].Mapping.Bindings[0].Allocations = []Allocation{
				{Target: q.Scope.Objects[0], WeightPPM: 500_000}, {Target: q.Scope.Objects[0], WeightPPM: 500_000},
			}
		}, ErrInvalidMapping},
	} {
		t.Run(tc.name, func(t *testing.T) {
			object := ObjectKey{Kind: Space, ID: "area"}
			points := []CategoryPointResult{
				categoryFixture(t, Electricity, "kWh", "one", 20, 30),
				categoryFixture(t, Electricity, "kWh", "two", 10, 30),
			}
			for i := range points {
				points[i].Mapping.Bindings = []BindingRevision{{
					View: Space, Meta: versionFixture(points[i].Result.Window.From),
					Allocations: []Allocation{{Target: object, WeightPPM: FullWeightPPM}},
				}}
			}
			_, catalog, topology := categoryInputs(points...)
			query := dimensionQuery(Space, []ObjectKey{object}, points...)
			tc.mutate(&query, points, &topology)
			report, err := CalculateDimensionUsage(query, catalog, topology, points)
			if !errors.Is(err, tc.want) || len(report.Rows) > 0 {
				t.Fatalf("want no rows and %v, got %+v (%v)", tc.want, report, err)
			}
		})
	}
}

func TestF07HistoricalBindingsProduceUsageButRequireStableRankScope(t *testing.T) {
	a := ObjectKey{Kind: Space, ID: "a"}
	b := ObjectKey{Kind: Space, ID: "b"}
	c := ObjectKey{Kind: Space, ID: "c"}
	point := categoryFixture(t, Electricity, "kWh", "one", 100, 100)
	base, catalog, topology := categoryInputs(point)
	first := versionFixture(base.Window.From)
	first.Effective.To = base.Window.From.Add(time.Hour)
	second := versionFixture(first.Effective.To)
	second.Version = "v2"
	point.Mapping.Bindings = []BindingRevision{
		{View: Space, Meta: first, Allocations: []Allocation{{Target: a, WeightPPM: 500_000}, {Target: c, WeightPPM: 500_000}}},
		{View: Space, Meta: second, Allocations: []Allocation{{Target: a, WeightPPM: 500_000}, {Target: b, WeightPPM: 500_000}}},
	}
	query := dimensionQuery(Space, []ObjectKey{a, b, c}, point)
	report, err := CalculateDimensionUsage(query, catalog, topology, []CategoryPointResult{point})
	if err != nil || len(report.Rows) != 3 {
		t.Fatalf("unexpected report: %+v (%v)", report, err)
	}
	for i, want := range []float64{100, 50, 50} {
		row := report.Rows[i]
		requireCategoryNumber(t, row.Value.Usage, want)
		if row.Status != TrendInconsistentScope || row.Rank != nil {
			t.Fatalf("unstable scope ranked: %+v", row)
		}
	}
	if got := report.Rows[0].Evidence; len(got) != 2 || got[0].ObjectBindingVersion != first.Version || got[1].ObjectBindingVersion != second.Version {
		t.Fatalf("historical evidence lost: %+v", got)
	}
}

func TestF08RankObjectsWithTiesZeroAndQuality(t *testing.T) {
	type fixture struct {
		name   string
		values []float64
		rank   int
		status TrendStatus
	}
	fixtures := []fixture{
		{"top", []float64{15, 15}, 1, TrendComparable},
		{"tie-a", []float64{10, 10}, 2, TrendComparable},
		{"tie-b", []float64{0, 20}, 2, TrendComparable},
		{"zero", []float64{0, 0}, 4, TrendComparable},
		{"missing", nil, 0, TrendMissingData},
		{"partial", []float64{80}, 0, TrendInsufficientQuality},
		{"unallocated", nil, 0, TrendMissingData},
	}
	var points []CategoryPointResult
	var keys []ObjectKey
	for _, f := range fixtures {
		object := ObjectKey{Kind: Building, ID: f.name}
		keys = append(keys, object)
		if f.name == "unallocated" {
			continue
		}
		point := categoryFixture(t, Electricity, "kWh", f.name, f.values...)
		point.Mapping.Bindings = []BindingRevision{{
			View: Building, Meta: versionFixture(point.Result.Window.From),
			Allocations: []Allocation{{Target: object, WeightPPM: FullWeightPPM}},
		}}
		points = append(points, point)
	}
	_, catalog, topology := categoryInputs(points...)
	query := dimensionQuery(Building, keys, points...)
	slices.Reverse(query.Scope.Objects)
	slices.Reverse(points)
	report, err := CalculateDimensionUsage(query, catalog, topology, points)
	if err != nil || len(report.Rows) != len(fixtures) {
		t.Fatalf("unexpected report: %+v (%v)", report, err)
	}
	for i, want := range fixtures {
		row := report.Rows[i]
		if row.Object.ID != want.name || row.Status != want.status ||
			(want.rank == 0 && row.Rank != nil) || (want.rank > 0 && (row.Rank == nil || *row.Rank != want.rank)) {
			t.Fatalf("row %d = %+v, wanted %+v", i, row, want)
		}
	}
	requireCategoryNumber(t, report.Rows[3].Value.Usage, 0)
	requireCategoryNumber(t, report.Rows[5].Value.Usage, 80)
	if report.Rows[4].Value.Usage != nil || report.Rows[4].Value.Coverage.Missing != 2 ||
		report.Rows[5].Value.Coverage.Valid != 1 || report.Rows[6].Value.Coverage.Expected != 0 {
		t.Fatalf("coverage or measured zero lost: %+v", report.Rows)
	}
}
