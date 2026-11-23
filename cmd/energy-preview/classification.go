package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/admin"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const classificationPath = "/admin/v1/analytics/building-energy/preview/classification"

type f06Coverage struct {
	Expected float64  `json:"expected"`
	Valid    float64  `json:"valid"`
	Missing  float64  `json:"missing"`
	Suspect  float64  `json:"suspect"`
	Rate     *float64 `json:"rate"`
}

type f06Value struct {
	Usage    *float64    `json:"usage"`
	Coverage f06Coverage `json:"coverage"`
}

type f06Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	f06Value
	Children []f06Category `json:"children,omitempty"`
}

type f06Share struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Usage *float64 `json:"usage"`
	Share *float64 `json:"share"`
}

type f06Day struct {
	From         time.Time `json:"from"`
	Total        f06Value  `json:"total"`
	Classified   *float64  `json:"classified"`
	Unclassified *float64  `json:"unclassified"`
}

// f06Source is a narrow read-only projection for the selected synthetic scope.
// Source identifiers refer to known fixture channels, never raw sample records.
type f06Source struct {
	Name                   string         `json:"name"`
	Source                 string         `json:"source"`
	MappingGeneration      uint64         `json:"mappingGeneration"`
	ProfileVersion         string         `json:"profileVersion"`
	BindingVersion         string         `json:"bindingVersion"`
	CategoryBindingVersion string         `json:"categoryBindingVersion,omitempty"`
	Observations           int            `json:"observations"`
	Usage                  *float64       `json:"usage"`
	Valid                  int            `json:"valid"`
	Missing                int            `json:"missing"`
	Suspect                int            `json:"suspect"`
	Breakdown              []f06SourceUse `json:"breakdown"`
}

type f06SourceUse struct {
	Name  string   `json:"name"`
	Usage *float64 `json:"usage"`
}

type f06Report struct {
	Sample                    bool          `json:"sample"`
	Unit                      string        `json:"unit"`
	Timezone                  string        `json:"timezone"`
	From                      time.Time     `json:"from"`
	To                        time.Time     `json:"to"`
	AsOf                      time.Time     `json:"asOf"`
	Total                     f06Value      `json:"total"`
	Classified                *float64      `json:"classified"`
	Unclassified              f06Value      `json:"unclassified"`
	Categories                []f06Category `json:"categories"`
	Composition               []f06Share    `json:"composition"`
	Trend                     []f06Day      `json:"trend"`
	Sources                   []f06Source   `json:"sources"`
	RuleVersion               string        `json:"ruleVersion"`
	ClassificationRuleVersion string        `json:"classificationRuleVersion"`
	CatalogVersion            string        `json:"catalogVersion"`
	TopologyVersion           string        `json:"topologyVersion"`
	TopologySource            string        `json:"topologySource"`
}

func sampleClassification(w http.ResponseWriter, r *http.Request) {
	query, queryErr := url.ParseQuery(r.URL.RawQuery)
	period := query.Get("period")
	days, periodErr := strconv.Atoi(period)
	if period == "day" {
		days, periodErr = 7, nil
	}
	if queryErr != nil || periodErr != nil || len(query) != 3 || len(query["building"]) != 1 || len(query["energy"]) != 1 || len(query["period"]) != 1 {
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "仅支持样本建筑、能源和时间范围"})
		return
	}
	fixture, err := newF06Fixture(query.Get("building"), buildingenergy.EnergyType(query.Get("energy")), days)
	if err == nil && period == "day" {
		fixture, _, err = planTrendFixture(fixture, buildingenergy.Day, false)
		fixture.window.Granularity = buildingenergy.Day
	}
	if err != nil {
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "无效的样本筛选条件"})
		return
	}
	service := f06Service{fixture: fixture, read: fixture.readMeter}
	report, err := service.calculate(r.Context(), fixture.scope())
	if errors.Is(err, buildingenergy.ErrAccessDenied) {
		writeResponse(w, http.StatusForbidden, admin.Response{Code: 1, Message: "样本范围未授权"})
		return
	}
	if err != nil {
		log.Printf("calculate classification preview: %v", err)
		writeResponse(w, http.StatusInternalServerError, admin.Response{Code: 1, Message: "样本计算失败"})
		return
	}
	writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: report})
}

