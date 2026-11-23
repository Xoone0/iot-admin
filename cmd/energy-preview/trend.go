package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/admin"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const trendPath = "/admin/v1/analytics/building-energy/preview/trend"

type f08Period struct {
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Hours float64   `json:"hours"`
	f06Value
	MeterRuleVersion          string `json:"meterRuleVersion"`
	ClassificationRuleVersion string `json:"classificationRuleVersion"`
	CatalogVersion            string `json:"catalogVersion"`
	TopologyVersion           string `json:"topologyVersion"`
}

type f08Comparison struct {
	Period     f08Period                  `json:"period"`
	Status     buildingenergy.TrendStatus `json:"status"`
	Delta      *float64                   `json:"delta"`
	ChangeRate *float64                   `json:"changeRate"`
	Adjusted   bool                       `json:"adjusted"`
}

type f08Report struct {
	Sample      bool          `json:"sample"`
	Unit        string        `json:"unit"`
	Timezone    string        `json:"timezone"`
	AsOf        time.Time     `json:"asOf"`
	RuleVersion string        `json:"ruleVersion"`
	Partial     bool          `json:"partial"`
	Current     f08Period     `json:"current"`
	Previous    f08Comparison `json:"previous"`
	PriorYear   f08Comparison `json:"priorYear"`
	Trend       []f08Period   `json:"trend"`
}

func sampleTrend(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 5 || len(values["building"]) != 1 || len(values["energy"]) != 1 ||
		len(values["granularity"]) != 1 || len(values["mode"]) != 1 || len(values["scenario"]) != 1 {
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "仅支持样本建筑、能源、统计粒度、周期和场景"})
		return
	}
	f, err := newF06Fixture(values.Get("building"), buildingenergy.EnergyType(values.Get("energy")), 7)
	mode, scenario := values.Get("mode"), values.Get("scenario")
	grain := buildingenergy.Granularity(values.Get("granularity"))
	if err != nil || !supportedTrendSample(grain, mode, scenario) {
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "无效的趋势样本筛选条件"})
		return
	}
	report, err := calculateTrendSample(r.Context(), f, grain, mode == "to-date", scenario)
	if errors.Is(err, buildingenergy.ErrAccessDenied) {
		writeResponse(w, http.StatusForbidden, admin.Response{Code: 1, Message: "样本范围未授权"})
		return
	}
	if err != nil {
		log.Printf("calculate trend preview: %v", err)
		writeResponse(w, http.StatusInternalServerError, admin.Response{Code: 1, Message: "趋势样本计算失败"})
		return
	}
	writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: report})
}

func supportedTrendSample(grain buildingenergy.Granularity, mode, scenario string) bool {
	if (grain != buildingenergy.Hour && grain != buildingenergy.Day && grain != buildingenergy.Month) ||
		(mode != "complete" && mode != "to-date") || (grain == buildingenergy.Hour && mode == "to-date") {
		return false
	}
	switch scenario {
	case "standard", "missing", "suspect", "zero", "scope":
		return true
	default:
		return false
	}
}

type trendSamplePeriod struct {
	report buildingenergy.ClassifiedUsageReport
	value  f08Period
}

func planTrendFixture(f f06Fixture, grain buildingenergy.Granularity, partial bool) (f06Fixture, buildingenergy.TrendPlan, error) {
	location := f.effective.From.Location()
	anchor := time.Date(2026, 10, 15, 0, 0, 0, 0, location)
	lookback := map[buildingenergy.Granularity]int{buildingenergy.Hour: 24, buildingenergy.Day: 14, buildingenergy.Month: 12}
	request := buildingenergy.TrendRequest{At: anchor, Timezone: f.project.Timezone, Granularity: grain, Lookback: lookback[grain]}
	if partial {
		request.Through = anchor.Add(12 * time.Hour)
	}
	plan, err := buildingenergy.PlanTrend(request)
	if err != nil {
		return f06Fixture{}, buildingenergy.TrendPlan{}, err
	}

	// The synthetic manifest defines the same independent circuits throughout
	// this fixed historical fixture.
	f.effective.From = time.Date(2025, 1, 1, 0, 0, 0, 0, location)
	f.project.Meta.Effective = f.effective
	f.window.Range = plan.Current
	f.window.AsOf = plan.Current.To
	f.window.Granularity = buildingenergy.Hour
	return f, plan, nil
}

