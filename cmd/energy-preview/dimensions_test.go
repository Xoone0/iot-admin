package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

func readDimensions(t *testing.T, view, building, period, scenario string) f07Report {
	t.Helper()
	query := "?view=" + view + "&building=" + building + "&energy=electricity&period=" + period + "&mode=complete&scenario=" + scenario
	return readDimensionQuery(t, query)
}

func readDimensionQuery(t *testing.T, query string) f07Report {
	t.Helper()
	res := httptest.NewRecorder()
	previewHandler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, dimensionsPath+query, nil))
	var payload struct{ Data f07Report }
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil || res.Code != http.StatusOK {
		t.Fatalf("dimension preview status %d (%v): %s", res.Code, err, res.Body)
	}
	return payload.Data
}

func TestF07MonthToDateUsesF08CutoffAndFullMonthQualityWithholdsRanks(t *testing.T) {
	data := readDimensions(t, "building", "all", "month", "standard")
	if len(data.Rows) != 2 {
		t.Fatalf("wrong full-month cohort: %+v", data)
	}
	for _, row := range data.Rows {
		if row.Rank != nil || row.Status != buildingenergy.TrendInsufficientQuality ||
			row.Coverage.Expected != 2232 || row.Coverage.Missing != 72 || row.Coverage.Suspect != 72 {
			t.Fatalf("full month with incomplete coverage was ranked: %+v", row)
		}
	}
	month := readDimensionQuery(t, "?view=building&building=office-a&energy=electricity&period=month&mode=to-date&scenario=standard")
	trend := readTrendFixture(t, "month", "to-date", "standard")
	if !month.From.Equal(trend.Current.From) || !month.To.Equal(trend.Current.To) || !month.AsOf.Equal(trend.AsOf) ||
		len(month.Rows) != 1 || !reflect.DeepEqual(month.Rows[0].Coverage, trend.Current.Coverage) {
		t.Fatalf("F07 did not use the F08 month-to-date cutoff or coverage: %+v", month)
	}
	assertF06Usage(t, month.Rows[0].Usage, 14208)
	assertF06Usage(t, trend.Current.Usage, 14208)
}

func TestF07IndependentViewsAndRankings(t *testing.T) {
	for _, tc := range []struct {
		view string
		rows map[string]float64
	}{
		{"building", map[string]float64{"office-a": 1536, "office-b": 3072}},
		{"space", map[string]float64{"office-a-east": 1056, "office-a-west": 480, "office-b-east": 2112, "office-b-west": 960}},
		{"organization", map[string]float64{"operations": 3024, "occupants": 1584}},
	} {
		t.Run(tc.view, func(t *testing.T) {
			data := readDimensions(t, tc.view, "all", "day", "standard")
			if !data.Sample || string(data.View) != tc.view || len(data.Rows) != len(tc.rows) || data.Unit != "kWh" {
				t.Fatalf("unexpected response shape: %+v", data)
			}
			var sum, last float64
			for i, row := range data.Rows {
				want, found := tc.rows[row.ID]
				if !found || row.Status != buildingenergy.TrendComparable || row.Rank == nil || *row.Rank != i+1 || (i > 0 && want >= last) {
					t.Fatalf("unexpected ranked row %d: %+v", i, row)
				}
				assertF06Usage(t, row.Usage, want)
				last = want
				sum += want
				var traced float64
				for _, channel := range row.Sources {
					if channel.Channel == "" || channel.ObjectBindingVersion == "" || channel.MappingGeneration != 1 || channel.Observations != channel.Valid {
						t.Fatalf("source revision or coverage missing: %+v", channel)
					}
					traced += *channel.Usage
				}
				assertF06Usage(t, row.Usage, traced)
			}
			if sum != 4608 {
				t.Fatalf("%s failed to reconcile its independent partition: %g", tc.view, sum)
			}
		})
	}
}