type f06Service struct {
	fixture f06Fixture
	read    func(buildingenergy.PointRef, buildingenergy.Window) buildingenergy.MeteringData
}

type sampleClassificationResult struct {
	report   buildingenergy.ClassifiedUsageReport
	inputs   []buildingenergy.CategoryPointResult
	topology buildingenergy.CategoryTopologySnapshot
}

func (s f06Service) classify(ctx context.Context, scope buildingenergy.Scope, catalog buildingenergy.ClassificationSnapshot) (sampleClassificationResult, error) {
	f := s.fixture
	resolved, err := buildingenergy.ResolveScope(ctx, f, f, f, buildingenergy.ReadUsage, scope)
	if err != nil {
		return sampleClassificationResult{}, err
	}
	topology, err := f.checkedTopology(f.syntheticCertificate())
	if err != nil {
		return sampleClassificationResult{}, err
	}
	query := buildingenergy.ClassifiedUsageQuery{Project: resolved.Project.Key, Window: f.window, Object: resolved.Objects[0].Key}
	inputs := make([]buildingenergy.CategoryPointResult, 0, len(scope.Points))
	for _, id := range scope.Points {
		point := buildingenergy.PointRef{Project: query.Project, Key: id}
		result, err := buildingenergy.Calculate(s.read(point, query.Window), query.Window)
		if err != nil {
			return sampleClassificationResult{}, err
		}
		inputs = append(inputs, buildingenergy.CategoryPointResult{Result: result, Mapping: f.mapping(point)})
	}
	report, err := buildingenergy.CalculateClassifiedUsage(query, catalog, topology, inputs)
	return sampleClassificationResult{report: report, inputs: inputs, topology: topology}, err
}

func (s f06Service) calculate(ctx context.Context, scope buildingenergy.Scope) (f06Report, error) {
	f := s.fixture
	catalog := f.classification()
	calculated, err := s.classify(ctx, scope, catalog)
	if err != nil {
		return f06Report{}, err
	}
	report, inputs, topology := calculated.report, calculated.inputs, calculated.topology
	query := report.Query
	summary := report.Energy[0]
	result := f06Report{
		Sample: true, Unit: summary.Unit, Timezone: query.Window.Timezone,
		From: query.Window.From, To: query.Window.To, AsOf: f.window.AsOf,
		Total: f06Measure(summary.Total), Classified: f06Classified(summary), Unclassified: f06Measure(summary.Unclassified),
		Categories: f06Tree(summary.Categories), Composition: f06Composition(summary), Sources: f.projectSources(query, report),
		RuleVersion: inputs[0].Result.RuleVersion, ClassificationRuleVersion: report.RuleVersion, CatalogVersion: report.CatalogVersion.Version,
		TopologyVersion: report.TopologyVersion.Version, TopologySource: report.TopologyVersion.Source.ID,
	}

	// Reuse M1-A's calendar buckets and normalized interval traces. Each day
	// goes through the same M1-C engine and retains the report's cutoff.
	offset := 0
	for _, bucket := range inputs[0].Result.Buckets {
		daily := query
		daily.Window.Range = buildingenergy.Range{From: bucket.From, To: bucket.To}
		dayInputs := make([]buildingenergy.CategoryPointResult, 0, len(inputs))
		for _, point := range inputs {
			point.Result.Window = daily.Window
			point.Result.Intervals = point.Result.Intervals[offset : offset+bucket.Expected]
			dayInputs = append(dayInputs, point)
		}
		day, err := buildingenergy.CalculateClassifiedUsage(daily, catalog, topology, dayInputs)
		if err != nil {
			return f06Report{}, err
		}
		energy := day.Energy[0]
		result.Trend = append(result.Trend, f06Day{
			From: bucket.From, Total: f06Measure(energy.Total), Classified: f06Classified(energy), Unclassified: energy.Unclassified.Usage,
		})
		offset += bucket.Expected
	}
	return result, nil
}

