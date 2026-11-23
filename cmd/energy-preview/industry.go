package main

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/admin"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const industryPath = overviewPath + "/industry"
const industryFixtureSystem = "industry-synthetic"
const industryDemoRegion = "合成片区 A"

type industryTemplateEvidence struct {
	Version         string    `json:"version"`
	Source          string    `json:"source"`
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	DenominatorKind string    `json:"denominatorKind"`
	DenominatorUnit string    `json:"denominatorUnit"`
}

type industryStandardEvidence struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Publisher    string    `json:"publisher"`
	Version      string    `json:"version"`
	Source       string    `json:"source"`
	Kind         string    `json:"kind"`
	Region       string    `json:"region"`
	BuildingType string    `json:"buildingType"`
	Unit         string    `json:"unit"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	Condition    string    `json:"condition"`
}

type industryBenchmarkView struct {
	Status   string                    `json:"status"`
	Limit    *float64                  `json:"limit"`
	Delta    *float64                  `json:"delta"`
	Standard *industryStandardEvidence `json:"standard"`
}

type industryMetricView struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name"`
	Definition  string                   `json:"definition"`
	Unit        string                   `json:"unit"`
	Status      string                   `json:"status"`
	Value       *float64                 `json:"value"`
	Template    industryTemplateEvidence `json:"template"`
	Denominator *metricDenominator       `json:"denominator"`
	Benchmark   industryBenchmarkView    `json:"benchmark"`
}

type industryReport struct {
	businessUsageScope
	BuildingName string               `json:"buildingName"`
	BuildingType string               `json:"buildingType"`
	Region       string               `json:"region"`
	Scenario     string               `json:"scenario"`
	Numerator    f06Value             `json:"numerator"`
	Quality      usageQuality         `json:"quality"`
	RuleVersion  string               `json:"ruleVersion"`
	Metrics      []industryMetricView `json:"metrics"`
}

type industryBuilding struct {
	name       string
	kind       buildingenergy.BuildingType
	meterScale float64
}

var industryBuildings = map[string]industryBuilding{
	"office-a":   {name: "办公楼 A", kind: buildingenergy.BuildingOffice},
	"office-b":   {name: "办公楼 B", kind: buildingenergy.BuildingOffice},
	"hospital-a": {name: "合成医院 A", kind: buildingenergy.BuildingHospital, meterScale: 2},
	"school-a":   {name: "合成学校 A", kind: buildingenergy.BuildingSchool, meterScale: .5},
	"hotel-a":    {name: "合成酒店 A", kind: buildingenergy.BuildingHotel, meterScale: .75},
}

type industryMetricFixture struct {
	id, name, definition, unit string
	kind                       buildingenergy.DenominatorKind
	denominator                *metricDenominator
	electricityLimit           float64
	waterLimit                 float64
}

func sampleIndustry(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	building := query.Get("building")
	energy := buildingenergy.EnergyType(query.Get("energy"))
	scenario := query.Get("scenario")
	preset, known := industryBuildings[building]
	if err != nil || len(query) != 3 || len(query["building"]) != 1 || len(query["energy"]) != 1 ||
		len(query["scenario"]) != 1 || !known || (energy != buildingenergy.Electricity && energy != buildingenergy.Water) ||
		!supportedIndustryScenario(scenario) {
		writeBusinessUsageError(w, buildingenergy.ErrInvalidScope)
		return
	}

	fixture, err := newIndustryFixture(building, preset, energy)
	if err != nil {
		writeBusinessUsageError(w, err)
		return
	}
	result, err := calculateIndustrySample(r.Context(), fixture, preset, scenario)
	if err != nil {
		writeBusinessUsageError(w, err)
		return
	}
	writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: result})
}

func supportedIndustryScenario(scenario string) bool {
	switch scenario {
	case "standard", "missing-denominator", "invalid-denominator", "out-of-period",
		"partial", "zero", "no-standard", "not-applicable", "incomparable":
		return true
	default:
		return false
	}
}

func newIndustryFixture(building string, preset industryBuilding, energy buildingenergy.EnergyType) (f06Fixture, error) {
	seedBuilding := building
	if preset.kind != buildingenergy.BuildingOffice {
		seedBuilding = "office-a"
	}
	fixture, err := newF06Fixture(seedBuilding, energy, 7)
	if err != nil {
		return f06Fixture{}, err
	}
	fixture, _, err = planTrendFixture(fixture, buildingenergy.Day, false)
	if err != nil {
		return f06Fixture{}, err
	}
	fixture.window.Granularity = buildingenergy.Day
	if preset.kind == buildingenergy.BuildingOffice {
		return fixture, nil
	}

	// Industry cohorts use the existing authorized three-circuit fixture
	// machinery with separate object keys, meters, and a synthetic project.
	fixture.project.Key.ProjectID = "f12-industry-demo"
	fixture.project.Meta = industryMeta("industry-directory-v1", "industry-demo-directory", fixture.effective)
	fixture.object.ID = building
	for i, circuit := range []string{"primary", "secondary", "reserve"} {
		fixture.meters[i].key = fixture.pointKey(circuit)
		fixture.meters[i].base *= preset.meterScale
	}
	return fixture, nil
}