func calculateTrendSample(ctx context.Context, f f06Fixture, grain buildingenergy.Granularity, partial bool, scenario string) (f08Report, error) {
	f, plan, err := planTrendFixture(f, grain, partial)
	if err != nil {
		return f08Report{}, err
	}

	readPeriod := func(period buildingenergy.Range) (trendSamplePeriod, error) {
		if err := ctx.Err(); err != nil {
			return trendSamplePeriod{}, err
		}
		span := f
		span.window.Range = period
		catalog := span.classification()
		if scenario == "scope" && period.From.Equal(plan.Previous.From) {
			catalog.Meta.Version = "category-catalog-v0"
		}
		service := f06Service{fixture: span, read: func(point buildingenergy.PointRef, window buildingenergy.Window) buildingenergy.MeteringData {
			return span.generateMeter(point, window, func(at time.Time, base float64) (float64, buildingenergy.Quality) {
				return trendSample(at, base, plan.Previous, scenario)
			})
		}}
		calculated, err := service.classify(ctx, span.scope(), catalog)
		if err != nil {
			return trendSamplePeriod{}, err
		}
		report := calculated.report
		return trendSamplePeriod{report: report, value: f08Period{
			From: period.From, To: period.To, Hours: period.To.Sub(period.From).Hours(),
			f06Value: f06Measure(report.Energy[0].Total), MeterRuleVersion: calculated.inputs[0].Result.RuleVersion,
			ClassificationRuleVersion: report.RuleVersion, CatalogVersion: report.CatalogVersion.Version,
			TopologyVersion: report.TopologyVersion.Version,
		}}, nil
	}

	current, err := readPeriod(plan.Current)
	if err != nil {
		return f08Report{}, err
	}
	previous, err := readPeriod(plan.Previous)
	if err != nil {
		return f08Report{}, err
	}
	year, err := readPeriod(plan.PriorYear)
	if err != nil {
		return f08Report{}, err
	}
	comparison, err := buildingenergy.CompareClassifiedPeriods(plan, f.energy, current.report, previous.report, year.report)
	if err != nil {
		return f08Report{}, err
	}
	out := f08Report{
		Sample: true, Unit: current.report.Energy[0].Unit, Timezone: plan.Timezone,
		AsOf: current.report.Query.Window.AsOf, RuleVersion: buildingenergy.TrendRuleVersion, Partial: partial,
		Current:   current.value,
		Previous:  f08Result(previous.value, comparison.Previous, plan.PreviousAdjusted),
		PriorYear: f08Result(year.value, comparison.PriorYear, plan.PriorYearAdjusted),
		Trend:     make([]f08Period, 0, len(plan.Trend)),
	}
	for _, span := range plan.Trend {
		switch {
		case span.From.Equal(current.value.From) && span.To.Equal(current.value.To):
			out.Trend = append(out.Trend, current.value)
		case span.From.Equal(previous.value.From) && span.To.Equal(previous.value.To):
			out.Trend = append(out.Trend, previous.value)
		default:
			period, err := readPeriod(span)
			if err != nil {
				return f08Report{}, err
			}
			out.Trend = append(out.Trend, period.value)
		}
	}
	return out, nil
}

func f08Result(period f08Period, change buildingenergy.TrendComparison, adjusted bool) f08Comparison {
	return f08Comparison{Period: period, Status: change.Status, Delta: change.Delta, ChangeRate: change.ChangeRate, Adjusted: adjusted}
}

func trendSample(at time.Time, base float64, previous buildingenergy.Range, scenario string) (float64, buildingenergy.Quality) {
	if !at.Before(previous.From) && at.Before(previous.To) {
		switch scenario {
		case "missing":
			return 0, buildingenergy.Missing
		case "suspect":
			if at.Equal(previous.From) {
				return 987654, buildingenergy.Suspect
			}
		case "zero":
			return 0, buildingenergy.Normal
		}
	}
	if at.Year() == 2026 && at.Month() == time.October {
		return octoberSample(at, base)
	}
	if at.Year() == 2025 {
		base *= .625
	} else {
		base *= .875
	}
	return base * float64(at.Day()%4+1), buildingenergy.Normal
}
