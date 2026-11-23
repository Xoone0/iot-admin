package main

import (
	"cmp"
	"encoding/csv"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const (
	industryReportRuleVersion = "f22-f25-industry-report-v1"
	industryReportNotice      = "演示规则，非权威标准"
)

type industryReportOptions struct {
	Kind      string
	Fields    []string
	Sort      string
	Direction string
}

type industryReportField string

const (
	industryReportMetric              industryReportField = "metric"
	industryReportValue               industryReportField = "value"
	industryReportNumerator           industryReportField = "numerator"
	industryReportDenominator         industryReportField = "denominator"
	industryReportDenominatorEvidence industryReportField = "denominatorEvidence"
	industryReportCoverage            industryReportField = "coverage"
	industryReportDefinition          industryReportField = "definition"
	industryReportTemplate            industryReportField = "template"
	industryReportComparison          industryReportField = "comparison"
	industryReportBenchmarkRule       industryReportField = "benchmarkRule"
	industryReportBenchmarkThreshold  industryReportField = "benchmarkThreshold"
	industryReportBenchmarkDelta      industryReportField = "benchmarkDelta"
	industryReportInternalTarget      industryReportField = "internalTarget"
	industryReportExternalStandard    industryReportField = "externalStandard"
	industryReportScope               industryReportField = "scope"
	industryReportPeriod              industryReportField = "period"
	industryReportUnit                industryReportField = "unit"
	industryReportQuality             industryReportField = "quality"
	industryReportStatus              industryReportField = "status"
	industryReportDisclaimer          industryReportField = "notice"
	industryReportVersions            industryReportField = "versions"
)

type industryReportColumn struct {
	ID    industryReportField `json:"id"`
	Label string              `json:"label"`
}

// Only these report columns may be selected by a caller. Preview identity,
// meter internals and unpublished fields do not belong to the custom catalog.
var industryReportCatalog = []industryReportColumn{
	{industryReportValue, "指标值"},
	{industryReportNumerator, "分子用量"},
	{industryReportDenominator, "业务分母"},
	{industryReportDenominatorEvidence, "分母单位与来源"},
	{industryReportCoverage, "覆盖率"},
	{industryReportDefinition, "指标定义"},
	{industryReportTemplate, "模板来源与版本"},
	{industryReportComparison, "演示对标"},
	{industryReportBenchmarkRule, "演示规则与适用条件"},
	{industryReportBenchmarkThreshold, "演示上限"},
	{industryReportBenchmarkDelta, "与演示上限的差值"},
	{industryReportInternalTarget, "内部目标"},
	{industryReportExternalStandard, "外部定额标准"},
}

var industryReportScopeColumns = []industryReportColumn{
	{industryReportScope, "对象与能源"},
	{industryReportPeriod, "统计区间与时区"},
	{industryReportUnit, "指标单位"},
	{industryReportQuality, "数据质量"},
	{industryReportStatus, "结果与限制"},
	{industryReportVersions, "快照编号与版本"},
	{industryReportDisclaimer, industryReportNotice},
}

var industryReportSorts = []industryReportColumn{
	{industryReportMetric, "指标名称"},
	{industryReportValue, "指标值（按指标单位分组）"},
	{industryReportCoverage, "覆盖率"},
}

var industryReportFixedOrder = []industryReportField{
	industryReportMetric, industryReportValue, industryReportUnit, industryReportQuality,
	industryReportNumerator, industryReportDenominator, industryReportCoverage, industryReportStatus,
	industryReportDefinition, industryReportDenominatorEvidence, industryReportTemplate, industryReportComparison,
	industryReportBenchmarkThreshold, industryReportBenchmarkDelta, industryReportBenchmarkRule,
	industryReportInternalTarget, industryReportExternalStandard, industryReportScope, industryReportPeriod,
	industryReportVersions, industryReportDisclaimer,
}

type industryReportSnapshot struct {
	businessUsageScope
	BuildingName      string                 `json:"buildingName"`
	Kind              string                 `json:"kind"`
	Scenario          string                 `json:"scenario"`
	Status            string                 `json:"status"`
	StatusMessage     string                 `json:"statusMessage"`
	Notice            string                 `json:"notice"`
	GeneratedAt       time.Time              `json:"generatedAt"`
	ReportID          string                 `json:"reportId"`
	Operator          string                 `json:"operator"`
	RuleVersion       string                 `json:"ruleVersion"`
	MetricRuleVersion string                 `json:"metricRuleVersion"`
	Columns           []industryReportColumn `json:"columns"`
	Rows              [][]string             `json:"rows"`
	AvailableFields   []industryReportColumn `json:"availableFields,omitempty"`
	AvailableSorts    []industryReportColumn `json:"availableSorts,omitempty"`
	Sort              string                 `json:"sort"`
	Direction         string                 `json:"direction"`
	Filename          string                 `json:"filename"`
	CSV               string                 `json:"csv"`
}

func findIndustryReportColumn(columns []industryReportColumn, id industryReportField) (industryReportColumn, bool) {
	for _, column := range columns {
		if column.ID == id {
			return column, true
		}
	}
	return industryReportColumn{}, false
}

func checkIndustryReportOptions(options industryReportOptions) error {
	switch options.Kind {
	case "f22":
		if options.Fields != nil || options.Sort != "" || options.Direction != "" {
			return buildingenergy.ErrInvalidScope
		}
	case "f25":
		if len(options.Fields) > len(industryReportCatalog) {
			return buildingenergy.ErrInvalidScope
		}
		seen := make(map[industryReportField]bool, len(options.Fields))
		for _, requested := range options.Fields {
			field := industryReportField(requested)
			if _, valid := findIndustryReportColumn(industryReportCatalog, field); !valid || seen[field] {
				return buildingenergy.ErrInvalidScope
			}
			seen[field] = true
		}
		if _, valid := findIndustryReportColumn(industryReportSorts, industryReportField(options.Sort)); options.Sort != "" && !valid {
			return buildingenergy.ErrInvalidScope
		}
		if options.Direction != "" && options.Direction != "asc" && options.Direction != "desc" {
			return buildingenergy.ErrInvalidScope
		}
	default:
		return buildingenergy.ErrInvalidScope
	}
	return nil
}

// projectIndustryReport uses the already authorized, computed industry result.
// It performs no lookup, rule evaluation or metric calculation.
func projectIndustryReport(report industryReport, options industryReportOptions, now time.Time, operator string) (industryReportSnapshot, error) {
	if err := checkIndustryReportOptions(options); err != nil {
		return industryReportSnapshot{}, err
	}
	if !report.Sample || report.Project == "" || report.Building == "" || report.From.IsZero() || !report.From.Before(report.To) {
		return industryReportSnapshot{}, buildingenergy.ErrInvalidScope
	}
	location, err := time.LoadLocation(report.Timezone)
	if err != nil {
		return industryReportSnapshot{}, buildingenergy.ErrInvalidScope
	}
	for _, metric := range report.Metrics {
		if rule := metric.Benchmark.Standard; rule != nil && rule.Kind != string(buildingenergy.BenchmarkDemo) {
			return industryReportSnapshot{}, buildingenergy.ErrInvalidScope
		}
	}

	result := industryReportSnapshot{
		businessUsageScope: report.businessUsageScope, BuildingName: report.BuildingName, Kind: options.Kind, Status: "ready", Notice: industryReportNotice,
		Scenario:    report.Scenario,
		GeneratedAt: now.In(location).Truncate(time.Second), Operator: operator,
		ReportID:    fmt.Sprintf("sample-%s-%s-%s-%d", options.Kind, report.Building, report.Energy, now.UnixNano()),
		RuleVersion: industryReportRuleVersion, MetricRuleVersion: report.RuleVersion,
		Rows:     make([][]string, 0, len(report.Metrics)),
		Filename: fmt.Sprintf("energy-%s-%s-%s-%s.csv", options.Kind, report.Building, report.Energy, report.From.Format(time.DateOnly)),
	}
	result.Sources = slices.Clone(report.Sources)
	metricColumn := industryReportColumn{industryReportMetric, "指标"}
	metrics := report.Metrics
	if options.Kind == "f25" {
		result.Columns = []industryReportColumn{metricColumn}
		result.AvailableFields = slices.Clone(industryReportCatalog)
		result.AvailableSorts = slices.Clone(industryReportSorts)
		result.Sort = cmp.Or(options.Sort, string(industryReportMetric))
		result.Direction = cmp.Or(options.Direction, "asc")
		metrics = sortIndustryReportMetrics(report.Metrics, industryReportField(result.Sort), result.Direction)
		fields := options.Fields
		if fields == nil {
			fields = []string{"value", "numerator", "denominator", "denominatorEvidence", "template"}
		}
		for _, field := range fields {
			column, _ := findIndustryReportColumn(industryReportCatalog, industryReportField(field))
			result.Columns = append(result.Columns, column)
		}
		result.Columns = append(result.Columns, industryReportScopeColumns...)
	} else {
		columns := append([]industryReportColumn{metricColumn}, industryReportScopeColumns...)
		columns = append(columns, industryReportCatalog...)
		for _, field := range industryReportFixedOrder {
			column, _ := findIndustryReportColumn(columns, field)
			result.Columns = append(result.Columns, column)
		}
	}
	if len(metrics) == 0 {
		result.Status, result.StatusMessage = "no_applicable_template", "无适用模板"
	}
	for _, metric := range metrics {
		cells := make([]string, len(result.Columns))
		for i, column := range result.Columns {
			cells[i] = projectIndustryReportCell(column.ID, report, metric, result)
		}
		result.Rows = append(result.Rows, cells)
	}
	headers := make([]string, len(result.Columns))
	for i, column := range result.Columns {
		headers[i] = column.Label
	}
	result.CSV, err = encodeReportCSV(headers, func(writer *csv.Writer) error {
		return writer.WriteAll(result.Rows)
	})
	return result, err
}

func sortIndustryReportMetrics(metrics []industryMetricView, field industryReportField, direction string) []industryMetricView {
	sorted := slices.Clone(metrics)
	slices.SortStableFunc(sorted, func(first, second industryMetricView) int {
		order := 0
		switch field {
		case industryReportMetric:
			order = strings.Compare(first.Name, second.Name)
		case industryReportValue:
			firstValid, secondValid := isIndustryReportNumber(first.Value), isIndustryReportNumber(second.Value)
			if firstValid != secondValid {
				if firstValid {
					return -1
				}
				return 1
			}
			if firstValid {
				// Group the distinct units before ordering by value. The display
				// order does not imply a comparison across different metrics.
				if group := strings.Compare(first.Unit, second.Unit); group != 0 {
					return group
				}
				order = cmp.Compare(*first.Value, *second.Value)
			}
		case industryReportCoverage:
			// All metrics here share the one measured energy numerator and
			// coverage. Rows with the same coverage use the same stable order.
		}
		if direction == "desc" {
			order = -order
		}
		return cmp.Or(order, strings.Compare(first.ID, second.ID))
	})
	return sorted
}

func projectIndustryReportCell(field industryReportField, report industryReport, metric industryMetricView, snapshot industryReportSnapshot) string {
	var text string
	switch field {
	case industryReportMetric:
		text = metric.Name
	case industryReportValue:
		return industryReportNumber(metric.Value)
	case industryReportNumerator:
		return industryReportNumber(report.Numerator.Usage)
	case industryReportDenominator:
		if metric.Denominator != nil {
			return industryReportNumber(&metric.Denominator.Value)
		}
	case industryReportDenominatorEvidence:
		text = "预期单位 " + metric.Template.DenominatorUnit
		if evidence := metric.Denominator; evidence != nil {
			text = strings.Join([]string{
				evidence.Unit, evidence.Source, evidence.Version, industryReportPeriodText(evidence.From, evidence.To), evidence.Basis,
			}, " · ")
		}
	case industryReportCoverage:
		return industryReportCoverageText(report.Numerator.Coverage)
	case industryReportDefinition:
		text = metric.Definition
	case industryReportTemplate:
		text = strings.Join([]string{metric.Template.Source, metric.Template.Version, industryReportPeriodText(metric.Template.From, metric.Template.To)}, " · ")
	case industryReportComparison:
		text = industryReportBenchmarkStatus(metric.Benchmark.Status)
	case industryReportBenchmarkRule:
		if rule := metric.Benchmark.Standard; rule != nil {
			text = strings.Join([]string{
				rule.Name, rule.Source, rule.Publisher, rule.Version, rule.Region, industryReportBuildingType(rule.BuildingType),
				rule.Unit, industryReportPeriodText(rule.From, rule.To), rule.Condition,
			}, " · ")
		}
	case industryReportBenchmarkThreshold:
		return industryReportNumber(metric.Benchmark.Limit)
	case industryReportBenchmarkDelta:
		return industryReportNumber(metric.Benchmark.Delta)
	case industryReportInternalTarget:
		text = "本报表未纳入内部目标（参见 F14）"
	case industryReportExternalStandard:
		text = "未接入权威标准"
	case industryReportScope:
		text = fmt.Sprintf("%s（%s） · %s · %s · 合成数据 · 项目 %s · %s %s", report.BuildingName, report.Building, industryReportBuildingType(report.BuildingType),
			report.Region, report.Project, industryReportEnergy(report.Energy), report.Unit)
	case industryReportPeriod:
		text = strings.Join([]string{industryReportPeriodText(report.From, report.To), report.Timezone, "数据截止 " + report.AsOf.Format(time.RFC3339)}, " · ")
	case industryReportUnit:
		text = metric.Unit
	case industryReportQuality:
		text = industryReportQualityText(report.Quality, report.Numerator.Coverage)
	case industryReportStatus:
		text = industryReportMetricStatus(metric.Status) + " · " + industryReportBenchmarkStatus(metric.Benchmark.Status)
	case industryReportDisclaimer:
		text = industryReportNotice
	case industryReportVersions:
		text = strings.Join([]string{
			snapshot.ReportID, snapshot.GeneratedAt.Format(time.RFC3339), "报表 " + snapshot.RuleVersion, "指标 " + snapshot.MetricRuleVersion,
			"计量 " + report.MeterRuleVersion, "分项 " + report.ClassificationRuleVersion, "目录 " + report.CatalogVersion, "拓扑 " + report.TopologyVersion,
			"操作者 " + snapshot.Operator, fmt.Sprintf("来源 %d 点位", len(report.Sources)),
		}, " · ")
	}
	return industryReportText(text)
}

func industryReportPeriodText(from, to time.Time) string {
	return "[" + from.Format(time.RFC3339) + ", " + to.Format(time.RFC3339) + ")"
}

func isIndustryReportNumber(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0)
}

