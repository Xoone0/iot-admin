package buildingenergy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func timelineFixture() (MappingTimeline, time.Time, ObjectKey, ObjectKey, ObjectKey) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	change := from.Add(time.Hour)
	profile := pointFixture(from, Electricity, Cumulative, "kWh")
	profile.Meta.Effective.To = change
	newProfile := profile
	newProfile.Meta.Version, newProfile.Meta.Effective = "p2", Range{From: change}
	newProfile.Multiplier = 2
	meta := versionFixture(from)
	meta.Effective.To = change
	after := versionFixture(change)
	after.Version = "b2"
	old := ObjectKey{Kind: Building, ID: "b1"}
	next := ObjectKey{Kind: Building, ID: "b2"}
	org := ObjectKey{Kind: Organization, ID: "org-1"}
	timeline := MappingTimeline{
		Point: profile.Point, Generation: 3,
		Profiles: []ProfileRevision{
			{Profile: profile, Channel: SourceRef{System: "fixture", ID: "physical-1"}, RegisterEpoch: "epoch-1"},
			{Profile: newProfile, Channel: SourceRef{System: "fixture", ID: "physical-1"}, RegisterEpoch: "epoch-1"},
		},
		Bindings: []BindingRevision{
			{View: Building, Meta: meta, Allocations: []Allocation{{Target: old, TargetRevision: "b1-rev1", WeightPPM: 600000}}},
			{View: Building, Meta: after, Allocations: []Allocation{{Target: next, TargetRevision: "b2-rev1", WeightPPM: FullWeightPPM}}},
			{View: Organization, Meta: versionFixture(from), Allocations: []Allocation{{Target: org, TargetRevision: "org1-rev1", WeightPPM: FullWeightPPM}}},
		},
	}
	return timeline, from, old, next, org
}

func TestM1BBindingValidityAndWeight(t *testing.T) {
	cases := []struct {
		name   string
		change func(*MappingTimeline)
	}{
		{"overlapping profile", func(m *MappingTimeline) { m.Profiles[0].Profile.Meta.Effective.To = time.Time{} }},
		{"overlapping same view", func(m *MappingTimeline) { m.Bindings[0].Meta.Effective.To = time.Time{} }},
		{"reversed validity", func(m *MappingTimeline) {
			m.Bindings[0].Meta.Effective.To = m.Bindings[0].Meta.Effective.From.Add(-time.Hour)
		}},
		{"more than 100 percent", func(m *MappingTimeline) {
			m.Bindings[0].Allocations = append(m.Bindings[0].Allocations, Allocation{Target: ObjectKey{Kind: Building, ID: "extra"}, WeightPPM: 400001})
		}},
		{"duplicate target", func(m *MappingTimeline) {
			a := m.Bindings[0].Allocations[0]
			a.WeightPPM = 100000
			m.Bindings[0].Allocations = append(m.Bindings[0].Allocations, a)
		}},
		{"invalid weight", func(m *MappingTimeline) { m.Bindings[0].Allocations[0].WeightPPM = 0 }},
		{"cross view target", func(m *MappingTimeline) { m.Bindings[0].Allocations[0].Target.Kind = Organization }},
		{"missing meter channel", func(m *MappingTimeline) { m.Profiles[0].Channel = SourceRef{} }},
		{"no cumulative register epoch", func(m *MappingTimeline) { m.Profiles[0].RegisterEpoch = "" }},
		{"non-energy unit as cumulative", func(m *MappingTimeline) { m.Profiles[0].Profile.Unit = "A" }},
		{"foreign point profile", func(m *MappingTimeline) { m.Profiles[0].Profile.Point.Project.ProjectID = "other" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			timeline, _, _, _, _ := timelineFixture()
			tc.change(&timeline)
			if err := timeline.Validate(); !errors.Is(err, ErrInvalidMapping) {
				t.Fatalf("invalid mapping accepted: %v", err)
			}
		})
	}
	timeline, _, _, _, _ := timelineFixture()
	if err := timeline.Validate(); err != nil {
		t.Fatalf("adjacent versions and separate views rejected: %v", err)
	}
}

