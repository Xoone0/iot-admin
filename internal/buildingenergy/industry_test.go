package buildingenergy

import (
	"math"
	"testing"
	"time"
)

func industryFixture(t *testing.T) (IndustryContext, IndustryTemplate, ClassifiedUsageReport, BusinessDenominator) {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, time.October, 15, 0, 0, 0, 0, loc)
	window := Window{Range: Range{from, from.AddDate(0, 0, 1)}, Timezone: loc.String(), Granularity: Hour, AsOf: from.AddDate(0, 0, 1)}
	scope := IndustryContext{Project: ProjectKey{"t", "project"}, Object: ObjectKey{Building, "hospital-a"}, BuildingType: BuildingHospital, Region: "demo-region-a"}
	meta := VersionMeta{Version: "v1", Effective: Range{from.AddDate(0, 0, -5), from.AddDate(0, 0, 5)}, Source: SourceRef{"synthetic", "test"}}
	usage := 1_536.0
	report := ClassifiedUsageReport{Query: ClassifiedUsageQuery{Project: scope.Project, Object: scope.Object, Window: window},
		RuleVersion: "meter-classification-v1", CatalogVersion: meta, TopologyVersion: meta,
		Energy: []EnergyCategorySummary{{Energy: Electricity, Unit: "kWh",
			Total: CategoryValue{Usage: &usage, Coverage: WeightedCoverage{Expected: 72, Valid: 72}}}},
	}
	template := IndustryTemplate{ID: "hospital-bed-days", Name: "Hospital per bed-day", Meta: meta, BuildingType: scope.BuildingType,
		ObjectKind: Building, Energy: Electricity, Denominator: DenominatorBedDays}
	record := BusinessDenominator{Project: scope.Project, Object: scope.Object, Kind: DenominatorBedDays,
		Value: 128, Unit: "床日", Period: window.Range, Meta: meta}
	return scope, template, report, record
}

func TestF12IndustryTemplateReusesScopedMeteredUsage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		typ       BuildingType
		kind      DenominatorKind
		inputUnit string
		value     float64
		unit      string
		want      float64
	}{
		{"bed-days", BuildingHospital, DenominatorBedDays, "床日", 128, "kWh/床日", 12},
		{"sold room nights", BuildingHotel, DenominatorRoomNights, "间夜", 96, "kWh/间夜", 16},
		{"business revenue", BuildingHospital, DenominatorRevenue, "元", 320_000, "kWh/万元", 48},
		{"student count", BuildingSchool, DenominatorStudents, "人", 256, "kWh/人", 6},
		{"area matches F11", BuildingOffice, DenominatorArea, "m²", 1200, "kWh/m²", 1.28},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, template, report, record := industryFixture(t)
			scope.BuildingType, template.BuildingType = tc.typ, tc.typ
			template.Denominator, record.Kind, record.Unit, record.Value = tc.kind, tc.kind, tc.inputUnit, tc.value
			if tc.kind == DenominatorStudents || tc.kind == DenominatorArea {
				record.Period = record.Meta.Effective
			}
			got := EvaluateIndustryMetric(scope, template, report, &record)
			if got.Status != "ready" || got.Value == nil || *got.Value != tc.want || got.Unit != tc.unit {
				t.Fatalf("metric = %+v, want %v %s", got, tc.want, tc.unit)
			}
			if got.Numerator.Usage == nil || *got.Numerator.Usage != 1536 || got.Numerator.Coverage.Valid != 72 ||
				got.Denominator == nil || got.Denominator.Meta != record.Meta || got.Template.Meta != template.Meta ||
				got.Scope != scope || !sameRange(got.Window.Range, report.Query.Window.Range) {
				t.Fatalf("incomplete evidence: %+v", got)
			}
		})
	}
}

