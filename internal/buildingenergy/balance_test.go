package buildingenergy

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"
)

func balanceFixture(t *testing.T) (BalanceQuery, BalanceTopology, []CategoryPointResult) {
	t.Helper()
	parent := categoryFixture(t, Electricity, "kWh", "meter-parent", 50, 50)
	east := categoryFixture(t, Electricity, "Wh", "meter-east", 35_000, 35_000)
	west := categoryFixture(t, Electricity, "kWh", "meter-west", 10, 10)
	object := ObjectKey{Kind: Building, ID: "building-a"}
	window := parent.Result.Window
	meta := versionFixture(window.From)
	meta.Effective.To = window.To
	query := BalanceQuery{Project: parent.Mapping.Point.Project, Object: object, Energy: Electricity, Window: window}
	inputs := []CategoryPointResult{parent, east, west}
	for i := range inputs {
		inputs[i].Mapping.Bindings = []BindingRevision{{
			View: Building, Meta: meta, Allocations: []Allocation{{Target: object, WeightPPM: FullWeightPPM}},
		}}
	}
	meter := func(input CategoryPointResult, name string, zones ...string) BalanceMeter {
		return BalanceMeter{Point: input.Mapping.Point, Channel: input.Mapping.Profiles[0].Channel,
			MappingGeneration: input.Mapping.Generation, Name: name, Zones: zones}
	}
	topology := BalanceTopology{
		Project: query.Project, Object: object, Energy: Electricity, Unit: "kWh",
		Meta: VersionMeta{Version: "single-line-v1", Effective: window.Range,
			Source: SourceRef{System: "test-physical-manifest", ID: "building-a-electricity"}},
		Parent:   meter(parent, "Main incomer", "east", "west", "common"),
		Children: []BalanceMeter{meter(east, "East", "east"), meter(west, "West", "west")},
	}
	for i := range topology.Children {
		topology.Children[i].Parent = topology.Parent.Point
	}
	return query, topology, inputs
}

func recalculateBalanceInput(t *testing.T, input *CategoryPointResult, window Window, samples ...Sample) {
	t.Helper()
	result, err := Calculate(MeteringData{Profile: input.Result.Profile, Samples: samples}, window)
	if err != nil {
		t.Fatal(err)
	}
	input.Result = result
}

func TestF09BalanceSeparatesParentFromChildTotals(t *testing.T) {
	query, topology, input := balanceFixture(t)
	report, err := CalculateBalance(query, topology, input)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "unallocated" || report.Unit != "kWh" || report.Topology != topology.Meta || report.RuleVersion == "" {
		t.Fatalf("status or proof missing: %+v", report)
	}
	requireCategoryNumber(t, report.Parent.Usage, 100)
	requireCategoryNumber(t, report.Children[0].Usage, 70)
	requireCategoryNumber(t, report.Children[1].Usage, 20)
	requireCategoryNumber(t, report.ChildrenUsage, 90)
	requireCategoryNumber(t, report.Difference, 10)
	requireCategoryNumber(t, report.DifferenceRate, 0.10)
	// Source traces still have normalized quantities and remain linked to the
	// selected measurement and object mapping.
	east := report.Children[0]
	if east.Coverage != (Coverage{Expected: 2, Valid: 2}) || len(east.Intervals) != 2 ||
		east.Meter.Channel != topology.Children[0].Channel || east.Meter.MappingGeneration != input[1].Mapping.Generation ||
		east.ProfileVersion != input[1].Result.Profile.Meta.Version ||
		east.BindingVersion != input[1].Mapping.Bindings[0].Meta.Version || east.MeterRuleVersion != calculationVersion ||
		len(east.Intervals[0].Sources) != 1 {
		t.Fatalf("meter lineage or quality missing: %+v", east)
	}
	requireCategoryNumber(t, east.Intervals[0].Usage, 35)
	slices.Reverse(input)
	replayed, err := CalculateBalance(query, topology, input)
	if err != nil || !reflect.DeepEqual(replayed, report) {
		t.Fatalf("result changed with input order: %v", err)
	}
}

