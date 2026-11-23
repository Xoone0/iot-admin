package buildingenergy

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"
)

func categoryFixture(t *testing.T, energy EnergyType, unit, meter string, values ...float64) CategoryPointResult {
	t.Helper()
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	profile := pointFixture(start, energy, Interval, unit)
	profile.Point.Key.TerminalID = meter
	var samples []Sample
	for i, value := range values {
		samples = append(samples, intervalFixture(start.Add(time.Duration(i)*time.Hour), value, meter))
	}
	result, err := Calculate(MeteringData{Profile: profile, Samples: samples}, fixtureWindow(start, start.Add(2*time.Hour), Hour))
	if err != nil {
		t.Fatal(err)
	}
	return CategoryPointResult{
		Result: result,
		Mapping: MappingTimeline{
			Point: profile.Point, Generation: 1,
			Profiles: []ProfileRevision{{Profile: profile, Channel: SourceRef{System: "fixture", ID: meter}}},
		},
	}
}

func categoryInputs(points ...CategoryPointResult) (ClassifiedUsageQuery, ClassificationSnapshot, CategoryTopologySnapshot) {
	project := points[0].Mapping.Point.Project
	window := points[0].Result.Window
	query := ClassifiedUsageQuery{Project: project, Window: window}
	catalog := ClassificationSnapshot{
		Project: project, Meta: versionFixture(window.From),
		Nodes: []CategoryNode{
			{ID: "building", Name: "Building electricity", Energy: Electricity},
			{ID: "systems", ParentID: "building", Name: "Systems", Energy: Electricity},
			{ID: "lighting", ParentID: "systems", Name: "Lighting", Energy: Electricity},
			{ID: "office", ParentID: "building", Name: "Office", Energy: Electricity},
			{ID: "water", Name: "Water", Energy: Water},
		},
	}
	topology := CategoryTopologySnapshot{Project: project, Meta: versionFixture(window.From)}
	for _, p := range points {
		topology.Boundaries = append(topology.Boundaries, []CategoryBoundaryContributor{{
			BoundaryContributor: BoundaryContributor{Point: p.Mapping.Point, Role: BoundaryTotal},
			MappingGeneration:   p.Mapping.Generation,
			Channel:             p.Mapping.Profiles[0].Channel,
		}})
	}
	return query, catalog, topology
}

func categoryAssignment(p CategoryPointResult, allocations ...CategoryAllocation) CategoryBinding {
	return CategoryBinding{Point: p.Mapping.Point, Meta: versionFixture(p.Result.Window.From), Allocations: allocations}
}