func TestF12IndustryTemplateWithholdsUnqualifiedMetrics(t *testing.T) {
	const missing, invalid, crossed, meters = "missing_denominator", "invalid_denominator", "denominator_out_of_period", "incompatible_usage"
	for _, tc := range []struct {
		name string
		want string
		edit func(*IndustryContext, *IndustryTemplate, *ClassifiedUsageReport, **BusinessDenominator)
	}{
		{"missing", missing, func(_ *IndustryContext, _ *IndustryTemplate, _ *ClassifiedUsageReport, d **BusinessDenominator) {
			*d = nil
		}},
		{"zero denominator", invalid, func(_ *IndustryContext, _ *IndustryTemplate, _ *ClassifiedUsageReport, d **BusinessDenominator) {
			(*d).Value = 0
		}},
		{"negative denominator", invalid, func(_ *IndustryContext, _ *IndustryTemplate, _ *ClassifiedUsageReport, d **BusinessDenominator) {
			(*d).Value = -1
		}},
		{"nonfinite denominator", invalid, func(_ *IndustryContext, _ *IndustryTemplate, _ *ClassifiedUsageReport, d **BusinessDenominator) {
			(*d).Value = math.Inf(1)
		}},
		{"unknown units", invalid, func(_ *IndustryContext, _ *IndustryTemplate, _ *ClassifiedUsageReport, d **BusinessDenominator) {
			(*d).Unit = "天"
		}},
		{"wrong driver", invalid, func(_ *IndustryContext, _ *IndustryTemplate, _ *ClassifiedUsageReport, d **BusinessDenominator) {
			(*d).Kind, (*d).Unit = DenominatorRoomNights, "间夜"
		}},
		{"foreign project", invalid, func(_ *IndustryContext, _ *IndustryTemplate, _ *ClassifiedUsageReport, d **BusinessDenominator) {
			(*d).Project.ProjectID = "foreign"
		}},
		{"foreign building", invalid, func(_ *IndustryContext, _ *IndustryTemplate, _ *ClassifiedUsageReport, d **BusinessDenominator) {
			(*d).Object.ID = "another"
		}},
		{"nonfinite quotient", invalid, func(_ *IndustryContext, _ *IndustryTemplate, _ *ClassifiedUsageReport, d **BusinessDenominator) {
			(*d).Value = math.SmallestNonzeroFloat64
		}},
		{"accumulated denominator includes other days", crossed, func(_ *IndustryContext, _ *IndustryTemplate, _ *ClassifiedUsageReport, d **BusinessDenominator) {
			(*d).Period = (*d).Meta.Effective
		}},
		{"expired denominator revision", crossed, func(_ *IndustryContext, _ *IndustryTemplate, report *ClassifiedUsageReport, d **BusinessDenominator) {
			(*d).Meta.Effective.To = report.Query.Window.From
		}},
		{"wrong building type", "inapplicable_template", func(s *IndustryContext, _ *IndustryTemplate, _ *ClassifiedUsageReport, _ **BusinessDenominator) {
			s.BuildingType = BuildingOffice
		}},
		{"expired template", "inapplicable_template", func(_ *IndustryContext, m *IndustryTemplate, report *ClassifiedUsageReport, _ **BusinessDenominator) {
			m.Meta.Effective.To = report.Query.Window.From
		}},
		{"invalid template", "invalid_template", func(_ *IndustryContext, m *IndustryTemplate, _ *ClassifiedUsageReport, _ **BusinessDenominator) {
			m.Denominator = "invented"
		}},
		{"missing energy", "missing_usage", func(_ *IndustryContext, _ *IndustryTemplate, r *ClassifiedUsageReport, _ **BusinessDenominator) {
			r.Energy = nil
		}},
		{"all readings missing", "missing_usage", func(_ *IndustryContext, _ *IndustryTemplate, r *ClassifiedUsageReport, _ **BusinessDenominator) {
			r.Energy[0].Total = CategoryValue{Coverage: WeightedCoverage{Expected: 72, Missing: 72}}
		}},
		{"partial data", "insufficient_quality", func(_ *IndustryContext, _ *IndustryTemplate, r *ClassifiedUsageReport, _ **BusinessDenominator) {
			r.Energy[0].Total.Coverage = WeightedCoverage{Expected: 72, Valid: 70, Missing: 2}
		}},
		{"suspect data", "insufficient_quality", func(_ *IndustryContext, _ *IndustryTemplate, r *ClassifiedUsageReport, _ **BusinessDenominator) {
			r.Energy[0].Total = CategoryValue{Coverage: WeightedCoverage{Expected: 72, Suspect: 72}}
		}},
		{"different meter object", meters, func(_ *IndustryContext, _ *IndustryTemplate, r *ClassifiedUsageReport, _ **BusinessDenominator) {
			r.Query.Object.ID = "foreign"
		}},
		{"different meter unit", meters, func(_ *IndustryContext, _ *IndustryTemplate, r *ClassifiedUsageReport, _ **BusinessDenominator) {
			r.Energy[0].Unit = "Wh"
		}},
		{"unclipped knowledge cutoff", meters, func(_ *IndustryContext, _ *IndustryTemplate, r *ClassifiedUsageReport, _ **BusinessDenominator) {
			r.Query.Window.AsOf = r.Query.Window.From
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, template, report, record := industryFixture(t)
			denominator := &record
			tc.edit(&scope, &template, &report, &denominator)
			got := EvaluateIndustryMetric(scope, template, report, denominator)
			if string(got.Status) != tc.want || got.Value != nil {
				t.Fatalf("metric status = %q, value %v, want %q and nil value", got.Status, got.Value, tc.want)
			}
			if got.Denominator != nil && (got.Denominator.Object != scope.Object || got.Denominator.Project != scope.Project) {
				t.Fatalf("foreign denominator evidence: %+v", got.Denominator)
			}
		})
	}
}

