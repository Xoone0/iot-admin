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

const anomaliesPath = overviewPath + "/anomalies"
const anomalyHistoryDays = 3

type anomalySource struct {
	Usage                  *float64 `json:"usage"`
	Name                   string   `json:"name"`
	Source                 string   `json:"source"`
	ProfileVersion         string   `json:"profileVersion"`
	BindingVersion         string   `json:"bindingVersion"`
	CategoryBindingVersion string   `json:"categoryBindingVersion,omitempty"`
	MappingGeneration      uint64   `json:"mappingGeneration"`
	Valid                  int      `json:"valid"`
	Missing                int      `json:"missing"`
	Suspect                int      `json:"suspect"`
}

type anomalyPeriod struct {
	f08Period
	Sources []anomalySource `json:"sources"`
}

type anomalyRuleView struct {
	ID            string    `json:"id"`
	Version       string    `json:"version"`
	Source        string    `json:"source"`
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	MinHistory    int       `json:"minHistory"`
	MinChangeRate float64   `json:"minChangeRate"`
	MinIncrease   float64   `json:"minIncrease"`
}

type anomalyFinding struct {
	Status        buildingenergy.AnomalyStatus `json:"status"`
	UsageStatus   buildingenergy.AnomalyStatus `json:"usageStatus"`
	Reason        buildingenergy.AnomalyReason `json:"reason"`
	BaselineCount int                          `json:"baselineCount"`
	Baseline      *float64                     `json:"baseline"`
	Delta         *float64                     `json:"delta"`
	ChangeRate    *float64                     `json:"changeRate"`
}

type energyOpportunity struct {
	Kind            buildingenergy.OpportunityKind   `json:"kind"`
	Status          buildingenergy.OpportunityStatus `json:"status"`
	Object          string                           `json:"object"`
	Name            string                           `json:"name"`
	Usage           *float64                         `json:"usage"`
	Baseline        *float64                         `json:"baseline"`
	Evidence        string                           `json:"evidence"`
	Limitations     []string                         `json:"limitations"`
	Confirm         []string                         `json:"confirm"`
	Sources         []anomalySource                  `json:"sources"`
	ComparablePeers int                              `json:"comparablePeers"`
	Measurement     *opportunityMeasurement          `json:"measurement,omitempty"`
}

type opportunityMeasurement struct {
	RuleVersion               string `json:"ruleVersion"`
	MeterRuleVersion          string `json:"meterRuleVersion"`
	ClassificationRuleVersion string `json:"classificationRuleVersion"`
	CatalogVersion            string `json:"catalogVersion"`
	TopologyVersion           string `json:"topologyVersion"`
}

type anomalyPreview struct {
	Sample        bool                      `json:"sample"`
	Project       string                    `json:"project"`
	Building      string                    `json:"building"`
	Energy        buildingenergy.EnergyType `json:"energy"`
	Scenario      string                    `json:"scenario"`
	Unit          string                    `json:"unit"`
	Timezone      string                    `json:"timezone"`
	AsOf          time.Time                 `json:"asOf"`
	Current       anomalyPeriod             `json:"current"`
	History       []anomalyPeriod           `json:"history"`
	Rule          *anomalyRuleView          `json:"rule"`
	Finding       anomalyFinding            `json:"finding"`
	Opportunities []energyOpportunity       `json:"opportunities"`
}

func sampleAnomalies(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 3 || len(query["building"]) != 1 || len(query["energy"]) != 1 ||
		len(query["scenario"]) != 1 || !supportedAnomalySample(query.Get("scenario")) {
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "仅支持样本建筑、能源和异常场景"})
		return
	}
	fixture, err := newF06Fixture(query.Get("building"), buildingenergy.EnergyType(query.Get("energy")), 7)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "无效的样本筛选条件"})
		return
	}
	report, err := calculateAnomalySample(r.Context(), fixture, query.Get("scenario"))
	if errors.Is(err, buildingenergy.ErrAccessDenied) {
		writeResponse(w, http.StatusForbidden, admin.Response{Code: 1, Message: "样本范围未授权"})
		return
	}
	if err != nil {
		log.Printf("calculate anomaly preview: %v", err)
		writeResponse(w, http.StatusInternalServerError, admin.Response{Code: 1, Message: "异常候选样本计算失败"})
		return
	}
	writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: report})
}

func supportedAnomalySample(scenario string) bool {
	switch scenario {
	case "standard", "normal", "missing", "suspect", "partial", "zero", "incomparable", "baseline-zero", "no-rule":
		return true
	default:
		return false
	}
}

