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

const f06DefaultQuery = "?building=office-a&energy=electricity&period=7"

func TestF06SampleReconcilesUsageCoverageAndProvenance(t *testing.T) {
	response := httptest.NewRecorder()
	previewHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, classificationPath+f06DefaultQuery, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body)
	}
	var payload struct{ Data f06Report }
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	r := payload.Data
	if !r.Sample || r.Unit != "kWh" || len(r.Trend) != 7 || len(r.Categories) != 2 || len(r.Sources) != 3 {
		t.Fatalf("unexpected result shape: %+v", r)
	}
	assertF06Usage(t, r.Total.Usage, 3840)
	assertF06Usage(t, r.Classified, 2880)
	assertF06Usage(t, r.Unclassified.Usage, 960)
	if len(r.Composition) != 3 || r.Composition[0].Share == nil || r.Composition[1].Share == nil || r.Composition[2].Share == nil ||
		*r.Composition[0].Share+*r.Composition[1].Share+*r.Composition[2].Share != 1 {
		t.Fatalf("composition does not reconcile: %+v", r.Composition)
	}
	q := r.Total.Coverage
	if q.Expected != 504 || q.Valid != 360 || q.Missing != 72 || q.Suspect != 72 || q.Valid+q.Missing+q.Suspect != q.Expected {
		t.Fatalf("wrong coverage: %+v", q)
	}
	var trendSum float64
	for _, day := range r.Trend {
		if day.Total.Usage != nil {
			trendSum += *day.Total.Usage
		}
	}
	assertF06Usage(t, r.Total.Usage, trendSum)
	assertF06Usage(t, r.Trend[4].Total.Usage, 0)
	if r.Trend[5].Total.Usage != nil || r.Trend[5].Total.Coverage.Missing != 72 ||
		r.Trend[6].Total.Usage != nil || r.Trend[6].Total.Coverage.Suspect != 72 {
		t.Fatal("zero, missing and suspect periods were merged")
	}
	var traced float64
	leafTotals := make(map[string]float64)
	for _, source := range r.Sources {
		if source.Usage == nil || source.MappingGeneration != 1 || source.ProfileVersion == "" || source.BindingVersion == "" ||
			source.Observations != 168 || source.Valid != 120 || source.Missing != 24 || source.Suspect != 24 {
			t.Fatalf("incomplete source evidence: %+v", source)
		}
		traced += *source.Usage
		var sourceTotal float64
		for _, item := range source.Breakdown {
			if item.Usage == nil {
				t.Fatalf("source breakdown has no usage: %+v", item)
			}
			sourceTotal += *item.Usage
			leafTotals[item.Name] += *item.Usage
		}
		assertF06Usage(t, source.Usage, sourceTotal)
	}
	assertF06Usage(t, r.Total.Usage, traced)
	if leafTotals["室内照明"] != 2400 || leafTotals["空调与通风"] != 480 || leafTotals["未分项"] != 960 {
		t.Fatalf("meter evidence does not reconcile to the taxonomy: %v", leafTotals)
	}
	if r.AsOf.IsZero() || !r.AsOf.Equal(r.To) || r.TopologyVersion == "" || r.TopologySource != f06TopologySource ||
		r.RuleVersion != "m1-a-v1" || r.ClassificationRuleVersion != "m1-c-v1" {
		t.Fatal("cutoff or calculation versions missing")
	}
	malformed := httptest.NewRecorder()
	previewHandler().ServeHTTP(malformed, httptest.NewRequest(http.MethodGet, classificationPath+f06DefaultQuery+"&from=%zz", nil))
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed selector returned status %d", malformed.Code)
	}
}

func TestF06ScopeDeniesBeforeReadingAndProjectsOnlyAuthorizedSources(t *testing.T) {
	for _, key := range []string{"project", "point", "object", "from", "to", "asOf", "building"} {
		t.Run(key, func(t *testing.T) {
			response := httptest.NewRecorder()
			previewHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, classificationPath+f06DefaultQuery+"&"+key+"=foreign", nil))
			if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "sources") {
				t.Fatalf("unapproved query exposed data: %d %s", response.Code, response.Body)
			}
		})
	}
	fixture, err := newF06Fixture("office-a", buildingenergy.Electricity, 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*buildingenergy.Scope){
		func(s *buildingenergy.Scope) { s.Project.ProjectID = "foreign" },
		func(s *buildingenergy.Scope) { s.Objects[0].ID = "office-b" },
		func(s *buildingenergy.Scope) {
			s.Objects = append(s.Objects, buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: "office-b"})
		},
		func(s *buildingenergy.Scope) { s.Points[0].TerminalID = "office-b-electricity-light" },
		func(s *buildingenergy.Scope) { s.Period.From = s.Period.From.Add(-time.Hour) },
	} {
		reads := 0
		service := f06Service{fixture: fixture, read: func(buildingenergy.PointRef, buildingenergy.Window) buildingenergy.MeteringData {
			reads++
			return buildingenergy.MeteringData{}
		}}
		scope := fixture.scope()
		change(&scope)
		if _, err := service.calculate(context.Background(), scope); !errors.Is(err, buildingenergy.ErrAccessDenied) || reads != 0 {
			t.Fatalf("unauthorized input reached metering: reads=%d err=%v", reads, err)
		}
	}
	response := httptest.NewRecorder()
	previewHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, classificationPath+f06DefaultQuery, nil))
	for _, secret := range []string{"office-b", "987654", "raw-reading", "WeightPPM", "allocations", "terminalId"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("unscoped source data leaked: %q", secret)
		}
	}
}

