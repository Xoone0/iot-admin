package buildingenergy

import (
	"math"
	"testing"
	"time"
)

func anomalyExample(t *testing.T, at time.Time) (AnomalyRule, ClassifiedUsageReport, []ClassifiedUsageReport) {
	t.Helper()
	project := ProjectKey{TenantID: "demo", ProjectID: "sample"}
	object := ObjectKey{Kind: Building, ID: "office-a"}
	point := PointRef{Project: project, Key: PointKey{TerminalID: "ch-a", ItemCode: "USE"}}
	cutoff := at.AddDate(0, 0, 1)
	lifespan := Range{From: at.AddDate(-1, 0, 0), To: at.AddDate(1, 0, 0)}
	source := SourceRef{System: "demo", ID: "catalog"}
	stable := VersionMeta{Version: "v1", Effective: lifespan, Source: source}
	makeReport := func(day time.Time, usage float64) ClassifiedUsageReport {
		end := day.AddDate(0, 0, 1)
		window := Window{Range: Range{From: day, To: end}, Timezone: at.Location().String(), AsOf: cutoff, Granularity: Hour}
		hours := end.Sub(day).Hours()
		sum := EnergyCategorySummary{Energy: Electricity, Unit: "kWh", Total: CategoryValue{
			Usage: &usage, Coverage: WeightedCoverage{Expected: hours, Valid: hours},
		}}
		var intervals []ClassifiedIntervalEvidence
		for next := day; next.Before(end); next = next.Add(time.Hour) {
			perHour := usage / hours
			intervals = append(intervals, ClassifiedIntervalEvidence{
				Point: point, Energy: Electricity, Unit: "kWh", MappingGeneration: 1, MeterRuleVersion: "m1-a",
				ProfileVersion: "profile-v1", ObjectBindingVersion: "building-v1", CategoryBindingVersion: "category-v1",
				Trace: IntervalTrace{Range: Range{From: next, To: next.Add(time.Hour)}, Usage: &perHour, Quality: Normal, Sources: []SourceRef{source}},
			})
		}
		return ClassifiedUsageReport{
			Query: ClassifiedUsageQuery{Project: project, Object: object, Window: window}, RuleVersion: "m1-c",
			CatalogVersion: stable, TopologyVersion: stable, Energy: []EnergyCategorySummary{sum}, Intervals: intervals,
		}
	}
	rule := AnomalyRule{Project: project, Object: object, Energy: Electricity,
		Meta:           VersionMeta{Version: "rule-v1", Effective: lifespan, Source: SourceRef{System: "demo", ID: "rule"}},
		MinimumHistory: 3, MinimumAbsoluteIncrease: 50, MinimumRelativeIncrease: .25}
	return rule, makeReport(at, 1536), []ClassifiedUsageReport{
		makeReport(at.AddDate(0, 0, -7), 384), makeReport(at.AddDate(0, 0, -14), 768), makeReport(at.AddDate(0, 0, -21), 336),
	}
}

func anomalyDate(t *testing.T) time.Time {
	t.Helper()
	location, err := projectLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(2026, time.October, 15, 0, 0, 0, 0, location)
}

func expectAnomalyNumber(t *testing.T, actual *float64, expected float64) {
	t.Helper()
	if actual == nil || math.Abs(*actual-expected) > 1e-9 {
		t.Fatalf("quantity %v, want %v", actual, expected)
	}
}

func TestF10ComparesMedianWithBothConfiguredThresholds(t *testing.T) {
	for _, tc := range []struct {
		name        string
		usage       float64
		absoluteMin float64
		relativeMin float64
		status      AnomalyStatus
	}{
		{"synthetic rise", 1536, 50, .25, AnomalyUsageCandidate},
		{"both thresholds equality", 480, 96, .25, AnomalyUsageCandidate},
		{"absolute threshold exceeds the change", 480, 97, .15, AnomalyNoCandidate},
		{"relative threshold exceeds the change", 480, 90, .251, AnomalyNoCandidate},
		{"normal unchanged", 384, 50, .25, AnomalyNoCandidate},
		{"valid measured zero", 0, 50, .25, AnomalyNoCandidate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule, current, history := anomalyExample(t, anomalyDate(t))
			current.Energy[0].Total.Usage = &tc.usage
			rule.MinimumAbsoluteIncrease, rule.MinimumRelativeIncrease = tc.absoluteMin, tc.relativeMin
			got, err := EvaluateUsageAnomaly(Electricity, &rule, current, history)
			if err != nil || got.Status != tc.status || got.BaselineCount != 3 || got.Reason != "" || got.Unit != "kWh" {
				t.Fatalf("status %+v, error %v, want %s", got, err, tc.status)
			}
			expectAnomalyNumber(t, got.Current, tc.usage)
			expectAnomalyNumber(t, got.Baseline, 384)
			expectAnomalyNumber(t, got.Delta, tc.usage-384)
			expectAnomalyNumber(t, got.ChangeRate, (tc.usage-384)/384)
		})
	}
}

