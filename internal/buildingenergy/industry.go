package buildingenergy

const IndustryRuleVersion = "f12-f13-v1"

type BuildingType string

const (
	BuildingOffice     BuildingType = "office"
	BuildingCommercial BuildingType = "commercial"
	BuildingHospital   BuildingType = "hospital"
	BuildingSchool     BuildingType = "school"
	BuildingHotel      BuildingType = "hotel"
)

func (kind BuildingType) valid() bool {
	return kind == BuildingOffice || kind == BuildingCommercial || kind == BuildingHospital || kind == BuildingSchool || kind == BuildingHotel
}

// IndustryContext is the authorized object and its known applicability facts.
// Region may be empty when the caller has no verified region for the object.
type IndustryContext struct {
	Project      ProjectKey
	Object       ObjectKey
	BuildingType BuildingType
	Region       string
}

type DenominatorKind string

const (
	DenominatorBedDays    DenominatorKind = "bed_days"
	DenominatorRoomNights DenominatorKind = "room_nights"
	DenominatorRevenue    DenominatorKind = "revenue"
	DenominatorStudents   DenominatorKind = "students"
	DenominatorArea       DenominatorKind = "area"
	DenominatorPeople     DenominatorKind = "people"
)

type denominatorDefinition struct {
	inputUnit, metricUnit string
	inputPerMetricUnit    float64
	accumulated           bool
}

var denominatorDefinitions = map[DenominatorKind]denominatorDefinition{
	DenominatorBedDays:    {"床日", "床日", 1, true},
	DenominatorRoomNights: {"间夜", "间夜", 1, true},
	DenominatorRevenue:    {"元", "万元", 10_000, true},
	DenominatorStudents:   {"人", "人", 1, false},
	DenominatorArea:       {"m²", "m²", 1, false},
	DenominatorPeople:     {"人", "人", 1, false},
}

// IndustryTemplate selects one normalized energy and one controlled divisor.
// Template revisions can differ by building type without changing the formula.
type IndustryTemplate struct {
	ID, Name     string
	Meta         VersionMeta
	BuildingType BuildingType
	ObjectKind   ObjectKind
	Energy       EnergyType
	Denominator  DenominatorKind
}

// BusinessDenominator is an already authorized record. For accumulated
// quantities Period is the measured interval. For snapshot quantities it
// identifies the interval for which the value is representative.
type BusinessDenominator struct {
	Project ProjectKey
	Object  ObjectKey
	Kind    DenominatorKind
	Value   float64
	Unit    string
	Period  Range
	Meta    VersionMeta
	Basis   string
}

type IndustryMetricStatus string

const (
	IndustryMetricReady            IndustryMetricStatus = "ready"
	IndustryMissingDenominator     IndustryMetricStatus = "missing_denominator"
	IndustryInvalidDenominator     IndustryMetricStatus = "invalid_denominator"
	IndustryDenominatorOutOfPeriod IndustryMetricStatus = "denominator_out_of_period"
	IndustryMissingUsage           IndustryMetricStatus = "missing_usage"
	IndustryInsufficientQuality    IndustryMetricStatus = "insufficient_quality"
	IndustryIncompatibleUsage      IndustryMetricStatus = "incompatible_usage"
	IndustryInvalidTemplate        IndustryMetricStatus = "invalid_template"
	IndustryInapplicableTemplate   IndustryMetricStatus = "inapplicable_template"
)

type IndustryMetric struct {
	Scope       IndustryContext
	Window      Window
	Template    IndustryTemplate
	Numerator   CategoryValue
	Denominator *BusinessDenominator
	Unit        string
	Status      IndustryMetricStatus
	Value       *float64

	ClassificationRuleVersion string
	CatalogVersion            VersionMeta
	TopologyVersion           VersionMeta

	validated bool
}