func TestF07RankExcludesLowCoverageAndOnlyProjectsAllowedBuilding(t *testing.T) {
	report := readDimensions(t, "building", "all", "day", "quality")
	if len(report.Rows) != 2 || report.Rows[0].ID != "office-b" || report.Rows[0].Rank == nil ||
		report.Rows[1].ID != "office-a" || report.Rows[1].Rank != nil ||
		report.Rows[1].Status != buildingenergy.TrendInsufficientQuality || report.Rows[1].Coverage.Suspect != 1 {
		t.Fatalf("low coverage appeared in the official ordering: %+v", report.Rows)
	}
	oneBuilding := readDimensions(t, "organization", "office-a", "day", "standard")
	if len(oneBuilding.Rows) != 2 {
		t.Fatalf("unexpected scoped rows: %+v", oneBuilding.Rows)
	}
	for _, row := range oneBuilding.Rows {
		if row.ID == "operations" {
			assertF06Usage(t, row.Usage, 1008)
		} else {
			assertF06Usage(t, row.Usage, 528)
		}
		for _, source := range row.Sources {
			if !strings.HasPrefix(source.Channel, "office-a-") {
				t.Fatalf("other building found in scoped trace: %+v", source)
			}
		}
	}
}

func TestF06F07F08ReconcileSameWindowAndVersions(t *testing.T) {
	res := httptest.NewRecorder()
	previewHandler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, classificationPath+"?building=office-a&energy=electricity&period=day", nil))
	var payload struct{ Data f06Report }
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil || res.Code != http.StatusOK {
		t.Fatalf("F06 status %d (%v): %s", res.Code, err, res.Body)
	}
	f06 := payload.Data
	f07 := readDimensions(t, "building", "office-a", "day", "standard")
	f08 := readTrendFixture(t, "day", "complete", "standard")
	if !f06.From.Equal(f07.From) || !f06.From.Equal(f08.Current.From) ||
		!f06.To.Equal(f07.To) || !f06.To.Equal(f08.Current.To) ||
		!f06.AsOf.Equal(f07.AsOf) || !f06.AsOf.Equal(f08.AsOf) ||
		f06.Timezone != f07.Timezone || f06.Timezone != f08.Timezone ||
		f06.Unit != f07.Unit || f06.Unit != f08.Unit ||
		f06.ClassificationRuleVersion != f07.ClassificationRuleVersion || f06.ClassificationRuleVersion != f08.Current.ClassificationRuleVersion ||
		f06.CatalogVersion != f07.CatalogVersion || f06.CatalogVersion != f08.Current.CatalogVersion ||
		f06.TopologyVersion != f07.TopologyVersion || f06.TopologyVersion != f08.Current.TopologyVersion || len(f07.Rows) != 1 {
		t.Fatal("reports are not from the same normalized period and versioned scope")
	}
	for _, measure := range []f06Value{f06.Total, f07.Rows[0].f06Value, f08.Current.f06Value} {
		assertF06Usage(t, measure.Usage, 1536)
		if !reflect.DeepEqual(f06.Total.Coverage, measure.Coverage) || measure.Coverage.Valid != 72 {
			t.Fatalf("cross-page quality differs: %+v", measure.Coverage)
		}
	}
	for _, source := range f07.Rows[0].Sources {
		if source.MeterRuleVersion != f06.RuleVersion || source.MeterRuleVersion != f08.Current.MeterRuleVersion {
			t.Fatalf("cross-page meter calculation version differs: %+v", source)
		}
	}
}

func TestF07EndpointRejectsUnapprovedScopeParameters(t *testing.T) {
	const query = "?view=space&building=office-a&energy=water&period=day&mode=complete&scenario=standard"
	for _, suffix := range []string{"&object=office-b-west", "&point=foreign", "&project=outside", "&from=%zz", "&building=all"} {
		res := httptest.NewRecorder()
		previewHandler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, dimensionsPath+query+suffix, nil))
		if res.Code != http.StatusBadRequest || strings.Contains(res.Body.String(), "sources") {
			t.Errorf("unapproved %q returned %d: %s", suffix, res.Code, res.Body)
		}
	}
}