func industryReportNumber(value *float64) string {
	if !isIndustryReportNumber(value) {
		return ""
	}
	return reportOptionalNumber(value)
}

// Normalize displayed text once, so preview and parsed CSV agree even with
// line breaks. Spreadsheet formula signs only receive a prefix on text cells.
func industryReportText(value string) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	leading, _ := utf8.DecodeRuneInString(strings.TrimLeftFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\uFEFF' }))
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\n") || strings.ContainsRune("=+-@＝＋－＠", leading) {
		return "'" + value
	}
	return value
}

func industryReportCoverageText(coverage f06Coverage) string {
	if !isIndustryReportNumber(coverage.Rate) {
		return ""
	}
	return fmt.Sprintf("%.1f%%", *coverage.Rate*100)
}

func industryReportQualityText(quality usageQuality, coverage f06Coverage) string {
	status := map[usageQuality]string{
		usageValid: "正常", usageValidZero: "有效零", usageMissing: "缺测", usageSuspect: "可疑", usagePartial: "部分覆盖",
	}[quality]
	rate := industryReportCoverageText(coverage)
	if rate == "" {
		rate = "未取得覆盖率"
	}
	missingRate := "缺失率未知"
	if coverage.Expected > 0 {
		missingRate = fmt.Sprintf("缺失率 %.1f%%", coverage.Missing/coverage.Expected*100)
	}
	return fmt.Sprintf("%s · 覆盖 %s · 有效 %s/%s · 缺测 %s · 可疑 %s · %s",
		status, rate, reportNumber(coverage.Valid), reportNumber(coverage.Expected), reportNumber(coverage.Missing), reportNumber(coverage.Suspect), missingRate)
}