func industryBenchmark(scope IndustryContext, template IndustryTemplate) BenchmarkRule {
	return BenchmarkRule{ID: "demo-hospital-limit", Name: "Synthetic threshold", Publisher: "Synthetic registry",
		Region: scope.Region, MetricID: template.ID, TemplateVersion: template.Meta.Version, Unit: "kWh/床日",
		Kind: BenchmarkDemo, BuildingType: scope.BuildingType, ObjectKind: scope.Object.Kind, Period: Day,
		Meta: template.Meta, Maximum: 12}
}

func TestF13IndustryBenchmarkApplicabilityAndQuality(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
		edit func(*ClassifiedUsageReport, **BenchmarkRule, **BusinessDenominator)
	}{
		{"below", "within_threshold", func(_ *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) { (*r).Maximum = 13 }},
		{"equal", "within_threshold", func(_ *ClassifiedUsageReport, _ **BenchmarkRule, _ **BusinessDenominator) {}},
		{"above", "exceeds_threshold", func(_ *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) { (*r).Maximum = 11 }},
		{"genuine zero", "within_threshold", func(report *ClassifiedUsageReport, _ **BenchmarkRule, _ **BusinessDenominator) {
			*report.Energy[0].Total.Usage = 0
		}},
		{"absent", "no_standard", func(_ *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) { *r = nil }},
		{"other region", "inapplicable", func(_ *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) { (*r).Region = "different" }},
		{"other building type", "inapplicable", func(_ *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) {
			(*r).BuildingType = BuildingHotel
		}},
		{"out of validity", "inapplicable", func(report *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) {
			(*r).Meta.Effective.From = report.Query.Window.To
		}},
		{"minimum floor condition", "inapplicable", func(_ *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) {
			minimum := 129.0
			(*r).MinimumDenominator = &minimum
		}},
		{"metric period differs", "not_comparable", func(_ *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) { (*r).Period = Month }},
		{"metric unit differs", "not_comparable", func(_ *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) { (*r).Unit = "m³/床日" }},
		{"template revision differs", "not_comparable", func(_ *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) {
			(*r).TemplateVersion = "another"
		}},
		{"internal target refused", "not_comparable", func(_ *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) {
			(*r).Kind = "internal_target"
		}},
		{"untrusted threshold rejected", "not_comparable", func(_ *ClassifiedUsageReport, r **BenchmarkRule, _ **BusinessDenominator) { (*r).Maximum = math.NaN() }},
		{"no denominator", "insufficient_data", func(_ *ClassifiedUsageReport, _ **BenchmarkRule, d **BusinessDenominator) { *d = nil }},
		{"partial energy", "insufficient_data", func(report *ClassifiedUsageReport, _ **BenchmarkRule, _ **BusinessDenominator) {
			report.Energy[0].Total.Coverage = WeightedCoverage{Expected: 72, Valid: 70, Missing: 2}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, template, report, record := industryFixture(t)
			rule, denominator := new(BenchmarkRule), &record
			*rule = industryBenchmark(scope, template)
			tc.edit(&report, &rule, &denominator)
			metric := EvaluateIndustryMetric(scope, template, report, denominator)
			got := EvaluateBenchmark(metric, rule)
			if string(got.Status) != tc.want {
				t.Fatalf("benchmark = %+v, want %s, metric %+v", got, tc.want, metric)
			}
			compared := tc.want == "within_threshold" || tc.want == "exceeds_threshold"
			if (got.Delta != nil) != compared {
				t.Fatalf("unexpected benchmark delta: %+v", got)
			}
			if compared && (*got.Delta != *metric.Value-rule.Maximum || got.Rule == nil || got.Rule.Meta != rule.Meta) {
				t.Fatalf("missing exact comparison evidence: %+v", got)
			}
			if tc.name == "genuine zero" && (*metric.Value != 0 || *got.Delta != -12) {
				t.Fatalf("actual zero not preserved: metric %+v comparison %+v", metric, got)
			}
		})
	}
}