func requireCategoryNumber(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-10 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestM1CCompleteLeafSplitStillNeedsExplicitObjectProportion(t *testing.T) {
	p := categoryFixture(t, Electricity, "kWh", "e1", 100, 0)
	query, catalog, topology := categoryInputs(p)
	query.Object = ObjectKey{Kind: Building, ID: "building-a"}
	p.Mapping.Bindings = []BindingRevision{{
		View: Building, Meta: versionFixture(query.Window.From),
		Allocations: []Allocation{{Target: query.Object, WeightPPM: 500_000}},
	}}
	catalog.Bindings = []CategoryBinding{categoryAssignment(p,
		CategoryAllocation{CategoryID: "office", WeightPPM: 500_000},
		CategoryAllocation{CategoryID: "lighting", WeightPPM: 500_000},
	)}
	if _, err := CalculateClassifiedUsage(query, catalog, topology, []CategoryPointResult{p}); !errors.Is(err, ErrInvalidClassification) {
		t.Fatalf("complete split bypassed the proportional policy: %v", err)
	}
	catalog.ProportionalAcrossObjects = true
	report, err := CalculateClassifiedUsage(query, catalog, topology, []CategoryPointResult{p})
	if err != nil {
		t.Fatal(err)
	}
	energy := report.Energy[0]
	root := energy.Categories[0]
	requireCategoryNumber(t, energy.Total.Usage, 50)
	requireCategoryNumber(t, root.Usage, 50)
	requireCategoryNumber(t, root.Children[0].Usage, 25)
	requireCategoryNumber(t, root.Children[1].Usage, 25)
	requireCategoryNumber(t, report.Intervals[0].Trace.Usage, 50)
	requireCategoryNumber(t, report.Intervals[1].Trace.Usage, 0)
	if energy.Unclassified.Coverage.Expected != 0 || energy.Unclassified.Usage != nil ||
		energy.Total.Coverage != (WeightedCoverage{Expected: 1, Valid: 1}) ||
		root.Coverage != energy.Total.Coverage {
		t.Fatalf("scoped quality or balance incorrect: %+v", energy)
	}
}

func TestM1CCategoriesRollUpOnceAndKeepEnergyAndQualitySeparate(t *testing.T) {
	e1 := categoryFixture(t, Electricity, "kWh", "e1", 100)
	e2 := categoryFixture(t, Electricity, "Wh", "e2", 0, 0)
	w1 := categoryFixture(t, Water, "L", "w1", 3000, 0)
	points := []CategoryPointResult{e1, e2, w1}
	query, catalog, topology := categoryInputs(points...)
	catalog.Bindings = []CategoryBinding{
		categoryAssignment(e1, CategoryAllocation{CategoryID: "office", WeightPPM: 250_000}, CategoryAllocation{CategoryID: "lighting", WeightPPM: 500_000}),
		categoryAssignment(e2, CategoryAllocation{CategoryID: "lighting", WeightPPM: FullWeightPPM}),
	}
	report, err := CalculateClassifiedUsage(query, catalog, topology, points)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Energy) != 2 || report.Energy[0].Energy != Electricity || report.Energy[0].Unit != "kWh" ||
		report.Energy[1].Energy != Water || report.Energy[1].Unit != "m³" {
		t.Fatalf("energy or units mixed: %+v", report.Energy)
	}
	power, water := report.Energy[0], report.Energy[1]
	bld := power.Categories[0]
	if bld.Category.ID != "building" || len(bld.Children) != 2 || bld.Children[1].Children[0].Category.ID != "lighting" {
		t.Fatalf("wrong tree: %+v", bld)
	}
	requireCategoryNumber(t, power.Total.Usage, 100)
	requireCategoryNumber(t, bld.Usage, 75)
	requireCategoryNumber(t, bld.Children[0].Usage, 25)
	requireCategoryNumber(t, bld.Children[1].Children[0].Usage, 50)
	requireCategoryNumber(t, power.Unclassified.Usage, 25)
	requireCategoryNumber(t, water.Unclassified.Usage, 3)
	if got := power.Total.Coverage; got != (WeightedCoverage{Expected: 4, Valid: 3, Missing: 1}) {
		t.Fatalf("energy coverage counted more than once: %+v", got)
	}
	if got := bld.Coverage; got != (WeightedCoverage{Expected: 3.5, Valid: 2.75, Missing: .75}) {
		t.Fatalf("weighted category coverage: %+v", got)
	}
	if water.Categories[0].Usage != nil || water.Categories[0].Coverage.Expected != 0 {
		t.Fatal("category without input must not display measured zero")
	}
	if len(report.Intervals) != 6 || report.Intervals[0].Point != e1.Mapping.Point ||
		report.Intervals[0].MappingGeneration != 1 || report.Intervals[0].Unit != "kWh" ||
		report.Intervals[0].ProfileVersion != e1.Result.Profile.Meta.Version ||
		report.Intervals[0].CategoryBindingVersion != catalog.Bindings[0].Meta.Version ||
		len(report.Intervals[0].Allocations) != 2 || report.Intervals[1].Trace.Quality != Missing ||
		report.Intervals[1].Trace.Usage != nil || len(report.Intervals[0].Trace.Sources) != 1 {
		t.Fatalf("missing point, category or quality evidence: %+v", report.Intervals)
	}

	slices.Reverse(points)
	slices.Reverse(catalog.Nodes)
	slices.Reverse(catalog.Bindings)
	slices.Reverse(topology.Boundaries)
	replayed, err := CalculateClassifiedUsage(query, catalog, topology, points)
	if err != nil || !reflect.DeepEqual(report, replayed) {
		t.Fatalf("input ordering changed report: err=%v", err)
	}
}