func TestF10RejectsUnqualifiedEvidence(t *testing.T) {
	type tweak func(*AnomalyRule, *ClassifiedUsageReport, *[]ClassifiedUsageReport)
	freshness := map[string]struct {
		adjust tweak
		status AnomalyStatus
		reason AnomalyReason
	}{
		"missing current usage": {func(_ *AnomalyRule, current *ClassifiedUsageReport, _ *[]ClassifiedUsageReport) {
			current.Energy[0].Total = CategoryValue{Coverage: WeightedCoverage{Expected: 24, Missing: 24}}
		}, AnomalyMeteringAnomaly, "missing_usage"},
		"partial current coverage": {func(_ *AnomalyRule, current *ClassifiedUsageReport, _ *[]ClassifiedUsageReport) {
			current.Energy[0].Total.Coverage = WeightedCoverage{Expected: 24, Valid: 23, Missing: 1}
		}, AnomalyMeteringAnomaly, "partial_coverage"},
		"suspect outranks missing": {func(_ *AnomalyRule, current *ClassifiedUsageReport, _ *[]ClassifiedUsageReport) {
			current.Energy[0].Total.Coverage = WeightedCoverage{Expected: 24, Valid: 22, Missing: 1, Suspect: 1}
		}, AnomalyMeteringAnomaly, "suspect_readings"},
		"no authorized observations": {func(_ *AnomalyRule, current *ClassifiedUsageReport, _ *[]ClassifiedUsageReport) {
			current.Energy[0].Total = CategoryValue{}
		}, AnomalyInsufficientEvidence, "missing_usage"},
		"one historical record": {func(_ *AnomalyRule, _ *ClassifiedUsageReport, history *[]ClassifiedUsageReport) {
			*history = (*history)[:1]
		}, AnomalyInsufficientEvidence, "insufficient_history"},
		"history from a different weekday": {func(_ *AnomalyRule, _ *ClassifiedUsageReport, history *[]ClassifiedUsageReport) {
			w := &(*history)[0].Query.Window
			w.From, w.To = w.From.AddDate(0, 0, -1), w.To.AddDate(0, 0, -1)
		}, AnomalyInsufficientEvidence, "incompatible_period"},
		"incomplete current day": {func(_ *AnomalyRule, current *ClassifiedUsageReport, _ *[]ClassifiedUsageReport) {
			current.Query.Window.To = current.Query.Window.To.Add(-time.Hour)
		}, AnomalyInsufficientEvidence, "incompatible_period"},
		"repeat historical date": {func(_ *AnomalyRule, _ *ClassifiedUsageReport, history *[]ClassifiedUsageReport) {
			(*history)[1] = (*history)[0]
		}, AnomalyInsufficientEvidence, "incompatible_period"},
		"historical version mismatch": {func(_ *AnomalyRule, _ *ClassifiedUsageReport, history *[]ClassifiedUsageReport) {
			(*history)[0].CatalogVersion.Version = "old"
		}, AnomalyInsufficientEvidence, "incompatible_scope"},
		"historical cutoff mismatch": {func(_ *AnomalyRule, _ *ClassifiedUsageReport, history *[]ClassifiedUsageReport) {
			(*history)[0].Query.Window.AsOf = (*history)[0].Query.Window.AsOf.Add(-time.Hour)
		}, AnomalyInsufficientEvidence, "incompatible_scope"},
		"changed measurement boundary": {func(_ *AnomalyRule, _ *ClassifiedUsageReport, history *[]ClassifiedUsageReport) {
			(*history)[0].Intervals = (*history)[0].Intervals[1:]
		}, AnomalyInsufficientEvidence, "incompatible_scope"},
		"measurement trace for another project": {func(_ *AnomalyRule, current *ClassifiedUsageReport, _ *[]ClassifiedUsageReport) {
			current.Intervals[0].Point.Project.ProjectID = "another"
		}, AnomalyInsufficientEvidence, "incompatible_scope"},
		"reported complete coverage contradicts unusable trace": {func(_ *AnomalyRule, current *ClassifiedUsageReport, _ *[]ClassifiedUsageReport) {
			current.Intervals[0].Trace.Quality = Estimated
		}, AnomalyInsufficientEvidence, "incompatible_scope"},
		"historical quality insufficient": {func(_ *AnomalyRule, _ *ClassifiedUsageReport, history *[]ClassifiedUsageReport) {
			(*history)[0].Energy[0].Total.Coverage = WeightedCoverage{Expected: 24, Valid: 23, Suspect: 1}
		}, AnomalyInsufficientEvidence, "invalid_baseline_quality"},
		"zero baseline median": {func(_ *AnomalyRule, _ *ClassifiedUsageReport, history *[]ClassifiedUsageReport) {
			for i := range *history {
				zero := 0.0
				(*history)[i].Energy[0].Total.Usage = &zero
			}
		}, AnomalyInsufficientEvidence, "zero_baseline"},
		"expired rule": {func(rule *AnomalyRule, current *ClassifiedUsageReport, _ *[]ClassifiedUsageReport) {
			rule.Meta.Effective.To = current.Query.Window.From
		}, AnomalyInsufficientEvidence, "rule_not_applicable"},
		"non-positive threshold": {func(rule *AnomalyRule, _ *ClassifiedUsageReport, _ *[]ClassifiedUsageReport) {
			rule.MinimumRelativeIncrease = 0
		}, AnomalyInsufficientEvidence, "rule_not_applicable"},
		"rule is for another building": {func(rule *AnomalyRule, _ *ClassifiedUsageReport, _ *[]ClassifiedUsageReport) {
			rule.Object.ID = "building-b"
		}, AnomalyInsufficientEvidence, "rule_not_applicable"},
		"malformed quality": {func(_ *AnomalyRule, current *ClassifiedUsageReport, _ *[]ClassifiedUsageReport) {
			current.Energy[0].Total.Coverage.Valid = math.NaN()
		}, AnomalyInsufficientEvidence, "incompatible_scope"},
	}
	for name, tc := range freshness {
		t.Run(name, func(t *testing.T) {
			rule, current, history := anomalyExample(t, anomalyDate(t))
			tc.adjust(&rule, &current, &history)
			got, err := EvaluateUsageAnomaly(Electricity, &rule, current, history)
			if err != nil || got.Status != tc.status || got.Reason != tc.reason || got.Delta != nil || got.ChangeRate != nil {
				t.Fatalf("outcome %+v, error %v, want %s/%s", got, err, tc.status, tc.reason)
			}
		})
	}
	t.Run("no rule", func(t *testing.T) {
		_, current, history := anomalyExample(t, anomalyDate(t))
		got, err := EvaluateUsageAnomaly(Electricity, nil, current, history)
		if err != nil || got.Status != AnomalyInsufficientEvidence || got.Reason != "missing_rule" || got.ChangeRate != nil {
			t.Fatalf("outcome %+v, error %v", got, err)
		}
	})
}