// EvaluateIndustryMetric normalizes one authorized classified consumption
// result by a record covering the same business scope and period.
func EvaluateIndustryMetric(scope IndustryContext, template IndustryTemplate, report ClassifiedUsageReport, denominator *BusinessDenominator) IndustryMetric {
	metric := IndustryMetric{Scope: scope, Window: report.Query.Window, Template: template}
	definition, ok := denominatorDefinitions[template.Denominator]
	unit := industryEnergyUnit(template.Energy)
	if !ok || unit == "" || !template.BuildingType.valid() || !(ObjectKey{Kind: template.ObjectKind, ID: template.ID}).valid() ||
		!named(template.Name) || !validIndustryRevision(template.Meta) {
		metric.Status = IndustryInvalidTemplate
		return metric
	}
	metric.Unit = unit + "/" + definition.metricUnit
	if !scope.Project.valid() || !scope.Object.valid() || report.Query.Project != scope.Project || report.Query.Object != scope.Object {
		metric.Status = IndustryIncompatibleUsage
		return metric
	}
	window, err := localWindow(report.Query.Window)
	if err != nil || !named(report.RuleVersion) || !report.CatalogVersion.covers(window.Range) ||
		!report.TopologyVersion.covers(window.Range) {
		metric.Status = IndustryIncompatibleUsage
		return metric
	}
	metric.Window = window
	metric.ClassificationRuleVersion, metric.CatalogVersion, metric.TopologyVersion = report.RuleVersion, report.CatalogVersion, report.TopologyVersion
	numerator, usageStatus := industryNumerator(report, template.Energy, unit)
	metric.Numerator = numerator
	if template.BuildingType != scope.BuildingType || template.ObjectKind != scope.Object.Kind || !template.Meta.covers(window.Range) {
		metric.Status = IndustryInapplicableTemplate
		return metric
	}
	metric.Denominator, metric.Status = industryDenominator(scope, template, window.Range, definition, denominator)
	if metric.Status != IndustryMetricReady {
		return metric
	}
	if usageStatus != IndustryMetricReady {
		metric.Status = usageStatus
		return metric
	}
	value := *numerator.Usage / (metric.Denominator.Value / definition.inputPerMetricUnit)
	if !finite(value) {
		metric.Status = IndustryInvalidDenominator
		return metric
	}
	metric.Value, metric.validated = &value, true
	return metric
}

func industryEnergyUnit(energy EnergyType) string {
	switch energy {
	case Electricity:
		return "kWh"
	case Water:
		return "m³"
	case Gas:
		return "Nm³"
	case Cooling, Heat, Steam:
		return "GJ"
	default:
		return ""
	}
}

func validIndustryRevision(meta VersionMeta) bool {
	return named(meta.Version) && meta.Source.valid() && !meta.Effective.From.IsZero() &&
		(meta.Effective.To.IsZero() || meta.Effective.valid())
}

func industryNumerator(report ClassifiedUsageReport, energy EnergyType, unit string) (CategoryValue, IndustryMetricStatus) {
	var value *CategoryValue
	for _, summary := range report.Energy {
		if summary.Energy != energy {
			continue
		}
		if value != nil || summary.Unit != unit {
			return CategoryValue{}, IndustryIncompatibleUsage
		}
		value = &summary.Total
	}
	if value == nil {
		return CategoryValue{}, IndustryMissingUsage
	}
	if value.Usage != nil {
		if !finite(*value.Usage) || *value.Usage < 0 {
			return CategoryValue{}, IndustryIncompatibleUsage
		}
		usage := *value.Usage
		value.Usage = &usage
	}
	if value.Usage == nil && value.Coverage.Suspect == 0 {
		return *value, IndustryMissingUsage
	}
	if !trendComplete(*value) {
		return *value, IndustryInsufficientQuality
	}
	return *value, IndustryMetricReady
}

func industryDenominator(scope IndustryContext, template IndustryTemplate, period Range, definition denominatorDefinition, denominator *BusinessDenominator) (*BusinessDenominator, IndustryMetricStatus) {
	if denominator == nil {
		return nil, IndustryMissingDenominator
	}
	if scope.Project != denominator.Project || scope.Object != denominator.Object || !finite(denominator.Value) {
		return nil, IndustryInvalidDenominator
	}
	record := *denominator
	if record.Kind != template.Denominator || record.Value <= 0 || record.Unit != definition.inputUnit ||
		!validIndustryRevision(record.Meta) || !record.Period.valid() {
		return &record, IndustryInvalidDenominator
	}
	if !record.Meta.covers(period) || period.From.Before(record.Period.From) || period.To.After(record.Period.To) ||
		(definition.accumulated && !sameRange(record.Period, period)) {
		return &record, IndustryDenominatorOutOfPeriod
	}
	return &record, IndustryMetricReady
}

type BenchmarkKind string

const (
	BenchmarkDemo     BenchmarkKind = "demo"
	BenchmarkExternal BenchmarkKind = "external"
)

// BenchmarkRule identifies one explicit revision, separate from internal
// target plans. Maximum is an inclusive upper bound in Unit. Optional
// MinimumDenominator applies in the source denominator unit.
type BenchmarkRule struct {
	ID, Name, Publisher string
	Region              string
	MetricID            string
	TemplateVersion     string
	Unit                string
	Kind                BenchmarkKind
	BuildingType        BuildingType
	ObjectKind          ObjectKind
	Period              Granularity
	Meta                VersionMeta
	Maximum             float64
	MinimumDenominator  *float64
}