func calculateAnomalySample(ctx context.Context, seed f06Fixture, scenario string) (anomalyPreview, error) {
	fixture, calendar, err := planTrendFixture(seed, buildingenergy.Day, false)
	if err != nil {
		return anomalyPreview{}, err
	}
	out := anomalyPreview{
		Sample: true, Project: fixture.project.Key.ProjectID, Building: fixture.object.ID, Energy: fixture.energy,
		Scenario: scenario, Unit: fixture.unit, Timezone: fixture.project.Timezone, AsOf: fixture.window.AsOf,
		History: make([]anomalyPeriod, 0, anomalyHistoryDays), Opportunities: []energyOpportunity{},
	}
	read := func(span f06Fixture) (buildingenergy.ClassifiedUsageReport, anomalyPeriod, error) {
		if err := ctx.Err(); err != nil {
			return buildingenergy.ClassifiedUsageReport{}, anomalyPeriod{}, err
		}
		isCurrent := span.window.From.Equal(calendar.Current.From)
		catalog := span.classification()
		if scenario == "incomparable" && span.window.From.Equal(calendar.Current.From.AddDate(0, 0, -7)) {
			catalog.Meta.Version = "category-catalog-v0"
		}
		service := f06Service{fixture: span, read: func(point buildingenergy.PointRef, window buildingenergy.Window) buildingenergy.MeteringData {
			return span.generateMeter(point, window, func(at time.Time, base float64) (float64, buildingenergy.Quality) {
				return anomalySample(at, base, calendar, isCurrent, scenario)
			})
		}}
		result, err := service.classify(ctx, span.scope(), catalog)
		if err != nil {
			return buildingenergy.ClassifiedUsageReport{}, anomalyPeriod{}, err
		}
		report := result.report
		period := anomalyPeriod{f08Period: f08Period{
			From: report.Query.Window.From, To: report.Query.Window.To, Hours: span.window.To.Sub(span.window.From).Hours(),
			f06Value: f06Measure(report.Energy[0].Total), MeterRuleVersion: result.inputs[0].Result.RuleVersion,
			ClassificationRuleVersion: report.RuleVersion, CatalogVersion: report.CatalogVersion.Version,
			TopologyVersion: report.TopologyVersion.Version,
		}, Sources: make([]anomalySource, 0, len(span.meters))}
		for _, source := range span.projectSources(report.Query, report) {
			period.Sources = append(period.Sources, anomalySource{
				Name: source.Name, Source: source.Source, ProfileVersion: source.ProfileVersion, BindingVersion: source.BindingVersion,
				CategoryBindingVersion: source.CategoryBindingVersion,
				MappingGeneration:      source.MappingGeneration, Valid: source.Valid, Missing: source.Missing, Suspect: source.Suspect, Usage: source.Usage,
			})
		}
		return report, period, nil
	}

	current, measured, err := read(fixture)
	if err != nil {
		return anomalyPreview{}, err
	}
	out.Current = measured
	history := make([]buildingenergy.ClassifiedUsageReport, 0, anomalyHistoryDays)
	for week := anomalyHistoryDays; week > 0; week-- {
		span := fixture
		span.window.Range = buildingenergy.Range{From: calendar.Current.From.AddDate(0, 0, -7*week), To: calendar.Current.To.AddDate(0, 0, -7*week)}
		report, evidence, err := read(span)
		if err != nil {
			return anomalyPreview{}, err
		}
		history = append(history, report)
		out.History = append(out.History, evidence)
	}
	var rule *buildingenergy.AnomalyRule
	if scenario != "no-rule" {
		rule = anomalySampleRule(fixture)
		out.Rule = &anomalyRuleView{
			ID: "similar-weekday-increase", Version: rule.Meta.Version, Source: rule.Meta.Source.ID,
			From: rule.Meta.Effective.From, To: rule.Meta.Effective.To, MinHistory: rule.MinimumHistory,
			MinChangeRate: rule.MinimumRelativeIncrease, MinIncrease: rule.MinimumAbsoluteIncrease,
		}
	}
	finding, err := buildingenergy.EvaluateUsageAnomaly(fixture.energy, rule, current, history)
	if err != nil {
		return anomalyPreview{}, err
	}
	usageStatus := finding.Status
	if usageStatus == buildingenergy.AnomalyMeteringAnomaly {
		usageStatus = buildingenergy.AnomalyInsufficientEvidence
	}
	out.Finding = anomalyFinding{Status: finding.Status, UsageStatus: usageStatus, Reason: finding.Reason, BaselineCount: finding.BaselineCount,
		Baseline: finding.Baseline, Delta: finding.Delta, ChangeRate: finding.ChangeRate}
	out.Opportunities, err = projectAnomalyOpportunities(ctx, fixture, scenario, finding, measured.Sources)
	return out, err
}

// Anomaly scenarios only mutate synthetic interval input or its catalog version.
// Standard reuses the F08 complete day exactly.
func anomalySample(at time.Time, base float64, calendar buildingenergy.TrendPlan, current bool, scenario string) (float64, buildingenergy.Quality) {
	if current {
		switch scenario {
		case "normal":
			return base, buildingenergy.Normal
		case "missing":
			return 0, buildingenergy.Missing
		case "partial":
			if at.Equal(calendar.Current.From) {
				return 0, buildingenergy.Missing
			}
		case "suspect":
			if at.Equal(calendar.Current.From) {
				return 987654, buildingenergy.Suspect
			}
		case "zero":
			return 0, buildingenergy.Normal
		}
	} else if scenario == "baseline-zero" {
		return 0, buildingenergy.Normal
	}
	return trendSample(at, base, calendar.Previous, "standard")
}