func TestF10RejectsDaylightSavingOffsetAndDurationMismatches(t *testing.T) {
	loc, err := projectLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range []int{1, 8} {
		// November 1 is a 25-hour day. November 8 has a different offset
		// from the two ordinary Sundays before November 1.
		t.Run(time.Date(2026, 11, day, 0, 0, 0, 0, loc).Format(time.DateOnly), func(t *testing.T) {
			rule, current, history := anomalyExample(t, time.Date(2026, 11, day, 0, 0, 0, 0, loc))
			got, err := EvaluateUsageAnomaly(Electricity, &rule, current, history)
			if err != nil || got.Status != AnomalyInsufficientEvidence || got.Reason != "incompatible_period" {
				t.Fatalf("outcome %+v, error %v", got, err)
			}
		})
	}
}

func TestF17EvidenceOnlyProducesManualReviewClues(t *testing.T) {
	rule, current, history := anomalyExample(t, anomalyDate(t))
	evaluation, err := EvaluateUsageAnomaly(Electricity, &rule, current, history)
	if err != nil {
		t.Fatal(err)
	}
	rank1, rank2, east, west := 1, 2, 1016.0, 520.0
	full := WeightedCoverage{Expected: 24, Valid: 24}
	peers := OpportunityPeers{Project: current.Query.Project, Object: current.Query.Object, Energy: Electricity, Unit: "kWh",
		Window: current.Query.Window, Rows: []OpportunityPeer{
			{Object: ObjectKey{Kind: Space, ID: "east"}, Value: CategoryValue{Usage: &east, Coverage: full}, Status: TrendComparable, Rank: &rank1},
			{Object: ObjectKey{Kind: Space, ID: "west"}, Value: CategoryValue{Usage: &west, Coverage: full}, Status: TrendComparable, Rank: &rank2},
		},
	}
	check := func(t *testing.T, clues []OpportunityClue, kinds ...OpportunityKind) {
		t.Helper()
		if len(clues) != len(kinds) {
			t.Fatalf("clues %+v, want %v", clues, kinds)
		}
		for i, clue := range clues {
			if clue.Kind != kinds[i] || clue.Status != "pending_manual_review" || clue.Evidence == "" ||
				len(clue.Limitations) == 0 || len(clue.Confirm) == 0 {
				t.Fatalf("no traceable manual-review evidence: %+v", clue)
			}
		}
	}
	t.Run("candidate and same-scope rank observation", func(t *testing.T) {
		clues := BuildEnergyOpportunities(evaluation, peers)
		check(t, clues, "baseline_increase", "high_usage_observation")
		expectAnomalyNumber(t, clues[0].Usage, 1536)
		expectAnomalyNumber(t, clues[0].Baseline, 384)
		expectAnomalyNumber(t, clues[1].Usage, 1016)
		if clues[1].ComparablePeers != 2 || clues[1].Object != peers.Rows[0].Object {
			t.Fatalf("rank clue evidence %+v", clues[1])
		}
	})
	t.Run("F16 evidence independent of no candidate", func(t *testing.T) {
		rule.MinimumAbsoluteIncrease = 1_200
		ordinary, err := EvaluateUsageAnomaly(Electricity, &rule, current, history)
		if err != nil || ordinary.Status != AnomalyNoCandidate {
			t.Fatalf("unexpected assessment %+v: %v", ordinary, err)
		}
		check(t, BuildEnergyOpportunities(ordinary, peers), "high_usage_observation")
	})
	t.Run("F16 evidence independent of missing F10 rule", func(t *testing.T) {
		unconfigured, err := EvaluateUsageAnomaly(Electricity, nil, current, history)
		if err != nil || unconfigured.Reason != anomalyMissingRule {
			t.Fatalf("unexpected assessment %+v: %v", unconfigured, err)
		}
		check(t, BuildEnergyOpportunities(unconfigured, peers), "high_usage_observation")
	})
	t.Run("do not borrow an out-of-scope peer set", func(t *testing.T) {
		mismatched := peers
		mismatched.Object.ID = "another-building"
		check(t, BuildEnergyOpportunities(evaluation, mismatched), "baseline_increase")
	})
	t.Run("do not turn an equal ranking into a high-use clue", func(t *testing.T) {
		same := peers
		same.Rows = append([]OpportunityPeer(nil), peers.Rows...)
		same.Rows[1].Value.Usage = &east
		same.Rows[1].Rank = &rank1
		check(t, BuildEnergyOpportunities(evaluation, same), "baseline_increase")
	})
	t.Run("do not count a poorly covered peer", func(t *testing.T) {
		partial := peers
		partial.Rows = append([]OpportunityPeer(nil), peers.Rows...)
		partial.Rows[1].Value.Coverage = WeightedCoverage{Expected: 24, Valid: 23, Missing: 1}
		check(t, BuildEnergyOpportunities(evaluation, partial), "baseline_increase")
	})
	t.Run("reject duplicate peers even if one is ineligible", func(t *testing.T) {
		duplicate := peers
		ineligible := peers.Rows[0]
		ineligible.Status, ineligible.Rank = TrendInsufficientQuality, nil
		duplicate.Rows = append([]OpportunityPeer{ineligible}, peers.Rows...)
		check(t, BuildEnergyOpportunities(evaluation, duplicate), "baseline_increase")
	})
	t.Run("metering problems do not become reduction opportunities", func(t *testing.T) {
		suspect := current
		suspect.Energy = append([]EnergyCategorySummary(nil), current.Energy...)
		suspect.Energy[0].Total.Coverage = WeightedCoverage{Expected: 24, Valid: 23, Suspect: 1}
		bad, err := EvaluateUsageAnomaly(Electricity, &rule, suspect, history)
		if err != nil || bad.Status != AnomalyMeteringAnomaly {
			t.Fatalf("unexpected assessment %+v: %v", bad, err)
		}
		check(t, BuildEnergyOpportunities(bad, peers))
	})
}