type BenchmarkStatus string

const (
	BenchmarkNoStandard       BenchmarkStatus = "no_standard"
	BenchmarkInapplicable     BenchmarkStatus = "inapplicable"
	BenchmarkNotComparable    BenchmarkStatus = "not_comparable"
	BenchmarkInsufficientData BenchmarkStatus = "insufficient_data"
	BenchmarkWithinThreshold  BenchmarkStatus = "within_threshold"
	BenchmarkExceedsThreshold BenchmarkStatus = "exceeds_threshold"
)

type BenchmarkResult struct {
	Rule   *BenchmarkRule
	Status BenchmarkStatus
	Reason string
	Delta  *float64
}

// EvaluateBenchmark compares one qualified metric to an explicitly supplied
// rule snapshot. The numeric relation is not a formal assessment.
func EvaluateBenchmark(metric IndustryMetric, rule *BenchmarkRule) BenchmarkResult {
	if rule == nil {
		return BenchmarkResult{Status: BenchmarkNoStandard, Reason: "no_standard"}
	}
	if !validBenchmarkRule(*rule) {
		return BenchmarkResult{Status: BenchmarkNotComparable, Reason: "invalid_rule"}
	}
	snapshot := *rule
	if snapshot.MinimumDenominator != nil {
		minimum := *snapshot.MinimumDenominator
		snapshot.MinimumDenominator = &minimum
	}
	result := BenchmarkResult{Rule: &snapshot}
	switch {
	case rule.Region != metric.Scope.Region:
		result.Status, result.Reason = BenchmarkInapplicable, "region_mismatch"
	case rule.BuildingType != metric.Scope.BuildingType || rule.ObjectKind != metric.Scope.Object.Kind:
		result.Status, result.Reason = BenchmarkInapplicable, "object_type_mismatch"
	case !rule.Meta.covers(metric.Window.Range):
		result.Status, result.Reason = BenchmarkInapplicable, "rule_out_of_period"
	case rule.MetricID != metric.Template.ID || rule.TemplateVersion != metric.Template.Meta.Version || rule.Unit != metric.Unit:
		result.Status, result.Reason = BenchmarkNotComparable, "metric_mismatch"
	case !benchmarkPeriodMatches(metric.Window, rule.Period):
		result.Status, result.Reason = BenchmarkNotComparable, "period_mismatch"
	case metric.Status == IndustryIncompatibleUsage || metric.Status == IndustryInapplicableTemplate || metric.Status == IndustryInvalidTemplate:
		result.Status, result.Reason = BenchmarkNotComparable, string(metric.Status)
	case metric.Status != IndustryMetricReady || !metric.validated || metric.Value == nil ||
		!finite(*metric.Value) || *metric.Value < 0 || metric.Denominator == nil:
		result.Status, result.Reason = BenchmarkInsufficientData, string(metric.Status)
	case rule.MinimumDenominator != nil && metric.Denominator.Value < *rule.MinimumDenominator:
		result.Status, result.Reason = BenchmarkInapplicable, "minimum_denominator"
	default:
		delta := *metric.Value - rule.Maximum
		result.Delta = &delta
		result.Status = BenchmarkWithinThreshold
		if delta > 0 {
			result.Status = BenchmarkExceedsThreshold
		}
	}
	return result
}

func validBenchmarkRule(rule BenchmarkRule) bool {
	return (rule.Kind == BenchmarkDemo || rule.Kind == BenchmarkExternal) &&
		named(rule.ID) && named(rule.Name) && named(rule.Publisher) && named(rule.Region) &&
		named(rule.MetricID) && named(rule.TemplateVersion) && named(rule.Unit) &&
		rule.BuildingType.valid() && (ObjectKey{Kind: rule.ObjectKind, ID: rule.ID}).valid() &&
		validIndustryRevision(rule.Meta) && finite(rule.Maximum) && rule.Maximum >= 0 &&
		(rule.Period == Hour || rule.Period == Day || rule.Period == Month) &&
		(rule.MinimumDenominator == nil || (finite(*rule.MinimumDenominator) && *rule.MinimumDenominator > 0))
}

func benchmarkPeriodMatches(window Window, period Granularity) bool {
	location, err := projectLocation(window.Timezone)
	if err != nil {
		return false
	}
	start := window.From.In(location)
	return aligned(start, period) && bucketEnd(start, period).Equal(window.To)
}