func TestM1BHistoricalLookupAndMidSampleChange(t *testing.T) {
	timeline, from, old, next, org := timelineFixture()
	change := from.Add(time.Hour)
	period := Range{From: from, To: change}
	first, err := timeline.Resolve(Building, period)
	if err != nil || first.Profile.Profile.Multiplier != 1 || first.Allocations[0].Target != old || first.UnallocatedPPM != 400000 {
		t.Fatalf("old version: %+v (%v)", first, err)
	}
	second, err := timeline.Resolve(Building, Range{From: change, To: change.Add(time.Hour)})
	if err != nil || second.Profile.Profile.Multiplier != 2 || second.Allocations[0].Target != next || second.UnallocatedPPM != 0 {
		t.Fatalf("new version: %+v (%v)", second, err)
	}
	organization, err := timeline.Resolve(Organization, period)
	if err != nil || len(organization.Allocations) != 1 || organization.Allocations[0].Target != org {
		t.Fatalf("organization query included an unrelated view: %+v (%v)", organization, err)
	}
	space, err := timeline.Resolve(Space, period)
	if err != nil || len(space.Allocations) != 0 || space.UnallocatedPPM != FullWeightPPM {
		t.Fatalf("unmapped point was silently assigned: %+v (%v)", space, err)
	}
	crossing := Range{From: change.Add(-time.Minute), To: change.Add(time.Minute)}
	if _, err := timeline.Resolve(Building, crossing); !errors.Is(err, ErrMappingBoundary) {
		t.Fatalf("accepted a mid-sample profile change: %v", err)
	}
	timeline.Profiles[0].Profile.Meta.Effective.To = time.Time{}
	timeline.Profiles = timeline.Profiles[:1]
	if _, err := timeline.Resolve(Building, crossing); !errors.Is(err, ErrMappingBoundary) {
		t.Fatalf("accepted a mid-sample binding change: %v", err)
	}
}

type mappingReaderStub struct {
	timeline MappingTimeline
	calls    int
}

func (s *mappingReaderStub) ReadMapping(_ context.Context, _ PointRef, _ uint64) (MappingTimeline, error) {
	s.calls++
	return s.timeline, nil
}

func TestM1BReadRequiresProjectAndAllTargetsBeforeReturningFullMapping(t *testing.T) {
	timeline, from, old, _, _ := timelineFixture()
	extra := ObjectKey{Kind: Building, ID: "b-extra"}
	timeline.Bindings[0].Allocations = append(timeline.Bindings[0].Allocations, Allocation{Target: extra, TargetRevision: "b-extra-rev1", WeightPPM: 300000})
	project := timeline.Point.Project
	meta := versionFixture(from)
	oldMeta := meta
	oldMeta.Version = "b1-rev1"
	extraMeta := meta
	extraMeta.Version = "b-extra-rev1"
	catalog := &catalogStub{
		project: Project{Key: project, Timezone: "UTC", Meta: meta},
		objects: map[ObjectKey]Object{
			old: {Project: project, Key: old, Meta: oldMeta}, extra: {Project: project, Key: extra, Meta: extraMeta},
		},
	}
	identity := identityStub{subject: Subject{Issuer: "local", ID: "reviewer"}}
	reader := &mappingReaderStub{timeline: timeline}
	query := MappingQuery{Point: timeline.Point, View: Building, Period: Range{From: from, To: from.Add(time.Hour)},
		Generation: 3, Objects: []ObjectKey{old, old}}
	policy := policyStub{check: func(_ Subject, action Action, scope Scope) error {
		if action != ReadUsage || len(scope.Points) != 1 || len(scope.Objects) > 2 || scope.Points[0] != query.Point.Key {
			t.Fatalf("wrong or duplicated authorization scope: %+v", scope)
		}
		if scope.Project != project {
			return errors.New("cross-project denied")
		}
		return nil
	}}
	foreign := query
	foreign.Point.Project.TenantID = "other"
	if _, err := ReadAuthorizedMapping(t.Context(), identity, policy, catalog, reader, foreign); !errors.Is(err, ErrAccessDenied) || reader.calls != 0 || catalog.calls != 0 {
		t.Fatalf("unscoped source read: %v, calls=%d, catalog=%d", err, reader.calls, catalog.calls)
	}
	if _, err := ReadAuthorizedMapping(t.Context(), identity, policy, catalog, reader, query); !errors.Is(err, ErrAccessDenied) || reader.calls != 1 {
		t.Fatalf("partial authorization returned complete weights: %v", err)
	}
	query.Objects = []ObjectKey{old, extra, old, extra}
	result, err := ReadAuthorizedMapping(t.Context(), identity, policy, catalog, reader, query)
	if err != nil || len(result.Allocations) != 2 || result.UnallocatedPPM != 100000 {
		t.Fatalf("explicitly authorized set: %+v (%v)", result, err)
	}
	reader.timeline.Generation++
	if _, err := ReadAuthorizedMapping(t.Context(), identity, policy, catalog, reader, query); !errors.Is(err, ErrMappingUnavailable) {
		t.Fatalf("source returned an unrequested configuration version: %v", err)
	}
	query.Objects = nil
	if _, err := ReadAuthorizedMapping(t.Context(), identity, policy, catalog, reader, query); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("empty selection unexpectedly granted full scope: %v", err)
	}
}