func f06Measure(value buildingenergy.CategoryValue) f06Value {
	c := value.Coverage
	out := f06Coverage{Expected: c.Expected, Valid: c.Valid, Missing: c.Missing, Suspect: c.Suspect}
	if c.Expected > 0 {
		rate := c.Valid / c.Expected
		out.Rate = &rate
	}
	return f06Value{Usage: value.Usage, Coverage: out}
}

func f06Tree(rows []buildingenergy.CategoryRow) []f06Category {
	result := make([]f06Category, 0, len(rows))
	for _, row := range rows {
		result = append(result, f06Category{
			ID: row.Category.ID, Name: row.Category.Name, f06Value: f06Measure(row.CategoryValue), Children: f06Tree(row.Children),
		})
	}
	return result
}

func f06Classified(summary buildingenergy.EnergyCategorySummary) *float64 {
	var usage *float64
	for _, row := range summary.Categories {
		addF06Usage(&usage, row.Usage)
	}
	return usage
}

func addF06Usage(total **float64, value *float64) {
	if value == nil {
		return
	}
	if *total == nil {
		*total = new(float64)
	}
	**total += *value
}

func f06Composition(summary buildingenergy.EnergyCategorySummary) []f06Share {
	entries := make([]f06Share, 0, len(summary.Categories)+1)
	for _, row := range summary.Categories {
		entries = append(entries, f06Share{ID: row.Category.ID, Name: row.Category.Name, Usage: row.Usage})
	}
	entries = append(entries, f06Share{ID: "unclassified", Name: "未分项", Usage: summary.Unclassified.Usage})
	for i, entry := range entries {
		if summary.Total.Usage != nil && *summary.Total.Usage > 0 && entry.Usage != nil {
			share := *entry.Usage / *summary.Total.Usage
			entries[i].Share = &share
		}
	}
	return entries
}

func (f f06Fixture) projectSources(query buildingenergy.ClassifiedUsageQuery, report buildingenergy.ClassifiedUsageReport) []f06Source {
	sources := make([]f06Source, 0, len(f.meters))
	reported := report.Query
	if reported.Project != query.Project || reported.Object != query.Object ||
		reported.Window.Timezone != query.Window.Timezone || reported.Window.Granularity != query.Window.Granularity ||
		!reported.Window.From.Equal(query.Window.From) || !reported.Window.To.Equal(query.Window.To) ||
		!reported.Window.AsOf.Equal(query.Window.AsOf) {
		return sources
	}
	for _, meter := range f.meters {
		source := f06Source{Name: meter.name, Source: f.channel(meter.key).ID}
		categorized := make(map[string]*float64)
		var remainder *float64
		for _, evidence := range report.Intervals {
			if evidence.Point.Project != query.Project || evidence.Point.Key != meter.key ||
				evidence.Trace.From.Before(query.Window.From) || evidence.Trace.To.After(query.Window.To) {
				continue
			}
			source.MappingGeneration = evidence.MappingGeneration
			source.ProfileVersion = evidence.ProfileVersion
			source.BindingVersion = evidence.ObjectBindingVersion
			source.CategoryBindingVersion = evidence.CategoryBindingVersion
			source.Observations++
			switch {
			case evidence.Trace.Usage != nil:
				source.Valid++
			case evidence.Trace.Quality == buildingenergy.Missing:
				source.Missing++
			default:
				source.Suspect++
			}
			addF06Usage(&source.Usage, evidence.Trace.Usage)
			for _, contribution := range evidence.Contributions {
				total := categorized[contribution.CategoryID]
				addF06Usage(&total, contribution.Usage)
				categorized[contribution.CategoryID] = total
			}
			addF06Usage(&remainder, evidence.UnclassifiedUsage)
		}
		if source.Observations > 0 {
			for _, node := range f.nodes {
				if usage, assigned := categorized[node.ID]; assigned {
					source.Breakdown = append(source.Breakdown, f06SourceUse{Name: node.Name, Usage: usage})
				}
			}
			if meter.categoryShare < buildingenergy.FullWeightPPM {
				source.Breakdown = append(source.Breakdown, f06SourceUse{Name: "未分项", Usage: remainder})
			}
			sources = append(sources, source)
		}
	}
	return sources
}
