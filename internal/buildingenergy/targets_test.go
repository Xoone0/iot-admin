package buildingenergy

import (
	"errors"
	"testing"
	"time"
)

func targetFixture(t *testing.T) (TargetScope, TargetVersion, time.Time) {
	t.Helper()
	scope := TargetScope{
		Project: ProjectKey{TenantID: "target-test", ProjectID: "commercial"},
		Object:  ObjectKey{Kind: Building, ID: "office"},
		Energy:  Electricity, Unit: "kWh", Year: 2024, Timezone: "America/New_York",
	}
	location, err := time.LoadLocation(scope.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, location)
	version := TargetVersion{
		Scope: scope, AnnualMilli: 120_005, Method: TargetLinear,
		Meta: VersionMeta{
			Version: "plan-v1", Source: SourceRef{System: "approved-fixture", ID: "office"},
			Effective: Range{From: start, To: start.AddDate(1, 0, 0)},
		},
	}
	return scope, version, start.AddDate(0, 6, 0)
}

func TestF05F14TargetNaturalCalendarAndExactAllocation(t *testing.T) {
	scope, version, at := targetFixture(t)
	t.Run("linear rounds deterministically and conserves the annual total", func(t *testing.T) {
		plan, err := PlanEnergyTarget(scope, at, []TargetVersion{version})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Version == nil || plan.Version.Meta.Version != "plan-v1" || plan.Annual.Status != TargetReady ||
			!sameRange(plan.Annual.Range, version.Meta.Effective) || *plan.Annual.TargetMilli != 120_005 {
			t.Fatalf("annual target: %+v", plan)
		}
		sum := int64(0)
		for i, month := range plan.Months {
			want := int64(10_000)
			if i < 5 {
				want++
			}
			if month.Status != TargetReady || month.From.Month() != time.Month(i+1) || *month.TargetMilli != want {
				t.Fatalf("month %d: %+v, want %d milli-units", i+1, month, want)
			}
			sum += *month.TargetMilli
		}
		if sum != version.AnnualMilli || plan.Months[1].To.Sub(plan.Months[1].From) != 29*24*time.Hour ||
			plan.Months[2].To.Sub(plan.Months[2].From) != (31*24-1)*time.Hour {
			t.Fatalf("annual sum %d or project-local leap/DST boundary is incorrect", sum)
		}
	})
	t.Run("explicit month weights support nonuniform seasons", func(t *testing.T) {
		version.Method = TargetWeighted
		version.AnnualMilli = 100_001
		version.WeightPPM = [12]int64{0: 250_000, 6: 500_000, 11: 250_000}
		plan, err := PlanEnergyTarget(scope, at, []TargetVersion{version})
		if err != nil {
			t.Fatal(err)
		}
		want := [12]int64{0: 25_000, 6: 50_001, 11: 25_000}
		for i, month := range plan.Months {
			if *month.TargetMilli != want[i] {
				t.Errorf("month %d: %d, want %d", i+1, *month.TargetMilli, want[i])
			}
		}
	})
}

func TestF05F14TargetEffectiveVersions(t *testing.T) {
	scope, version, at := targetFixture(t)
	earlier, later := version, version
	earlier.Meta.Effective.To = at
	later.Meta.Effective.From = at
	later.Meta.Version = "plan-v2"
	later.AnnualMilli = 240_000
	for _, tc := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"old revision", at.Add(-time.Nanosecond), "plan-v1"},
		{"new revision starts at half-open boundary", at, "plan-v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanEnergyTarget(scope, tc.at, []TargetVersion{later, earlier})
			if err != nil || plan.Version == nil || plan.Version.Meta.Version != tc.want ||
				plan.Annual.TargetMilli != nil || plan.Annual.Status != TargetOutsideEffectivePeriod {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
		})
	}
	t.Run("only fully effective months receive a target", func(t *testing.T) {
		later.Meta.Effective.From = at.AddDate(0, 0, 10)
		plan, err := PlanEnergyTarget(scope, later.Meta.Effective.From, []TargetVersion{earlier, later})
		if err != nil || plan.Months[6].Status != TargetOutsideEffectivePeriod ||
			plan.Months[6].TargetMilli != nil || plan.Months[7].Status != TargetReady ||
			*plan.Months[7].TargetMilli != 20_000 || plan.Version.AnnualMilli != 240_000 {
			t.Fatalf("mid-month revision: %+v err=%v", plan, err)
		}
	})
	t.Run("adjacent scopes do not fill a missing effective version", func(t *testing.T) {
		other := version
		other.Scope.Object.ID = "another-building"
		plan, err := PlanEnergyTarget(scope, at, []TargetVersion{earlier, other})
		if err != nil || plan.Version != nil || plan.Annual.Status != TargetUnconfigured || plan.Months[0].TargetMilli != nil {
			t.Fatalf("missing target: %+v err=%v", plan, err)
		}
	})
	t.Run("overlap is invalid including away from the requested instant", func(t *testing.T) {
		later.Meta.Effective.From = at.Add(-time.Hour)
		_, err := PlanEnergyTarget(scope, at.AddDate(0, -5, 0), []TargetVersion{earlier, later})
		if !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("overlap: %v", err)
		}
	})
}