func TestF13IndustryBenchmarkKeepsHistoricalRuleSnapshot(t *testing.T) {
	scope, template, report, record := industryFixture(t)
	metric := EvaluateIndustryMetric(scope, template, report, &record)
	rule := industryBenchmark(scope, template)
	rule.Kind, rule.Meta.Version = BenchmarkExternal, "limit-2026-original"
	rule.Meta.Effective.To = report.Query.Window.To
	minimum := 100.0
	rule.MinimumDenominator = &minimum
	historical := EvaluateBenchmark(metric, &rule)

	rule.Meta.Version = "limit-2026-new"
	rule.Meta.Effective.From = report.Query.Window.To
	rule.Meta.Effective.To = report.Query.Window.To.AddDate(0, 0, 1)
	rule.Maximum = 9
	minimum = 127
	if got := EvaluateBenchmark(metric, &rule); got.Status != "inapplicable" || got.Delta != nil {
		t.Fatalf("new revision applied retroactively: %+v", got)
	}
	if historical.Rule == nil || historical.Rule.Meta.Version != "limit-2026-original" ||
		historical.Rule.Kind != BenchmarkExternal || historical.Rule.Maximum != 12 || *historical.Rule.MinimumDenominator != 100 ||
		historical.Status != "within_threshold" {
		t.Fatalf("historical rule snapshot changed: %+v", historical)
	}
}

func TestF13IndustryBenchmarkRefusesUnavailableMetricEvenWithValue(t *testing.T) {
	scope, template, report, record := industryFixture(t)
	metric := EvaluateIndustryMetric(scope, template, report, &record)
	metric.Status = IndustryMissingDenominator
	rule := industryBenchmark(scope, template)
	got := EvaluateBenchmark(metric, &rule)
	if got.Status != BenchmarkInsufficientData || got.Delta != nil {
		t.Fatalf("unavailable metric compared: %+v", got)
	}
}