func TestF09BalancePreservesSignAndZero(t *testing.T) {
	cases := []struct {
		name             string
		parent, east     float64
		west, difference float64
		rate             *float64
		status           BalanceStatus
	}{
		{name: "balanced", parent: 100, east: 80, west: 20, difference: 0, rate: new(0.0), status: "balanced"},
		{name: "negative", parent: 100, east: 85, west: 20, difference: -5, rate: new(-0.05), status: "negative_difference"},
		{name: "zero parent", parent: 0, east: 10, west: 0, difference: -10, status: "negative_difference"},
		{name: "known zero", parent: 0, east: 0, west: 0, difference: 0, status: "balanced"},
		{name: "roundoff", parent: 0.3, east: 0.1, west: 0.2, difference: 0, rate: new(0.0), status: "balanced"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, topology, input := balanceFixture(t)
			start := query.Window.From
			for i, amount := range []float64{tc.parent, tc.east * 1000, tc.west} {
				recalculateBalanceInput(t, &input[i], query.Window,
					intervalFixture(start, amount, "first"), intervalFixture(start.Add(time.Hour), 0, "second"))
			}
			report, err := CalculateBalance(query, topology, input)
			if err != nil || report.Status != tc.status {
				t.Fatalf("want %q, got %q, %v", tc.status, report.Status, err)
			}
			requireCategoryNumber(t, report.Parent.Usage, tc.parent)
			requireCategoryNumber(t, report.Difference, tc.difference)
			if tc.rate == nil {
				if report.DifferenceRate != nil {
					t.Fatalf("zero parent yielded a difference rate %v", *report.DifferenceRate)
				}
			} else {
				requireCategoryNumber(t, report.DifferenceRate, *tc.rate)
			}
		})
	}
}

func TestF09BalanceWithholdsIncompleteOrMisalignedDifference(t *testing.T) {
	cases := []struct {
		name     string
		status   BalanceStatus
		coverage Coverage
		reason   Reason
		usage    *float64
		alter    func(*testing.T, BalanceQuery, []CategoryPointResult)
	}{
		{name: "missing", status: "missing_data", coverage: Coverage{Expected: 2, Missing: 2}, reason: MissingSample,
			alter: func(t *testing.T, q BalanceQuery, points []CategoryPointResult) {
				recalculateBalanceInput(t, &points[2], q.Window)
			}},
		{name: "partial", status: "partial", coverage: Coverage{Expected: 2, Valid: 1, Missing: 1}, reason: MissingSample, usage: new(10.0),
			alter: func(t *testing.T, q BalanceQuery, points []CategoryPointResult) {
				recalculateBalanceInput(t, &points[2], q.Window, intervalFixture(q.Window.From, 10, "known"))
			}},
		{name: "suspect", status: "suspect_data", coverage: Coverage{Expected: 2, Valid: 1, Suspect: 1}, reason: SourceQuality, usage: new(10.0),
			alter: func(t *testing.T, q BalanceQuery, points []CategoryPointResult) {
				bad := intervalFixture(q.Window.From.Add(time.Hour), 90, "suspect")
				bad.Quality = Suspect
				recalculateBalanceInput(t, &points[2], q.Window, intervalFixture(q.Window.From, 10, "known"), bad)
			}},
		{name: "replacement", status: "meter_change", coverage: Coverage{Expected: 2, Valid: 1, Suspect: 1}, reason: EpochChange, usage: new(10.0),
			alter: func(t *testing.T, q BalanceQuery, points []CategoryPointResult) {
				profile := &points[2].Result.Profile
				profile.Kind = Cumulative
				points[2].Mapping.Profiles[0].Profile = *profile
				points[2].Mapping.Profiles[0].RegisterEpoch = "initial"
				samples := []Sample{
					readFixture(q.Window.From, 200, "initial-register"),
					readFixture(q.Window.From.Add(time.Hour), 210, "last-old-register"),
					readFixture(q.Window.To, 6, "replacement-register"),
				}
				samples[0].Epoch, samples[1].Epoch, samples[2].Epoch = "initial", "initial", "replacement"
				recalculateBalanceInput(t, &points[2], q.Window, samples...)
			}},
		{name: "shortened range", status: "time_mismatch",
			alter: func(t *testing.T, q BalanceQuery, points []CategoryPointResult) {
				q.Window.To = q.Window.From.Add(time.Hour)
				recalculateBalanceInput(t, &points[2], q.Window, intervalFixture(q.Window.From, 10, "outside-comparison"))
			}},
		{name: "different cutoff", status: "time_mismatch",
			alter: func(t *testing.T, q BalanceQuery, points []CategoryPointResult) {
				q.Window.AsOf = q.Window.AsOf.Add(time.Minute)
				recalculateBalanceInput(t, &points[2], q.Window, intervalFixture(q.Window.From, 10, "later-snapshot"))
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, topology, points := balanceFixture(t)
			tc.alter(t, query, points)
			report, err := CalculateBalance(query, topology, points)
			if err != nil {
				t.Fatal(err)
			}
			meter := report.Children[1]
			if report.Status != tc.status || meter.Status != tc.status || meter.Coverage != tc.coverage ||
				report.ChildrenUsage != nil || report.Difference != nil || report.DifferenceRate != nil {
				t.Fatalf("unsafe difference or unexpected status: %+v", report)
			}
			if tc.usage == nil {
				if meter.Usage != nil {
					t.Fatal("unavailable quantity turned into a measured value")
				}
			} else {
				requireCategoryNumber(t, meter.Usage, *tc.usage)
			}
			if tc.reason != "" && !slices.Contains(meter.Reasons, tc.reason) {
				t.Fatalf("meter reason lost: %v", meter.Reasons)
			}
			if tc.status == "time_mismatch" && len(meter.Intervals) != 0 {
				t.Fatal("unaligned readings leaked into the result")
			}
			requireCategoryNumber(t, report.Parent.Usage, 100)
		})
	}
}