func TestF05F14TargetRejectsInvalidPlans(t *testing.T) {
	scope, version, at := targetFixture(t)
	for _, tc := range []struct {
		name   string
		change func(*TargetScope, *TargetVersion, *time.Time)
	}{
		{"invalid project", func(s *TargetScope, _ *TargetVersion, _ *time.Time) { s.Project.ProjectID = "" }},
		{"invalid object", func(s *TargetScope, _ *TargetVersion, _ *time.Time) { s.Object.Kind = "floor" }},
		{"incompatible canonical unit", func(s *TargetScope, _ *TargetVersion, _ *time.Time) { s.Unit = "Wh" }},
		{"invalid year", func(s *TargetScope, _ *TargetVersion, _ *time.Time) { s.Year = 9999 }},
		{"ambiguous timezone", func(s *TargetScope, _ *TargetVersion, _ *time.Time) { s.Timezone = "Local" }},
		{"mismatched version timezone", func(_ *TargetScope, v *TargetVersion, _ *time.Time) { v.Scope.Timezone = "UTC" }},
		{"as-of outside year", func(_ *TargetScope, v *TargetVersion, a *time.Time) { *a = v.Meta.Effective.To }},
		{"missing version", func(_ *TargetScope, v *TargetVersion, _ *time.Time) { v.Meta.Version = "" }},
		{"missing source", func(_ *TargetScope, v *TargetVersion, _ *time.Time) { v.Meta.Source.ID = "" }},
		{"open effective end", func(_ *TargetScope, v *TargetVersion, _ *time.Time) { v.Meta.Effective.To = time.Time{} }},
		{"empty effective interval", func(_ *TargetScope, v *TargetVersion, _ *time.Time) {
			v.Meta.Effective.To = v.Meta.Effective.From
		}},
		{"effective range outside natural year", func(_ *TargetScope, v *TargetVersion, _ *time.Time) {
			v.Meta.Effective.From = v.Meta.Effective.From.AddDate(0, 0, -1)
		}},
		{"negative target", func(_ *TargetScope, v *TargetVersion, _ *time.Time) { v.AnnualMilli = -1 }},
		{"unrepresentable target", func(_ *TargetScope, v *TargetVersion, _ *time.Time) { v.AnnualMilli = 1 << 53 }},
		{"linear method cannot carry weights", func(_ *TargetScope, v *TargetVersion, _ *time.Time) { v.WeightPPM[0] = 1 }},
		{"incorrect seasonal sum", func(_ *TargetScope, v *TargetVersion, _ *time.Time) {
			v.Method, v.WeightPPM[0] = TargetWeighted, 999_999
		}},
		{"negative seasonal weight despite correct sum", func(_ *TargetScope, v *TargetVersion, _ *time.Time) {
			v.Method, v.WeightPPM = TargetWeighted, [12]int64{1_000_000, 1, -1}
		}},
		{"unsupported method", func(_ *TargetScope, v *TargetVersion, _ *time.Time) { v.Method = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, candidate, when := scope, version, at
			tc.change(&selected, &candidate, &when)
			_, err := PlanEnergyTarget(selected, when, []TargetVersion{candidate})
			if !errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("got %v, want ErrInvalidTarget", err)
			}
		})
	}
}

func targetActual(scope TargetScope, window Range, usage float64, coverage WeightedCoverage) ClassifiedUsageReport {
	return ClassifiedUsageReport{
		Query: ClassifiedUsageQuery{
			Project: scope.Project, Object: scope.Object,
			Window: Window{Range: window, Timezone: scope.Timezone, Granularity: Month, AsOf: window.To},
		},
		RuleVersion: classificationRuleVersion,
		Energy:      []EnergyCategorySummary{{Energy: scope.Energy, Unit: scope.Unit, Total: CategoryValue{Usage: &usage, Coverage: coverage}}},
	}
}