func industryReportMetricStatus(status string) string {
	switch buildingenergy.IndustryMetricStatus(status) {
	case buildingenergy.IndustryMetricReady:
		return "指标已计算"
	case buildingenergy.IndustryMissingDenominator:
		return "缺少有效分母记录"
	case buildingenergy.IndustryInvalidDenominator:
		return "分母数值、单位或来源无效"
	case buildingenergy.IndustryDenominatorOutOfPeriod:
		return "分母记录与当前统计期不匹配"
	case buildingenergy.IndustryMissingUsage:
		return "统计期缺少有效用量"
	case buildingenergy.IndustryInsufficientQuality:
		return "计量覆盖率或数据质量不足"
	case buildingenergy.IndustryIncompatibleUsage:
		return "计量与模板的范围或单位不一致"
	case buildingenergy.IndustryInvalidTemplate:
		return "模板定义或版本无效"
	case buildingenergy.IndustryInapplicableTemplate:
		return "模板不适用于当前范围"
	default:
		return "指标状态未确认"
	}
}

func industryReportBenchmarkStatus(status string) string {
	switch buildingenergy.BenchmarkStatus(status) {
	case buildingenergy.BenchmarkNoStandard:
		return "无演示规则"
	case buildingenergy.BenchmarkInapplicable:
		return "不适用（地区、业态、时间或其他适用条件未满足）"
	case buildingenergy.BenchmarkNotComparable:
		return "不可比较（统计周期、指标版本或单位不一致）"
	case buildingenergy.BenchmarkInsufficientData:
		return "数据不足（需要可用指标及完整计量）"
	case buildingenergy.BenchmarkWithinThreshold:
		return "未超过演示阈值"
	case buildingenergy.BenchmarkExceedsThreshold:
		return "超过演示阈值"
	default:
		return "演示对标状态未确认"
	}
}

func industryReportBuildingType(kind string) string {
	return map[buildingenergy.BuildingType]string{
		buildingenergy.BuildingHospital: "医院", buildingenergy.BuildingSchool: "学校",
		buildingenergy.BuildingHotel: "酒店", buildingenergy.BuildingOffice: "办公楼",
	}[buildingenergy.BuildingType(kind)]
}

func industryReportEnergy(energy buildingenergy.EnergyType) string {
	return map[buildingenergy.EnergyType]string{buildingenergy.Electricity: "用电", buildingenergy.Water: "用水"}[energy]
}