func TestM1BBoundaryCannotCountTotalAndSubmetersTogether(t *testing.T) {
	timeline, _, _, _, _ := timelineFixture()
	total := timeline.Point
	child := total
	child.Key.TerminalID = "child"
	parent := BoundaryContributor{Point: total, Role: BoundaryTotal}
	sub := BoundaryContributor{Point: child, Role: BoundarySubmeter}
	for _, inputs := range [][]BoundaryContributor{{parent, sub}, {sub, sub}, {parent, parent}} {
		if !errors.Is(ValidateBoundaryContributors(total.Project, inputs), ErrInvalidMapping) {
			t.Fatalf("double counting was allowed: %+v", inputs)
		}
	}
	if err := ValidateBoundaryContributors(total.Project, []BoundaryContributor{parent}); err != nil {
		t.Fatal(err)
	}
	child2 := sub
	child2.Point.Key.TerminalID = "child-2"
	if err := ValidateBoundaryContributors(total.Project, []BoundaryContributor{sub, child2}); err != nil {
		t.Fatal(err)
	}
	child2.Point.Project.TenantID = "foreign"
	if !errors.Is(ValidateBoundaryContributors(total.Project, []BoundaryContributor{sub, child2}), ErrInvalidMapping) {
		t.Fatal("accepted cross-tenant boundary")
	}
}

type mappingWriterStub struct {
	mu        sync.Mutex
	current   uint64
	history   map[uint64]MappingTimeline
	published []MappingTimeline
	audit     MappingAudit
}

func (s *mappingWriterStub) CompareAndSwapMapping(_ context.Context, expected uint64, next MappingTimeline, audit MappingAudit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != expected {
		return ErrMappingConflict
	}
	s.current = next.Generation
	s.history[next.Generation] = next
	s.published = append(s.published, next)
	s.audit = audit
	return nil
}

type mappingChangePolicyStub func(MappingAudit, uint64, MappingTimeline) error

func (s mappingChangePolicyStub) RequireMappingChange(_ context.Context, audit MappingAudit, expected uint64, next MappingTimeline) error {
	return s(audit, expected, next)
}

func TestM1BConcurrentPublicationKeepsHistoryAndRejectsLostUpdates(t *testing.T) {
	first, from, _, _, _ := timelineFixture()
	second, _, _, _, _ := timelineFixture()
	previousTimeline, _, old, _, _ := timelineFixture()
	first.Generation, second.Generation = 4, 4
	second.Bindings[0].Allocations[0].WeightPPM = 500000
	repo := &mappingWriterStub{current: 3, history: map[uint64]MappingTimeline{3: previousTimeline}}
	actor := Subject{Issuer: "trusted-fixture", ID: "reviewer"}
	identity := identityStub{subject: actor}
	approval := MappingApproval{At: from, Reason: "fixture changes", Ref: SourceRef{System: "fixture", ID: "review-1"}}
	audit := MappingAudit{Actor: actor, Approval: approval}
	allow := mappingChangePolicyStub(func(got MappingAudit, expected uint64, next MappingTimeline) error {
		if got == audit && expected == 3 && next.Point == previousTimeline.Point {
			return nil
		}
		return ErrAccessDenied
	})
	deny := mappingChangePolicyStub(func(MappingAudit, uint64, MappingTimeline) error { return ErrAccessDenied })
	if err := PublishMapping(t.Context(), identity, deny, repo, 3, first, approval); !errors.Is(err, ErrAccessDenied) || repo.current != 3 {
		t.Fatalf("unauthorized publication: %v", err)
	}
	results := make(chan error, 2)
	for _, candidate := range []MappingTimeline{first, second} {
		go func() { results <- PublishMapping(t.Context(), identity, allow, repo, 3, candidate, approval) }()
	}
	ok, conflicts := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrMappingConflict):
			conflicts++
		default:
			t.Fatalf("unexpected publish result: %v", err)
		}
	}
	if ok != 1 || conflicts != 1 || len(repo.published) != 1 || repo.current != 4 || repo.audit != audit {
		t.Fatalf("lost update or audit: accepted=%d conflicts=%d saved=%d", ok, conflicts, len(repo.published))
	}
	previous, err := repo.history[3].Resolve(Building, Range{From: from, To: from.Add(time.Hour)})
	if err != nil || previous.Generation != 3 || previous.Allocations[0].Target != old || previous.UnallocatedPPM != 400000 {
		t.Fatalf("original generation lost: %+v (%v)", previous, err)
	}
	next := second
	next.Generation = 5
	if err := PublishMapping(t.Context(), identityStub{}, allow, repo, 4, next, approval); !errors.Is(err, ErrAccessDenied) || repo.current != 4 {
		t.Fatalf("unaudited publication: %v", err)
	}
}