func calculateIndustrySample(ctx context.Context, fixture f06Fixture, preset industryBuilding, scenario string) (industryReport, error) {
	service := f06Service{fixture: fixture, read: func(point buildingenergy.PointRef, window buildingenergy.Window) buildingenergy.MeteringData {
		return fixture.generateMeter(point, window, func(at time.Time, base float64) (float64, buildingenergy.Quality) {
			switch scenario {
			case "zero":
				return 0, buildingenergy.Normal
			case "partial":
				if at.Equal(window.From) {
					return 0, buildingenergy.Missing
				}
			}
			return octoberSample(at, base)
		})
	}}
	measured, err := service.classify(ctx, fixture.scope(), fixture.classification())
	if err != nil {
		return industryReport{}, err
	}
	report := measured.report
	scope := sampleUsageScope(fixture, f06Report{
		Sample: true, Unit: report.Energy[0].Unit, Timezone: report.Query.Window.Timezone,
		From: report.Query.Window.From, To: report.Query.Window.To, AsOf: fixture.window.AsOf,
		RuleVersion: measured.inputs[0].Result.RuleVersion, ClassificationRuleVersion: report.RuleVersion,
		CatalogVersion: report.CatalogVersion.Version, TopologyVersion: report.TopologyVersion.Version,
		Sources: fixture.projectSources(report.Query, report),
	})
	numerator := f06Measure(report.Energy[0].Total)
	result := industryReport{businessUsageScope: scope, BuildingName: preset.name, BuildingType: string(preset.kind),
		Region: industryDemoRegion, Scenario: scenario, Numerator: numerator, Quality: measureQuality(numerator), RuleVersion: buildingenergy.IndustryRuleVersion}
	metricScope := buildingenergy.IndustryContext{
		Project: report.Query.Project, Object: report.Query.Object, BuildingType: preset.kind, Region: industryDemoRegion,
	}
	for i, definition := range industryMetricFixtures(preset.kind, fixture) {
		result.Metrics = append(result.Metrics, projectIndustryMetric(metricScope, definition, report, fixture.energy, scenario, i == 0))
	}
	return result, nil
}

func industryMetricFixtures(kind buildingenergy.BuildingType, fixture f06Fixture) []industryMetricFixture {
	period := fixture.window.Range
	switch kind {
	case buildingenergy.BuildingHospital:
		return []industryMetricFixture{
			{id: "bed-days", name: "床日用量", definition: "统计期能源用量 ÷ 同期实际床日",
				kind: buildingenergy.DenominatorBedDays, unit: "床日",
				denominator:      syntheticIndustryDenominator(128, "床日", "合成住院业务登记", "hospital-bed-days-v1", "统计期实际占用床日合计", period),
				electricityLimit: 25, waterLimit: 6},
			{id: "business-revenue", name: "万元业务收入用量", definition: "统计期能源用量 ÷ 同期业务收入（万元），由元折算",
				kind: buildingenergy.DenominatorRevenue, unit: "元",
				denominator: syntheticIndustryDenominator(240_000, "元", "合成财务记录", "hospital-business-revenue-v1", "统计期业务收入合计，原始计量单位为元", period)},
		}
	case buildingenergy.BuildingSchool:
		return []industryMetricFixture{
			{id: "students", name: "生均用量", definition: "统计期能源用量 ÷ 有效学生人数",
				kind: buildingenergy.DenominatorStudents, unit: "人",
				denominator:      syntheticIndustryDenominator(240, "人", "合成在读学生名册", "school-enrolled-students-v1", "覆盖所选统计期的学生人数登记", industryCalendarMonth(period)),
				electricityLimit: 3, waterLimit: .7},
		}
	case buildingenergy.BuildingHotel:
		return []industryMetricFixture{
			{id: "sold-room-nights", name: "已售客房间夜用量", definition: "统计期能源用量 ÷ 同期已售客房间夜",
				kind: buildingenergy.DenominatorRoomNights, unit: "间夜",
				denominator:      syntheticIndustryDenominator(72, "间夜", "合成酒店经营日结", "hotel-sold-room-nights-v1", "统计期已售间夜合计", period),
				electricityLimit: 18, waterLimit: 4.5},
		}
	default:
		area, people := sampleDenominators(fixture)
		return []industryMetricFixture{
			{id: "per-area", name: "单位面积用量", definition: "统计期能源用量 ÷ 有效建筑面积",
				kind: buildingenergy.DenominatorArea, unit: "m²", denominator: area, electricityLimit: 1.5, waterLimit: .35},
			{id: "per-person", name: "人均用量", definition: "统计期能源用量 ÷ 同期日均人数",
				kind: buildingenergy.DenominatorPeople, unit: "人", denominator: people},
		}
	}
}

