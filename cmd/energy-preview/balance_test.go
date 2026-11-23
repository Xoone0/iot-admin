package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

func TestF09BalancePreview(t *testing.T) {
	const path = "/admin/v1/analytics/building-energy/preview/balance"
	tests := []struct {
		name, query, state, unit string
		parent                   float64
		childTotal, difference   *float64
		rate                     *float64
		issue                    string
	}{
		{"AT-02 separate levels", "office-a&electricity&day&standard", "unallocated", "kWh", 100, floatPtr(90), floatPtr(10), floatPtr(.1), ""},
		{"negative difference", "office-a&electricity&day&negative", "negative_difference", "kWh", 100, floatPtr(105), floatPtr(-5), floatPtr(-.05), ""},
		{"separate energy and week", "office-b&water&7&standard", "unallocated", "m³", 560, floatPtr(504), floatPtr(56), floatPtr(.1), ""},
		{"missing reading", "office-a&electricity&day&missing", "partial", "kWh", 100, nil, nil, nil, "missing_sample"},
		{"suspect reading", "office-a&electricity&day&suspect", "suspect_data", "kWh", 100, nil, nil, nil, "source_quality"},
		{"meter replacement", "office-a&electricity&day&replacement", "meter_change", "kWh", 100, nil, nil, nil, "epoch_change"},
		{"misaligned window", "office-a&electricity&day&time-mismatch", "time_mismatch", "kWh", 100, nil, nil, nil, "time_mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := strings.Split(test.query, "&")
			query := "building=" + params[0] + "&energy=" + params[1] + "&period=" + params[2] + "&scenario=" + params[3]
			req := httptest.NewRequest(http.MethodGet, path+"?"+query, nil)
			recorder := httptest.NewRecorder()
			previewHandler().ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
			}
			var response struct {
				Code int
				Data struct {
					Sample                                         bool
					Project, Building, Unit, State, From, To, AsOf string
					RuleVersion                                    string
					ChildTotal, Difference, DifferenceRate         *float64
					Topology                                       struct {
						Version, Source, From, To string
						UnmeteredZones            []string
					}
					Parent   balanceTestRow
					Children []balanceTestRow
				}
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			got := response.Data
			if response.Code != 0 || !got.Sample || got.Project != "f09-demo" || got.Building != params[0] || got.Unit != test.unit ||
				got.State != test.state || got.From == "" || got.To == "" || got.AsOf == "" || got.RuleVersion == "" {
				t.Fatalf("wrong scope/status: %+v", got)
			}
			if got.Topology.Version != "f09-topology-v1" || got.Topology.Source == "" || got.Topology.From == "" ||
				got.Topology.To == "" || len(got.Topology.UnmeteredZones) != 1 {
				t.Fatalf("missing proof metadata: %+v", got.Topology)
			}
			checkBalanceValue(t, "parent", got.Parent.Usage, floatPtr(test.parent))
			checkBalanceValue(t, "child total", got.ChildTotal, test.childTotal)
			checkBalanceValue(t, "difference", got.Difference, test.difference)
			checkBalanceValue(t, "difference rate", got.DifferenceRate, test.rate)
			if len(got.Children) != 2 || got.Parent.ID == got.Children[0].ID || got.Children[0].ID == got.Children[1].ID ||
				len(got.Parent.Regions) != 3 || len(got.Children[0].Regions) != 1 || len(got.Children[1].Regions) != 1 ||
				got.Children[0].Regions[0] == got.Children[1].Regions[0] {
				t.Fatalf("invalid separate meter boundaries: parent %+v, children %+v", got.Parent, got.Children)
			}
			for _, row := range append([]balanceTestRow{got.Parent}, got.Children...) {
				if row.Channel == "" || row.From == "" || row.To == "" || row.ProfileVersion == "" || row.BindingVersion == "" ||
					row.MappingGeneration == 0 || row.MeterRuleVersion == "" {
					t.Fatalf("incomplete evidence: %+v", row)
				}
				if row.ID != got.Children[0].ID && row.Coverage.Expected != row.Coverage.Valid {
					t.Fatalf("unperturbed meter quality: %+v", row)
				}
			}
			if test.issue != "" {
				issues := got.Children[0].Issues
				if len(issues) == 0 || issues[0].Reason != test.issue || issues[0].From == "" || issues[0].To == "" {
					t.Fatalf("wrong issue evidence: %+v", issues)
				}
			}
			if test.name == "AT-02 separate levels" {
				checkBalanceValue(t, "east branch", got.Children[0].Usage, floatPtr(70))
				checkBalanceValue(t, "west branch", got.Children[1].Usage, floatPtr(20))
				checkBalanceValue(t, "parent coverage", got.Parent.Coverage.Rate, floatPtr(1))
			}
			child := got.Children[0]
			switch test.issue {
			case "missing_sample":
				if child.Usage == nil || child.Coverage.Missing != 2 || child.Coverage.Suspect != 0 || child.Coverage.Valid != 22 {
					t.Fatalf("missing interval counts: %+v", child)
				}
			case "source_quality":
				if child.Usage == nil || child.Coverage.Missing != 0 || child.Coverage.Suspect != 2 || child.Coverage.Valid != 22 {
					t.Fatalf("suspect interval counts: %+v", child)
				}
			case "epoch_change":
				if child.Usage == nil || child.Coverage.Missing != 0 || child.Coverage.Suspect != 1 || child.Coverage.Valid != 23 {
					t.Fatalf("replacement interval counts: %+v", child)
				}
			case "time_mismatch":
				if child.Usage != nil || child.Coverage.Rate != nil || child.To == got.To || child.AsOf != got.AsOf {
					t.Fatalf("misaligned child source metadata: %+v", child)
				}
			}
		})
	}
}