func TestM1CScopedAllocationsFollowAtomicIntervalVersions(t *testing.T) {
	p := categoryFixture(t, Electricity, "kWh", "e1", 100, 200)
	query, catalog, topology := categoryInputs(p)
	scope := ObjectKey{Kind: Space, ID: "floor-a"}
	other := ObjectKey{Kind: Space, ID: "floor-b"}
	query.Object = scope
	boundary := query.Window.From.Add(time.Hour)
	first := versionFixture(query.Window.From)
	first.Effective.To = boundary
	second := versionFixture(boundary)
	second.Version = "v2"
	p.Mapping.Bindings = []BindingRevision{
		{View: Space, Meta: first, Allocations: []Allocation{{Target: scope, WeightPPM: 600_000}}},
		{View: Space, Meta: second, Allocations: []Allocation{{Target: scope, WeightPPM: 400_000}, {Target: other, WeightPPM: 600_000}}},
	}
	catalog.Bindings = []CategoryBinding{
		{Point: p.Mapping.Point, Meta: first, Allocations: []CategoryAllocation{{CategoryID: "office", WeightPPM: 500_000}}},
		{Point: p.Mapping.Point, Meta: second, Allocations: []CategoryAllocation{{CategoryID: "lighting", WeightPPM: FullWeightPPM}}},
	}
	if _, err := CalculateClassifiedUsage(query, catalog, topology, []CategoryPointResult{p}); !errors.Is(err, ErrInvalidClassification) {
		t.Fatalf("object and category weights were combined without a proportional basis: %v", err)
	}
	catalog.ProportionalAcrossObjects = true
	report, err := CalculateClassifiedUsage(query, catalog, topology, []CategoryPointResult{p})
	if err != nil {
		t.Fatal(err)
	}
	got := report.Energy[0]
	requireCategoryNumber(t, got.Total.Usage, 140)
	requireCategoryNumber(t, got.Unclassified.Usage, 30)
	requireCategoryNumber(t, got.Categories[0].Children[0].Usage, 30)
	requireCategoryNumber(t, got.Categories[0].Children[1].Usage, 80)
	if got.Total.Coverage.Valid != 1 {
		t.Fatalf("incorrect scoped coverage: %+v", got.Total.Coverage)
	}
	requireCategoryNumber(t, report.Intervals[0].Trace.Usage, 60)
	requireCategoryNumber(t, report.Intervals[1].Trace.Usage, 80)
	if report.Intervals[0].ObjectBindingVersion != first.Version ||
		report.Intervals[1].ObjectBindingVersion != second.Version ||
		report.Intervals[0].CategoryBindingVersion != first.Version ||
		report.Intervals[1].CategoryBindingVersion != second.Version {
		t.Fatalf("classification evidence uses the wrong effective revisions: %+v", report.Intervals)
	}
	catalog.Bindings[0].Meta.Effective.To = boundary.Add(-time.Minute)
	if _, err := CalculateClassifiedUsage(query, catalog, topology, []CategoryPointResult{p}); !errors.Is(err, ErrMappingBoundary) {
		t.Fatalf("mid-interval classification was not rejected: %v", err)
	}
}

func TestM1CValidZeroAndMissingRemainDistinct(t *testing.T) {
	p := categoryFixture(t, Electricity, "kWh", "zero", 0)
	query, catalog, topology := categoryInputs(p)
	catalog.Bindings = []CategoryBinding{categoryAssignment(p, CategoryAllocation{CategoryID: "lighting", WeightPPM: 500_000})}
	report, err := CalculateClassifiedUsage(query, catalog, topology, []CategoryPointResult{p})
	if err != nil {
		t.Fatal(err)
	}
	row := report.Energy[0]
	requireCategoryNumber(t, row.Total.Usage, 0)
	requireCategoryNumber(t, row.Categories[0].Usage, 0)
	requireCategoryNumber(t, row.Unclassified.Usage, 0)
	if row.Total.Coverage.Missing != 1 || row.Categories[0].Children[0].Usage != nil {
		t.Fatalf("zero/missing collapsed: %+v", row)
	}
}

func TestM1CDisjointSubmetersAndAggregateOverflow(t *testing.T) {
	p1 := categoryFixture(t, Electricity, "kWh", "e1", 40)
	p2 := categoryFixture(t, Electricity, "kWh", "e2", 60)
	query, catalog, topology := categoryInputs(p1, p2)
	first, second := topology.Boundaries[0][0], topology.Boundaries[1][0]
	first.Role, second.Role = BoundarySubmeter, BoundarySubmeter
	topology.Boundaries = [][]CategoryBoundaryContributor{{first, second}}
	report, err := CalculateClassifiedUsage(query, catalog, topology, []CategoryPointResult{p1, p2})
	if err != nil {
		t.Fatal(err)
	}
	requireCategoryNumber(t, report.Energy[0].Total.Usage, 100)
	p1 = categoryFixture(t, Electricity, "kWh", "e1", math.MaxFloat64)
	p2 = categoryFixture(t, Electricity, "kWh", "e2", math.MaxFloat64)
	if _, err := CalculateClassifiedUsage(query, catalog, topology, []CategoryPointResult{p1, p2}); !errors.Is(err, ErrInvalidClassification) {
		t.Fatalf("aggregate overflow was not rejected: %v", err)
	}
}