func industryCalendarMonth(period buildingenergy.Range) buildingenergy.Range {
	from := time.Date(period.From.Year(), period.From.Month(), 1, 0, 0, 0, 0, period.From.Location())
	return buildingenergy.Range{From: from, To: from.AddDate(0, 1, 0)}
}

func syntheticIndustryDenominator(value float64, unit, source, version, basis string, period buildingenergy.Range) *metricDenominator {
	return &metricDenominator{Value: value, Unit: unit, Source: source, Version: version, Basis: basis, From: period.From, To: period.To}
}

func industryMeta(version, source string, period buildingenergy.Range) buildingenergy.VersionMeta {
	return buildingenergy.VersionMeta{Version: version, Source: buildingenergy.SourceRef{System: industryFixtureSystem, ID: source}, Effective: period}
}

func projectIndustryMetric(scope buildingenergy.IndustryContext, definition industryMetricFixture, report buildingenergy.ClassifiedUsageReport,
	energy buildingenergy.EnergyType, scenario string, primary bool) industryMetricView {
	effective := industryCalendarMonth(report.Query.Window.Range)
	template := buildingenergy.IndustryTemplate{
		ID: definition.id, Name: definition.name, Meta: industryMeta("sample-template-"+definition.id+"-v1", "合成行业模板登记册", effective),
		BuildingType: scope.BuildingType, ObjectKind: scope.Object.Kind, Energy: energy, Denominator: definition.kind,
	}
	evidence := definition.denominator
	if primary && evidence != nil {
		switch scenario {
		case "missing-denominator":
			evidence = nil
		case "invalid-denominator":
			evidence.Value = 0
		case "out-of-period":
			evidence.From = report.Query.Window.From.AddDate(0, 0, -1)
			evidence.To = report.Query.Window.From
		}
	}
	var denominator *buildingenergy.BusinessDenominator
	if evidence != nil {
		period := buildingenergy.Range{From: evidence.From, To: evidence.To}
		denominator = &buildingenergy.BusinessDenominator{
			Project: scope.Project, Object: scope.Object, Kind: definition.kind, Unit: evidence.Unit, Value: evidence.Value,
			Period: period, Meta: industryMeta(evidence.Version, evidence.Source, period), Basis: evidence.Basis,
		}
	}
	metric := buildingenergy.EvaluateIndustryMetric(scope, template, report, denominator)
	benchmark := buildingenergy.EvaluateBenchmark(metric, sampleIndustryBenchmark(metric, definition, effective, scenario))
	return industryMetricView{
		ID: template.ID, Name: template.Name, Definition: definition.definition, Unit: metric.Unit,
		Status: string(metric.Status), Value: metric.Value, Denominator: evidence,
		Template: industryTemplateEvidence{Version: template.Meta.Version, Source: template.Meta.Source.ID,
			From: template.Meta.Effective.From, To: template.Meta.Effective.To, DenominatorKind: string(template.Denominator),
			DenominatorUnit: definition.unit},
		Benchmark: industryBenchmarkProjection(benchmark),
	}
}

func sampleIndustryBenchmark(metric buildingenergy.IndustryMetric, definition industryMetricFixture, effective buildingenergy.Range,
	scenario string) *buildingenergy.BenchmarkRule {
	limit := definition.electricityLimit
	if metric.Template.Energy == buildingenergy.Water {
		limit = definition.waterLimit
	}
	if limit == 0 || scenario == "no-standard" {
		return nil
	}
	rule := &buildingenergy.BenchmarkRule{
		ID: "demo-" + definition.id + "-" + string(metric.Template.Energy), Name: "演示定额 · " + definition.name,
		Publisher: "建筑能耗隔离预览", Region: industryDemoRegion, Kind: buildingenergy.BenchmarkDemo,
		MetricID: metric.Template.ID, TemplateVersion: metric.Template.Meta.Version,
		Unit: metric.Unit, BuildingType: metric.Scope.BuildingType, ObjectKind: metric.Scope.Object.Kind,
		Period: buildingenergy.Day, Meta: industryMeta("demo-benchmark-v1", "合成定额规则登记册", effective),
		Maximum: limit,
	}
	switch scenario {
	case "not-applicable":
		rule.Region = "合成片区 B"
	case "incomparable":
		rule.Unit = metric.Unit + "（其他口径）"
	}
	return rule
}

func industryBenchmarkProjection(result buildingenergy.BenchmarkResult) industryBenchmarkView {
	view := industryBenchmarkView{Status: string(result.Status), Delta: result.Delta}
	if rule := result.Rule; rule != nil {
		if result.Delta != nil {
			view.Limit = &rule.Maximum
		}
		view.Standard = &industryStandardEvidence{
			ID: rule.ID, Name: rule.Name, Publisher: rule.Publisher, Version: rule.Meta.Version, Source: rule.Meta.Source.ID,
			Kind: string(rule.Kind), Region: rule.Region, BuildingType: string(rule.BuildingType), Unit: rule.Unit,
			From: rule.Meta.Effective.From, To: rule.Meta.Effective.To,
			Condition: "适用地区与业态匹配、自然日完整、指标模板版本与单位一致",
		}
	}
	return view
}