type balanceTestRow struct {
	ID, State, Channel, ProfileVersion, BindingVersion, MeterRuleVersion, From, To, AsOf string
	Regions                                                                              []string
	Usage                                                                                *float64
	MappingGeneration                                                                    uint64
	Coverage                                                                             struct {
		Expected, Valid, Missing, Suspect int
		Rate                              *float64
	}
	Issues []struct{ Reason, From, To string }
}

func TestF09BalanceScopeBeforeMeterRead(t *testing.T) {
	f, err := newBalanceSample("building=office-a&energy=electricity&period=day&scenario=standard")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*buildingenergy.Scope)
	}{
		{"other project", func(s *buildingenergy.Scope) { s.Project.ProjectID = "f06-demo" }},
		{"other building", func(s *buildingenergy.Scope) { s.Objects[0].ID = "office-b" }},
		{"wrong point", func(s *buildingenergy.Scope) { s.Points[1].TerminalID = "office-b-electricity-east" }},
		{"unscoped parent", func(s *buildingenergy.Scope) { s.Points = s.Points[1:] }},
		{"expanded window", func(s *buildingenergy.Scope) { s.Period.To = s.Period.To.Add(time.Hour) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope := f.scope()
			tt.edit(&scope)
			_, err := f.calculate(context.Background(), scope, func(buildingenergy.BalanceMeter, buildingenergy.Window) (buildingenergy.MeteringData, buildingenergy.MappingTimeline) {
				t.Fatal("metering read before authorization")
				return buildingenergy.MeteringData{}, buildingenergy.MappingTimeline{}
			})
			if !errors.Is(err, buildingenergy.ErrAccessDenied) {
				t.Fatalf("expected access denied, got %v", err)
			}
		})
	}
}

func floatPtr(value float64) *float64 { return &value }

func checkBalanceValue(t *testing.T, field string, actual, expected *float64) {
	t.Helper()
	if actual == nil || expected == nil {
		if actual != expected {
			t.Fatalf("%s = %v, expected %v", field, actual, expected)
		}
		return
	}
	if *actual != *expected {
		t.Fatalf("%s = %v, expected %v", field, *actual, *expected)
	}
}

func TestF09BalanceRejectsUnsupportedQuery(t *testing.T) {
	const path = "/admin/v1/analytics/building-energy/preview/balance"
	const valid = "building=office-a&energy=electricity&period=day&scenario=standard"
	for _, query := range []string{
		"", valid + "&building=office-b", valid + "&point=private-meter", valid + "&scope=all",
		strings.Replace(valid, "office-a", "all", 1), strings.Replace(valid, "electricity", "steam", 1),
		strings.Replace(valid, "day", "hour", 1), strings.Replace(valid, "standard", "unknown", 1),
	} {
		request := httptest.NewRequest(http.MethodGet, path+"?"+query, nil)
		recorder := httptest.NewRecorder()
		previewHandler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("query %q got status %d: %s", query, recorder.Code, recorder.Body)
		}
	}
}