func anomalySampleRule(fixture f06Fixture) *buildingenergy.AnomalyRule {
	minRate, minIncrease := .6, 120.0
	if fixture.energy == buildingenergy.Water {
		minRate, minIncrease = .4, 30
	}
	if fixture.object.ID == "office-b" {
		minIncrease *= 2
	}
	return &buildingenergy.AnomalyRule{
		Project: fixture.project.Key, Object: fixture.object, Energy: fixture.energy,
		Meta:           f06Meta("sample-increase-"+fixture.object.ID+"-"+string(fixture.energy)+"-v1", "synthetic-anomaly-rules", fixture.effective),
		MinimumHistory: anomalyHistoryDays, MinimumAbsoluteIncrease: minIncrease, MinimumRelativeIncrease: minRate,
	}
}

func projectAnomalyOpportunities(ctx context.Context, fixture f06Fixture, scenario string, finding buildingenergy.AnomalyEvaluation,
	sources []anomalySource) ([]energyOpportunity, error) {
	peers := buildingenergy.OpportunityPeers{}
	rows := make(map[buildingenergy.ObjectKey]f07Row)
	var ranking f07Report
	// Only these scenarios retain the exact same current readings as F16.
	// Removing the F10 rule leaves this independent observation eligible.
	if scenario == "standard" || scenario == "no-rule" {
		dimensions, err := newDimensionsFixture(buildingenergy.Space, fixture.object.ID, fixture.energy, "day", "complete", "standard")
		if err != nil {
			return nil, err
		}
		scope := dimensions.scope()
		report, err := dimensions.calculate(ctx, scope)
		if err != nil {
			return nil, err
		}
		ranking = report
		peers = buildingenergy.OpportunityPeers{Project: scope.Project, Object: fixture.object, Energy: fixture.energy, Unit: report.Unit,
			Window: buildingenergy.Window{Range: buildingenergy.Range{From: report.From, To: report.To}, Timezone: report.Timezone,
				AsOf: report.AsOf, Granularity: dimensions.buildings[0].window.Granularity}}
		for _, row := range report.Rows {
			object := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: row.ID}
			coverage := row.Coverage
			peers.Rows = append(peers.Rows, buildingenergy.OpportunityPeer{Object: object, Status: row.Status, Rank: row.Rank,
				Value: buildingenergy.CategoryValue{Usage: row.Usage, Coverage: buildingenergy.WeightedCoverage{
					Expected: coverage.Expected, Valid: coverage.Valid, Missing: coverage.Missing, Suspect: coverage.Suspect,
				}}})
			rows[object] = row
		}
	}
	out := []energyOpportunity{}
	for _, clue := range buildingenergy.BuildEnergyOpportunities(finding, peers) {
		name, evidence := anomalyBuildingName(fixture.object.ID), sources
		var measurement *opportunityMeasurement
		if row, scoped := rows[clue.Object]; scoped {
			name = row.Name
			evidence = projectAnomalyPeerSources(row.Sources, sources)
			if len(row.Sources) > 0 {
				measurement = &opportunityMeasurement{
					RuleVersion: ranking.RuleVersion, MeterRuleVersion: row.Sources[0].MeterRuleVersion,
					ClassificationRuleVersion: ranking.ClassificationRuleVersion, CatalogVersion: ranking.CatalogVersion, TopologyVersion: ranking.TopologyVersion,
				}
			}
		}
		out = append(out, energyOpportunity{
			Kind: clue.Kind, Status: clue.Status, Object: clue.Object.ID, Name: name, Usage: clue.Usage, Baseline: clue.Baseline,
			Evidence: clue.Evidence, Limitations: clue.Limitations, Confirm: clue.Confirm, Sources: evidence, ComparablePeers: clue.ComparablePeers, Measurement: measurement,
		})
	}
	return out, nil
}

func anomalyBuildingName(id string) string {
	if id == "office-b" {
		return "办公楼 B"
	}
	return "办公楼 A"
}

func projectAnomalyPeerSources(rows []f07Source, buildingSources []anomalySource) []anomalySource {
	out := make([]anomalySource, 0, len(rows))
	for _, row := range rows {
		for _, source := range buildingSources {
			if row.Channel == source.Source {
				out = append(out, anomalySource{
					Name: source.Name, Source: row.Channel, ProfileVersion: row.ProfileVersion, BindingVersion: row.ObjectBindingVersion,
					CategoryBindingVersion: row.CategoryBindingVersion,
					MappingGeneration:      row.MappingGeneration, Valid: row.Valid, Missing: row.Missing, Suspect: row.Suspect, Usage: row.Usage,
				})
				break
			}
		}
	}
	return out
}