func TestF09BalanceRejectsInvalidTopologyAndMeterScope(t *testing.T) {
	cases := []struct {
		name  string
		alter func(*BalanceTopology, []CategoryPointResult)
	}{
		{"missing proof source", func(top *BalanceTopology, _ []CategoryPointResult) { top.Meta.Source = SourceRef{} }},
		{"expired revision", func(top *BalanceTopology, _ []CategoryPointResult) {
			top.Meta.Effective.To = top.Meta.Effective.To.Add(-time.Second)
		}},
		{"ambiguous child footprint", func(top *BalanceTopology, _ []CategoryPointResult) { top.Children[1].Zones = nil }},
		{"shared sibling zone on distinct channels", func(top *BalanceTopology, _ []CategoryPointResult) { top.Children[1].Zones = []string{"east", "west"} }},
		{"duplicate zone on one child", func(top *BalanceTopology, _ []CategoryPointResult) { top.Children[1].Zones = []string{"west", "west"} }},
		{"child zone outside parent", func(top *BalanceTopology, _ []CategoryPointResult) { top.Children[1].Zones = []string{"unrelated"} }},
		{"wrong parent", func(top *BalanceTopology, _ []CategoryPointResult) { top.Children[1].Parent = top.Children[0].Point }},
		{"parent repeated as child", func(top *BalanceTopology, _ []CategoryPointResult) { top.Children[1].Point = top.Parent.Point }},
		{"channel alias", func(top *BalanceTopology, _ []CategoryPointResult) { top.Children[1].Channel = top.Children[0].Channel }},
		{"unexpected channel", func(top *BalanceTopology, _ []CategoryPointResult) { top.Children[1].Channel.ID = "unmatched" }},
		{"mapping version changed", func(top *BalanceTopology, _ []CategoryPointResult) { top.Children[1].MappingGeneration++ }},
		{"noncanonical unit", func(top *BalanceTopology, _ []CategoryPointResult) { top.Unit = "Wh" }},
		{"different energy", func(_ *BalanceTopology, points []CategoryPointResult) { points[2].Result.Profile.Energy = Water }},
		{"duplicate result", func(_ *BalanceTopology, points []CategoryPointResult) { points[2] = points[1] }},
		{"out-of-building point", func(_ *BalanceTopology, points []CategoryPointResult) {
			points[2].Mapping.Bindings[0].Allocations[0].Target.ID = "building-other"
		}},
		{"partial allocation", func(_ *BalanceTopology, points []CategoryPointResult) {
			points[2].Mapping.Bindings[0].Allocations[0].WeightPPM = 500_000
		}},
		{"omitted interval", func(_ *BalanceTopology, points []CategoryPointResult) {
			points[2].Result.Intervals = points[2].Result.Intervals[:1]
		}},
		{"trace with no source", func(_ *BalanceTopology, points []CategoryPointResult) {
			points[2].Result.Intervals[0].Sources = nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, topology, points := balanceFixture(t)
			tc.alter(&topology, points)
			report, err := CalculateBalance(query, topology, points)
			if !errors.Is(err, ErrInvalidBalance) || !reflect.DeepEqual(report, BalanceReport{}) {
				t.Fatalf("want rejected proof or input, got report=%+v err=%v", report, err)
			}
		})
	}

	query, topology, points := balanceFixture(t)
	for _, candidate := range [][]CategoryPointResult{points[:2], append(points, points[0])} {
		t.Run(fmt.Sprintf("%d supplied results", len(candidate)), func(t *testing.T) {
			if _, err := CalculateBalance(query, topology, candidate); !errors.Is(err, ErrInvalidBalance) {
				t.Fatalf("invalid input count accepted: %v", err)
			}
		})
	}
}