func TestF06TopologyCertificateMustCoverExactPhysicalSelection(t *testing.T) {
	fixture, err := newF06Fixture("office-a", buildingenergy.Electricity, 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*f06Certificate){
		func(c *f06Certificate) { c.snapshot.Meta.Source.ID = "" },
		func(c *f06Certificate) { c.snapshot.Meta.Source.ID = "unconfirmed-channel-ids" },
		func(c *f06Certificate) { c.snapshot.Meta.Effective.To = fixture.window.From },
		func(c *f06Certificate) { c.object.ID = "office-b" },
		func(c *f06Certificate) { c.energy = buildingenergy.Water },
		func(c *f06Certificate) { c.snapshot.Boundaries = c.snapshot.Boundaries[:2] },
		func(c *f06Certificate) { c.snapshot.Boundaries[0][0].Channel.ID = "different-channel" },
		func(c *f06Certificate) { c.snapshot.Boundaries[0][0].MappingGeneration++ },
	} {
		certificate := fixture.syntheticCertificate()
		mutate(&certificate)
		if _, err := fixture.checkedTopology(certificate); !errors.Is(err, buildingenergy.ErrInvalidClassification) {
			t.Fatalf("nonmatching physical evidence accepted: %v", err)
		}
	}
	if _, err := fixture.checkedTopology(fixture.syntheticCertificate()); err != nil {
		t.Fatal(err)
	}
}

func TestF06SourceProjectionOmitsForeignAndOutOfWindowEvidence(t *testing.T) {
	f, err := newF06Fixture("office-a", buildingenergy.Electricity, 7)
	if err != nil {
		t.Fatal(err)
	}
	query := buildingenergy.ClassifiedUsageQuery{Project: f.project.Key, Object: f.object, Window: f.window}
	point := buildingenergy.PointRef{Project: query.Project, Key: f.meters[0].key}
	trusted, secret := 10.0, 987654.0
	allowed := buildingenergy.ClassifiedIntervalEvidence{Point: point, Trace: buildingenergy.IntervalTrace{
		Range: buildingenergy.Range{From: query.Window.From, To: query.Window.From.Add(time.Hour)}, Usage: &trusted,
	}}
	foreign, outside := allowed, allowed
	foreign.Point.Key.TerminalID = "office-b-electricity-primary"
	foreign.Trace.Usage = &secret
	outside.Trace.Range = buildingenergy.Range{From: query.Window.From.Add(-time.Hour), To: query.Window.From}
	outside.Trace.Usage = &secret
	report := buildingenergy.ClassifiedUsageReport{Query: query, Intervals: []buildingenergy.ClassifiedIntervalEvidence{allowed, foreign, outside}}
	got := f.projectSources(query, report)
	if len(got) != 1 || got[0].Observations != 1 {
		t.Fatalf("outside or foreign interval was projected: %+v", got)
	}
	assertF06Usage(t, got[0].Usage, 10)
	report.Query.Object.ID = "office-b"
	if got := f.projectSources(query, report); len(got) != 0 {
		t.Fatalf("foreign object report was projected: %+v", got)
	}
}

func TestF06BuildingEnergyAndPeriodSelectionsUseTheirOwnInputs(t *testing.T) {
	for _, tc := range []struct {
		query, unit string
		days        int
		usage       float64
	}{
		{"?building=office-b&energy=water&period=7", "m³", 7, 1920},
		{"?building=office-a&energy=water&period=14", "m³", 14, 2688},
		{"?building=office-b&energy=electricity&period=30", "kWh", 30, 52224},
	} {
		t.Run(tc.query, func(t *testing.T) {
			response := httptest.NewRecorder()
			previewHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, classificationPath+tc.query, nil))
			var payload struct{ Data f06Report }
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || response.Code != http.StatusOK {
				t.Fatalf("status %d: %s", response.Code, response.Body)
			}
			if len(payload.Data.Trend) != tc.days || payload.Data.Unit != tc.unit {
				t.Fatalf("wrong unit or time window: %s with %d days", payload.Data.Unit, len(payload.Data.Trend))
			}
			assertF06Usage(t, payload.Data.Total.Usage, tc.usage)
		})
	}
}

func assertF06Usage(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("usage %v, want %v", got, want)
	}
}