type versionedCatalogStub struct {
	project Project
	objects func(project ProjectKey, key ObjectKey, at time.Time) (Object, error)
}

func (c *versionedCatalogStub) Project(_ context.Context, _ ProjectKey, _ time.Time) (Project, error) {
	return c.project, nil
}

func (c *versionedCatalogStub) Object(_ context.Context, project ProjectKey, key ObjectKey, at time.Time) (Object, error) {
	return c.objects(project, key, at)
}

func TestM1BTargetRevisionVerificationAcrossDirectoryChangeHistoryCrossProjectAndTypedNils(t *testing.T) {
	timeline, from, b1, _, _ := timelineFixture()
	change := from.Add(time.Hour)
	project := timeline.Point.Project

	// b1 changes directory revision at `change`: b1-rev1 in [from, change), b1-rev2 in [change, ∞).
	timeline.Profiles = timeline.Profiles[:1]
	timeline.Profiles[0].Profile.Meta.Effective = Range{From: from}
	bindV1 := versionFixture(from)
	bindV1.Version = "bind-v1"
	bindV1.Effective = Range{From: from, To: change}
	bindV2 := versionFixture(change)
	bindV2.Version = "bind-v2"
	bindV2.Effective = Range{From: change}
	timeline.Bindings = []BindingRevision{
		{View: Building, Meta: bindV1, Allocations: []Allocation{{Target: b1, TargetRevision: "b1-rev1", WeightPPM: FullWeightPPM}}},
		{View: Building, Meta: bindV2, Allocations: []Allocation{{Target: b1, TargetRevision: "b1-rev2", WeightPPM: FullWeightPPM}}},
	}

	projMeta := versionFixture(from)
	projMeta.Version = "proj-rev1"
	catalog := &versionedCatalogStub{
		project: Project{Key: project, Timezone: "UTC", Meta: projMeta},
		objects: func(proj ProjectKey, key ObjectKey, at time.Time) (Object, error) {
			if proj != project || key != b1 {
				return Object{}, ErrCatalogScope
			}
			if at.Before(change) {
				m := versionFixture(from)
				m.Version = "b1-rev1"
				m.Effective = Range{From: from, To: change}
				return Object{Project: project, Key: b1, Meta: m}, nil
			}
			m := versionFixture(change)
			m.Version = "b1-rev2"
			m.Effective = Range{From: change}
			return Object{Project: project, Key: b1, Meta: m}, nil
		},
	}
	identity := identityStub{subject: Subject{Issuer: "corp-idp", ID: "u1"}}
	policy := policyStub{check: func(Subject, Action, Scope) error { return nil }}
	reader := &mappingReaderStub{timeline: timeline}

	// 1. Historical window [from, change) matches b1-rev1.
	histRes, err := ReadAuthorizedMapping(t.Context(), identity, policy, catalog, reader, MappingQuery{
		Point: timeline.Point, View: Building, Period: Range{From: from, To: change}, Generation: 3, Objects: []ObjectKey{b1},
	})
	if err != nil || histRes.Allocations[0].TargetRevision != "b1-rev1" {
		t.Fatalf("historical target revision failed: %+v (%v)", histRes, err)
	}

	// 2. Post-change window [change, change+1h) matches b1-rev2.
	currRes, err := ReadAuthorizedMapping(t.Context(), identity, policy, catalog, reader, MappingQuery{
		Point: timeline.Point, View: Building, Period: Range{From: change, To: change.Add(time.Hour)}, Generation: 3, Objects: []ObjectKey{b1},
	})
	if err != nil || currRes.Allocations[0].TargetRevision != "b1-rev2" {
		t.Fatalf("post-change target revision failed: %+v (%v)", currRes, err)
	}

	// 3. Stale target revision after directory change (b1-rev1 queried in post-change period) fails with ErrCatalogScope.
	stale := timeline
	stale.Bindings = []BindingRevision{
		timeline.Bindings[0],
		{View: Building, Meta: bindV2, Allocations: []Allocation{{Target: b1, TargetRevision: "b1-rev1", WeightPPM: FullWeightPPM}}},
	}
	if _, err := ReadAuthorizedMapping(t.Context(), identity, policy, catalog, &mappingReaderStub{timeline: stale}, MappingQuery{
		Point: timeline.Point, View: Building, Period: Range{From: change, To: change.Add(time.Hour)}, Generation: 3, Objects: []ObjectKey{b1},
	}); !errors.Is(err, ErrCatalogScope) {
		t.Fatalf("stale target revision accepted after directory change: %v", err)
	}

	// 4. BindingRevision.Meta.Version masquerading as TargetRevision fails with ErrCatalogScope.
	spoofed := timeline
	spoofed.Bindings = []BindingRevision{
		{View: Building, Meta: bindV1, Allocations: []Allocation{{Target: b1, TargetRevision: bindV1.Version, WeightPPM: FullWeightPPM}}},
	}
	if _, err := ReadAuthorizedMapping(t.Context(), identity, policy, catalog, &mappingReaderStub{timeline: spoofed}, MappingQuery{
		Point: timeline.Point, View: Building, Period: Range{From: from, To: change}, Generation: 3, Objects: []ObjectKey{b1},
	}); !errors.Is(err, ErrCatalogScope) {
		t.Fatalf("BindingRevision.Meta.Version masquerading as TargetRevision was accepted: %v", err)
	}

	// 5. Missing TargetRevision fails both ReadAuthorizedMapping and PublishMapping.
	missing := timeline
	missing.Bindings = []BindingRevision{
		{View: Building, Meta: bindV1, Allocations: []Allocation{{Target: b1, WeightPPM: FullWeightPPM}}},
	}
	if _, err := ReadAuthorizedMapping(t.Context(), identity, policy, catalog, &mappingReaderStub{timeline: missing}, MappingQuery{
		Point: timeline.Point, View: Building, Period: Range{From: from, To: change}, Generation: 3, Objects: []ObjectKey{b1},
	}); !errors.Is(err, ErrCatalogScope) {
		t.Fatalf("missing TargetRevision accepted on ReadAuthorizedMapping: %v", err)
	}
	missingPub := missing
	missingPub.Generation = 4
	approval := MappingApproval{At: from, Reason: "publish", Ref: SourceRef{System: "chg", ID: "1"}}
	allowPub := mappingChangePolicyStub(func(MappingAudit, uint64, MappingTimeline) error { return nil })
	if err := PublishMapping(t.Context(), identity, allowPub, &mappingWriterStub{current: 3, history: map[uint64]MappingTimeline{}}, 3, missingPub, approval); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("missing TargetRevision accepted on PublishMapping: %v", err)
	}

	// 6. Cross-project object returned by catalog fails with ErrCatalogScope.
	crossProjCatalog := &versionedCatalogStub{
		project: Project{Key: project, Timezone: "UTC", Meta: projMeta},
		objects: func(_ ProjectKey, key ObjectKey, _ time.Time) (Object, error) {
			m := versionFixture(from)
			m.Version = "b1-rev1"
			return Object{Project: ProjectKey{TenantID: project.TenantID, ProjectID: "other-proj"}, Key: key, Meta: m}, nil
		},
	}
	if _, err := ReadAuthorizedMapping(t.Context(), identity, policy, crossProjCatalog, reader, MappingQuery{
		Point: timeline.Point, View: Building, Period: Range{From: from, To: change}, Generation: 3, Objects: []ObjectKey{b1},
	}); !errors.Is(err, ErrCatalogScope) {
		t.Fatalf("cross-project catalog object accepted: %v", err)
	}

	// 7. Typed nil dependencies fail closed without panicking.
	var nilCatalog *versionedCatalogStub
	var nilReader *mappingReaderStub
	var nilWriter *mappingWriterStub
	var nilPolicy mappingChangePolicyStub
	q := MappingQuery{Point: timeline.Point, View: Building, Period: Range{From: from, To: change}, Generation: 3, Objects: []ObjectKey{b1}}
	if _, err := ReadAuthorizedMapping(t.Context(), identity, policy, nilCatalog, reader, q); !errors.Is(err, ErrCatalogScope) {
		t.Fatalf("typed nil catalog did not fail closed: %v", err)
	}
	if _, err := ReadAuthorizedMapping(t.Context(), identity, policy, catalog, nilReader, q); !errors.Is(err, ErrMappingUnavailable) {
		t.Fatalf("typed nil reader did not fail closed: %v", err)
	}
	pubNext := timeline
	pubNext.Generation = 4
	if err := PublishMapping(t.Context(), identity, nilPolicy, &mappingWriterStub{}, 3, pubNext, approval); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("typed nil policy did not fail closed: %v", err)
	}
	if err := PublishMapping(t.Context(), identity, allowPub, nilWriter, 3, pubNext, approval); !errors.Is(err, ErrInvalidMapping) {
		t.Fatalf("typed nil writer did not fail closed: %v", err)
	}
}
