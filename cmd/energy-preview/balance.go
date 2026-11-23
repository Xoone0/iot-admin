package main

import (
	"context"
	"net/http"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/admin"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const balancePath = overviewPath + "/balance"

type balancePreview struct {
	Sample         bool                         `json:"sample"`
	Project        string                       `json:"project"`
	Building       string                       `json:"building"`
	Energy         buildingenergy.EnergyType    `json:"energy"`
	Unit           string                       `json:"unit"`
	Timezone       string                       `json:"timezone"`
	From           time.Time                    `json:"from"`
	To             time.Time                    `json:"to"`
	AsOf           time.Time                    `json:"asOf"`
	RuleVersion    string                       `json:"ruleVersion"`
	Topology       balanceTopologyEvidence      `json:"topology"`
	State          buildingenergy.BalanceStatus `json:"state"`
	Parent         balanceMeterEvidence         `json:"parent"`
	Children       []balanceMeterEvidence       `json:"children"`
	ChildTotal     *float64                     `json:"childTotal"`
	Difference     *float64                     `json:"difference"`
	DifferenceRate *float64                     `json:"differenceRate"`
}

type balanceTopologyEvidence struct {
	Version        string    `json:"version"`
	Source         string    `json:"source"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	UnmeteredZones []string  `json:"unmeteredZones"`
}

type balanceMeterEvidence struct {
	ID                string                       `json:"id"`
	Name              string                       `json:"name"`
	Channel           string                       `json:"channel"`
	Regions           []string                     `json:"regions"`
	Usage             *float64                     `json:"usage"`
	State             buildingenergy.BalanceStatus `json:"state"`
	Coverage          balanceCoverage              `json:"coverage"`
	From              time.Time                    `json:"from"`
	To                time.Time                    `json:"to"`
	AsOf              time.Time                    `json:"asOf"`
	ProfileVersion    string                       `json:"profileVersion"`
	MappingGeneration uint64                       `json:"mappingGeneration"`
	BindingVersion    string                       `json:"bindingVersion"`
	MeterRuleVersion  string                       `json:"meterRuleVersion"`
	Issues            []balanceMeterIssue          `json:"issues"`
}

type balanceCoverage struct {
	buildingenergy.Coverage
	Rate *float64 `json:"rate"`
}

type balanceMeterIssue struct {
	Reason string    `json:"reason"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

type balanceMeterReader func(buildingenergy.BalanceMeter, buildingenergy.Window) (buildingenergy.MeteringData, buildingenergy.MappingTimeline)

func sampleBalance(w http.ResponseWriter, r *http.Request) {
	fixture, err := newBalanceSample(r.URL.RawQuery)
	if err != nil {
		writeBusinessUsageError(w, err)
		return
	}
	report, err := fixture.calculate(r.Context(), fixture.scope(), fixture.readMeter)
	if err != nil {
		writeBusinessUsageError(w, err)
		return
	}
	writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: report})
}

func (f balanceSample) calculate(ctx context.Context, scope buildingenergy.Scope, read balanceMeterReader) (balancePreview, error) {
	if _, err := buildingenergy.ResolveScope(ctx, f, f, f, buildingenergy.ReadUsage, scope); err != nil {
		return balancePreview{}, err
	}
	meters := f.meters()
	inputs := make([]buildingenergy.CategoryPointResult, 0, len(meters))
	windows := make(map[buildingenergy.PointRef]buildingenergy.Window, len(meters))
	for _, meter := range meters {
		if err := ctx.Err(); err != nil {
			return balancePreview{}, err
		}
		window := f.window
		if f.scenario == "time-mismatch" && meter.Point.Key == f.perturbed {
			// Still inside the authorized period. A short export is an actual
			// different data window, so it cannot be compared with the totals.
			window.To = window.To.Add(-time.Hour)
			window.Granularity = buildingenergy.Hour
		}
		data, mapping := read(meter, window)
		result, err := buildingenergy.Calculate(data, window)
		if err != nil {
			return balancePreview{}, err
		}
		windows[meter.Point] = result.Window
		inputs = append(inputs, buildingenergy.CategoryPointResult{Result: result, Mapping: mapping})
	}

	query := buildingenergy.BalanceQuery{Project: scope.Project, Object: f.object, Energy: f.topology.Energy, Window: f.window}
	result, err := buildingenergy.CalculateBalance(query, f.topology, inputs)
	if err != nil {
		return balancePreview{}, err
	}
	report := balancePreview{
		Sample: true, Project: scope.Project.ProjectID, Building: f.object.ID, Energy: query.Energy,
		Unit: result.Unit, Timezone: f.window.Timezone, From: f.window.From, To: f.window.To, AsOf: f.window.AsOf.UTC(),
		RuleVersion: result.RuleVersion, State: result.Status,
		Topology: balanceTopologyEvidence{
			Version: result.Topology.Version, Source: result.Topology.Source.ID, UnmeteredZones: f.unmetered,
			From: result.Topology.Effective.From, To: result.Topology.Effective.To,
		},
		Parent:     balanceMeterProjection(result.Parent, windows[result.Parent.Meter.Point]),
		Children:   make([]balanceMeterEvidence, 0, len(result.Children)),
		ChildTotal: result.ChildrenUsage, Difference: result.Difference, DifferenceRate: result.DifferenceRate,
	}
	for _, child := range result.Children {
		report.Children = append(report.Children, balanceMeterProjection(child, windows[child.Meter.Point]))
	}
	return report, nil
}

func balanceMeterProjection(result buildingenergy.BalanceMeterResult, window buildingenergy.Window) balanceMeterEvidence {
	meter := result.Meter
	evidence := balanceMeterEvidence{
		ID: meter.Point.Key.TerminalID, Name: meter.Name, Channel: meter.Channel.ID, Regions: meter.Zones,
		Usage: result.Usage, State: result.Status, Coverage: balanceCoverage{Coverage: result.Coverage},
		From: window.From, To: window.To, AsOf: window.AsOf, ProfileVersion: result.ProfileVersion,
		MappingGeneration: meter.MappingGeneration, BindingVersion: result.BindingVersion, MeterRuleVersion: result.MeterRuleVersion,
		Issues: []balanceMeterIssue{},
	}
	if result.Coverage.Expected > 0 {
		rate := result.Coverage.CoverageRate()
		evidence.Coverage.Rate = &rate
	}
	for _, interval := range result.Intervals {
		if interval.Reason != "" {
			evidence.Issues = append(evidence.Issues, balanceMeterIssue{Reason: string(interval.Reason), From: interval.From, To: interval.To})
		}
	}
	if result.Status == "time_mismatch" {
		evidence.Issues = append(evidence.Issues, balanceMeterIssue{Reason: "time_mismatch", From: window.From, To: window.To})
	}
	return evidence
}
