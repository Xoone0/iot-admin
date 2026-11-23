package main

import (
	"errors"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/admin"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const efficiencyPath = overviewPath + "/efficiency"
const efficiencyRuleVersion = "f11-intensity-v1"

type businessUsageScope struct {
	Sample                    bool                      `json:"sample"`
	Project                   string                    `json:"project"`
	Building                  string                    `json:"building"`
	Energy                    buildingenergy.EnergyType `json:"energy"`
	Unit                      string                    `json:"unit"`
	Timezone                  string                    `json:"timezone"`
	From                      time.Time                 `json:"from"`
	To                        time.Time                 `json:"to"`
	AsOf                      time.Time                 `json:"asOf"`
	MeterRuleVersion          string                    `json:"meterRuleVersion"`
	ClassificationRuleVersion string                    `json:"classificationRuleVersion"`
	CatalogVersion            string                    `json:"catalogVersion"`
	TopologyVersion           string                    `json:"topologyVersion"`
	Sources                   []f06Source               `json:"sources"`
}

func sampleUsageScope(f f06Fixture, report f06Report) businessUsageScope {
	return businessUsageScope{
		Sample: report.Sample, Project: f.project.Key.ProjectID, Building: f.object.ID, Energy: f.energy, Unit: report.Unit,
		Timezone: report.Timezone, From: report.From, To: report.To, AsOf: report.AsOf,
		MeterRuleVersion: report.RuleVersion, ClassificationRuleVersion: report.ClassificationRuleVersion,
		CatalogVersion: report.CatalogVersion, TopologyVersion: report.TopologyVersion, Sources: report.Sources,
	}
}

type usageQuality string

const (
	usageValid     usageQuality = "valid"
	usageValidZero usageQuality = "valid_zero"
	usageMissing   usageQuality = "missing"
	usageSuspect   usageQuality = "suspect"
	usagePartial   usageQuality = "partial"
)

func measureQuality(measure f06Value) usageQuality {
	if measure.Usage == nil {
		if measure.Coverage.Suspect > 0 {
			return usageSuspect
		}
		return usageMissing
	}
	coverage := measure.Coverage
	if coverage.Expected <= 0 || coverage.Valid != coverage.Expected || coverage.Missing > 0 || coverage.Suspect > 0 {
		return usagePartial
	}
	if *measure.Usage == 0 {
		return usageValidZero
	}
	return usageValid
}

type metricDenominator struct {
	Value   float64   `json:"value"`
	Unit    string    `json:"unit"`
	Source  string    `json:"source"`
	Version string    `json:"version"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Basis   string    `json:"basis"`
}

type intensityMetric struct {
	ID          string             `json:"id"`
	Value       *float64           `json:"value"`
	Unit        string             `json:"unit"`
	Status      string             `json:"status"`
	Denominator *metricDenominator `json:"denominator"`
}

type efficiencyReport struct {
	businessUsageScope
	Numerator   f06Value          `json:"numerator"`
	Quality     usageQuality      `json:"quality"`
	RuleVersion string            `json:"ruleVersion"`
	Metrics     []intensityMetric `json:"metrics"`
}

func sampleEfficiency(w http.ResponseWriter, r *http.Request) {
	fixture, _, err := selectBusinessUsage(r.URL.RawQuery, "")
	if err != nil {
		writeBusinessUsageError(w, err)
		return
	}
	service := f06Service{fixture: fixture, read: fixture.readMeter}
	usage, err := service.calculate(r.Context(), fixture.scope())
	if err != nil {
		writeBusinessUsageError(w, err)
		return
	}
	area, headcount := sampleDenominators(fixture)
	report := efficiencyReport{
		businessUsageScope: sampleUsageScope(fixture, usage),
		Numerator:          usage.Total, Quality: measureQuality(usage.Total), RuleVersion: efficiencyRuleVersion,
		Metrics: []intensityMetric{
			calculateIntensity("per-area", "m²", usage, area),
			calculateIntensity("per-person", "人", usage, headcount),
		},
	}
	writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: report})
}

// Each fixture value is constant for this building over its effective period.
// No headcount is defined for building B. The count for A represents the
// period's daily average under the synthetic constant-count premise.
func sampleDenominators(f f06Fixture) (area, headcount *metricDenominator) {
	location := f.window.From.Location()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, location)
	to := from.AddDate(0, 1, 0)
	var squareMeters float64
	switch f.object.ID {
	case "office-a":
		squareMeters = 1200
		headcount = &metricDenominator{Value: 96, Unit: "人", Source: "合成人数记录", Version: "sample-headcount-v1",
			From: from, To: to, Basis: "统计期日均人数，样本期内每日人数恒定"}
	case "office-b":
		squareMeters = 2400
	default:
		return nil, nil
	}
	area = &metricDenominator{Value: squareMeters, Unit: "m²", Source: "合成建筑面积档案", Version: "sample-area-v1",
		From: from, To: to, Basis: "建筑总建筑面积"}
	return area, headcount
}

func calculateIntensity(id, denominatorUnit string, usage f06Report, denominator *metricDenominator) intensityMetric {
	metric := intensityMetric{ID: id, Unit: usage.Unit + "/" + denominatorUnit, Denominator: denominator}
	switch {
	case denominator == nil:
		metric.Status = "missing_denominator"
	case math.IsNaN(denominator.Value) || math.IsInf(denominator.Value, 0):
		metric.Status = "invalid_denominator"
		metric.Denominator = nil
	case denominator.Value <= 0 || denominator.Unit != denominatorUnit || denominator.Source == "" || denominator.Version == "":
		metric.Status = "invalid_denominator"
	case denominator.From.IsZero() || !denominator.From.Before(denominator.To) || usage.From.Before(denominator.From) || usage.To.After(denominator.To):
		metric.Status = "denominator_out_of_period"
	case usage.Total.Usage == nil:
		metric.Status = "missing_usage"
	case measureQuality(usage.Total) == usagePartial:
		metric.Status = "insufficient_quality"
	default:
		value := *usage.Total.Usage / denominator.Value
		if math.IsNaN(value) || math.IsInf(value, 0) {
			metric.Status = "insufficient_quality"
			break
		}
		metric.Value, metric.Status = &value, "ready"
	}
	return metric
}

// The business previews accept the same fixed time selections as F06. Extra
// selectors must be declared by the caller; arbitrary keys and repeated values
// never widen the metering scope.
func selectBusinessUsage(rawQuery, extra string) (f06Fixture, string, error) {
	query, err := url.ParseQuery(rawQuery)
	keys := []string{"building", "energy", "period"}
	if extra != "" {
		keys = append(keys, extra)
	}
	if err != nil || len(query) != len(keys) {
		return f06Fixture{}, "", buildingenergy.ErrInvalidScope
	}
	for _, key := range keys {
		if len(query[key]) != 1 || query.Get(key) == "" {
			return f06Fixture{}, "", buildingenergy.ErrInvalidScope
		}
	}
	period := query.Get("period")
	days := 7
	switch period {
	case "day", "7":
	case "14", "30":
		days, _ = strconv.Atoi(period)
	default:
		return f06Fixture{}, "", buildingenergy.ErrInvalidScope
	}
	fixture, err := newF06Fixture(query.Get("building"), buildingenergy.EnergyType(query.Get("energy")), days)
	if err == nil && period == "day" {
		fixture, _, err = planTrendFixture(fixture, buildingenergy.Day, false)
		fixture.window.Granularity = buildingenergy.Day
	}
	return fixture, query.Get(extra), err
}

func writeBusinessUsageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, buildingenergy.ErrInvalidScope):
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "无效的样本筛选条件"})
	case errors.Is(err, buildingenergy.ErrAccessDenied):
		writeResponse(w, http.StatusForbidden, admin.Response{Code: 1, Message: "样本范围未授权"})
	default:
		log.Printf("calculate business energy sample: %v", err)
		writeResponse(w, http.StatusInternalServerError, admin.Response{Code: 1, Message: "样本计算失败"})
	}
}