func TestM1CRejectsAmbiguousOrMalformedInputs(t *testing.T) {
	type change func(*ClassifiedUsageQuery, *ClassificationSnapshot, *CategoryTopologySnapshot, *[]CategoryPointResult)
	cases := []struct {
		name   string
		change change
	}{
		{"tree cycle", func(_ *ClassifiedUsageQuery, c *ClassificationSnapshot, _ *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			c.Nodes[0].ParentID = "lighting"
		}},
		{"missing parent", func(_ *ClassifiedUsageQuery, c *ClassificationSnapshot, _ *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			c.Nodes[1].ParentID = "unknown"
		}},
		{"duplicate node", func(_ *ClassifiedUsageQuery, c *ClassificationSnapshot, _ *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			c.Nodes = append(c.Nodes, c.Nodes[0])
		}},
		{"mixed tree energy", func(_ *ClassifiedUsageQuery, c *ClassificationSnapshot, _ *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			c.Nodes[1].Energy = Heat
		}},
		{"parent allocation", func(_ *ClassifiedUsageQuery, c *ClassificationSnapshot, _ *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			c.Bindings[0].Allocations[0].CategoryID = "building"
		}},
		{"wrong energy assignment", func(_ *ClassifiedUsageQuery, c *ClassificationSnapshot, _ *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			c.Bindings[0].Allocations[0].CategoryID = "water"
		}},
		{"overweight allocation", func(_ *ClassifiedUsageQuery, c *ClassificationSnapshot, _ *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			c.Bindings[0].Allocations[0].WeightPPM = FullWeightPPM + 1
		}},
		{"duplicate allocation", func(_ *ClassifiedUsageQuery, c *ClassificationSnapshot, _ *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			c.Bindings[0].Allocations = append(c.Bindings[0].Allocations, c.Bindings[0].Allocations[0])
		}},
		{"overlapping assignment", func(_ *ClassifiedUsageQuery, c *ClassificationSnapshot, _ *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			c.Bindings = append(c.Bindings, c.Bindings[0])
			c.Bindings[1].Meta.Version = "overlap"
		}},
		{"total and submeter", func(_ *ClassifiedUsageQuery, _ *ClassificationSnapshot, p *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			p.Boundaries[1][0].Role = BoundarySubmeter
			p.Boundaries = [][]CategoryBoundaryContributor{append(p.Boundaries[0], p.Boundaries[1]...)}
		}},
		{"repeated topology point", func(_ *ClassifiedUsageQuery, _ *ClassificationSnapshot, p *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			p.Boundaries = append(p.Boundaries, p.Boundaries[0])
		}},
		{"repeated results", func(_ *ClassifiedUsageQuery, _ *ClassificationSnapshot, _ *CategoryTopologySnapshot, p *[]CategoryPointResult) {
			*p = append(*p, (*p)[0])
		}},
		{"aliased physical channel", func(_ *ClassifiedUsageQuery, _ *ClassificationSnapshot, topology *CategoryTopologySnapshot, p *[]CategoryPointResult) {
			(*p)[1].Mapping.Profiles[0].Channel = (*p)[0].Mapping.Profiles[0].Channel
			topology.Boundaries[1][0].Channel = topology.Boundaries[0][0].Channel
		}},
		{"mapping generation changed after topology selection", func(_ *ClassifiedUsageQuery, _ *ClassificationSnapshot, _ *CategoryTopologySnapshot, p *[]CategoryPointResult) {
			(*p)[1].Mapping.Generation++
		}},
		{"physical channel changed after topology selection", func(_ *ClassifiedUsageQuery, _ *ClassificationSnapshot, _ *CategoryTopologySnapshot, p *[]CategoryPointResult) {
			(*p)[1].Mapping.Profiles[0].Channel.ID = "replacement"
		}},
		{"missing topology point result", func(_ *ClassifiedUsageQuery, _ *ClassificationSnapshot, _ *CategoryTopologySnapshot, p *[]CategoryPointResult) {
			*p = (*p)[:1]
		}},
		{"foreign project", func(_ *ClassifiedUsageQuery, _ *ClassificationSnapshot, p *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			p.Project.ProjectID = "other"
		}},
		{"different as-of", func(_ *ClassifiedUsageQuery, _ *ClassificationSnapshot, _ *CategoryTopologySnapshot, p *[]CategoryPointResult) {
			(*p)[1].Result.Window.AsOf = (*p)[1].Result.Window.AsOf.Add(time.Hour)
		}},
		{"expired catalog", func(q *ClassifiedUsageQuery, c *ClassificationSnapshot, _ *CategoryTopologySnapshot, _ *[]CategoryPointResult) {
			c.Meta.Effective.To = q.Window.From.Add(time.Minute)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			points := []CategoryPointResult{categoryFixture(t, Electricity, "kWh", "e1", 12), categoryFixture(t, Electricity, "Wh", "e2", 3000)}
			q, c, p := categoryInputs(points...)
			c.Bindings = []CategoryBinding{categoryAssignment(points[0], CategoryAllocation{CategoryID: "office", WeightPPM: 300_000})}
			tc.change(&q, &c, &p, &points)
			if _, err := CalculateClassifiedUsage(q, c, p, points); err == nil {
				t.Fatal("ambiguous input produced a report")
			}
		})
	}
}