func TestF05F14CompareOnlyExactScopedAndCompleteResults(t *testing.T) {
	scope, version, at := targetFixture(t)
	version.AnnualMilli = 120_000
	plan, err := PlanEnergyTarget(scope, at, []TargetVersion{version})
	if err != nil {
		t.Fatal(err)
	}
	complete := WeightedCoverage{Expected: 4, Valid: 4}
	t.Run("rejects unplanned scopes and malformed planned periods", func(t *testing.T) {
		invalid := plan
		invalid.Annual.TargetMilli = nil
		for _, input := range []TargetPlan{{}, invalid} {
			if _, err := CompareEnergyTarget(input, 0, nil); !errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("invalid plan returned %v", err)
			}
		}
		if _, err := CompareEnergyTarget(plan, 13, nil); !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("invalid month returned %v", err)
		}
	})
	t.Run("closed annual and prior month targets compare independent windows", func(t *testing.T) {
		for _, tc := range []struct {
			month              time.Month
			window             Range
			usage, delta, rate float64
		}{
			{0, plan.Annual.Range, 90, -30, .75},
			{time.June, plan.Months[5].Range, 15, 5, 1.5},
			{time.July, plan.Months[6].Range, 0, -10, 0},
		} {
			report := targetActual(scope, tc.window, tc.usage, complete)
			got, err := CompareEnergyTarget(plan, tc.month, &report)
			if err != nil || got.Status != TargetComparable || got.Usage == nil || *got.Usage != tc.usage ||
				got.Delta == nil || *got.Delta != tc.delta || got.Ratio == nil || *got.Ratio != tc.rate {
				t.Fatalf("month %d: got %+v err=%v", tc.month, got, err)
			}
		}
	})
	t.Run("zero target keeps absolute difference only", func(t *testing.T) {
		version := version
		version.AnnualMilli = 0
		zero, err := PlanEnergyTarget(scope, at, []TargetVersion{version})
		if err != nil {
			t.Fatal(err)
		}
		report := targetActual(scope, zero.Annual.Range, 3, complete)
		got, err := CompareEnergyTarget(zero, 0, &report)
		if err != nil || got.Status != TargetZero || got.Ratio != nil || got.Delta == nil || *got.Delta != 3 {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})
	t.Run("actual may be shown without a configured target", func(t *testing.T) {
		blank, err := PlanEnergyTarget(scope, at, nil)
		if err != nil {
			t.Fatal(err)
		}
		report := targetActual(scope, blank.Annual.Range, 90, complete)
		got, err := CompareEnergyTarget(blank, 0, &report)
		if err != nil || got.Status != TargetUnconfigured || got.Usage == nil || *got.Usage != 90 || got.Delta != nil || got.Ratio != nil {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})
	for _, tc := range []struct {
		name   string
		status TargetStatus
		change func(*ClassifiedUsageReport)
		actual bool
	}{
		{"annual target with a monthly measurement", TargetIncompletePeriod, func(r *ClassifiedUsageReport) {
			r.Query.Window.Range = plan.Months[5].Range
		}, false},
		{"annual target with same-month prior-year measurement", TargetIncompatibleUsage, func(r *ClassifiedUsageReport) {
			r.Query.Window.Range = Range{r.Query.Window.From.AddDate(-1, 0, 0), r.Query.Window.To.AddDate(-1, 0, 0)}
		}, false},
		{"wrong project", TargetIncompatibleUsage, func(r *ClassifiedUsageReport) { r.Query.Project.ProjectID = "other" }, false},
		{"wrong object", TargetIncompatibleUsage, func(r *ClassifiedUsageReport) { r.Query.Object.ID = "other" }, false},
		{"wrong timezone", TargetIncompatibleUsage, func(r *ClassifiedUsageReport) { r.Query.Window.Timezone = "UTC" }, false},
		{"wrong energy", TargetIncompatibleUsage, func(r *ClassifiedUsageReport) { r.Energy[0].Energy = Water }, false},
		{"wrong unit", TargetIncompatibleUsage, func(r *ClassifiedUsageReport) { r.Energy[0].Unit = "m³" }, false},
		{"unknown metering version", TargetIncompatibleUsage, func(r *ClassifiedUsageReport) {
			r.RuleVersion = ""
		}, false},
		{"future readings excluded", TargetIncompatibleUsage, func(r *ClassifiedUsageReport) {
			r.Query.Window.AsOf = r.Query.Window.To.Add(-time.Hour)
		}, false},
		{"missing count", TargetMissingUsage, func(r *ClassifiedUsageReport) {
			r.Energy[0].Total = CategoryValue{Coverage: WeightedCoverage{Expected: 4, Missing: 4}}
		}, false},
		{"partial coverage", TargetPartialCoverage, func(r *ClassifiedUsageReport) {
			r.Energy[0].Total.Coverage = WeightedCoverage{Expected: 4, Valid: 3, Missing: 1}
		}, true},
		{"suspect usage", TargetPartialCoverage, func(r *ClassifiedUsageReport) {
			r.Energy[0].Total = CategoryValue{Coverage: WeightedCoverage{Expected: 4, Suspect: 4}}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := targetActual(scope, plan.Annual.Range, 90, complete)
			tc.change(&report)
			got, err := CompareEnergyTarget(plan, 0, &report)
			if err != nil || got.Status != tc.status || (got.Usage != nil) != tc.actual || got.Delta != nil || got.Ratio != nil {
				t.Fatalf("got %+v err=%v want %q", got, err, tc.status)
			}
		})
	}
}
