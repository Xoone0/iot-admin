package buildingenergy

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type identityStub struct {
	subject Subject
	err     error
}

func (s identityStub) AuthenticatedSubject(context.Context) (Subject, error) { return s.subject, s.err }

type policyStub struct {
	check func(Subject, Action, Scope) error
}

func (s policyStub) Require(_ context.Context, subject Subject, action Action, scope Scope) error {
	return s.check(subject, action, scope)
}

type catalogStub struct {
	project Project
	objects map[ObjectKey]Object
	calls   int
}

func (s *catalogStub) Project(_ context.Context, _ ProjectKey, _ time.Time) (Project, error) {
	s.calls++
	return s.project, nil
}

func (s *catalogStub) Object(_ context.Context, _ ProjectKey, key ObjectKey, _ time.Time) (Object, error) {
	s.calls++
	return s.objects[key], nil
}

func TestM0ResolveScopeAuthorizesBeforeReadingExactHistoricalScope(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	project := ProjectKey{TenantID: "tenant-a", ProjectID: "project-a"}
	building := ObjectKey{Kind: Building, ID: "b-1"}
	space := ObjectKey{Kind: Space, ID: "s-1"}
	org := ObjectKey{Kind: Organization, ID: "org-1"}
	scope := Scope{
		Project: project,
		Period:  Range{From: start, To: start.Add(time.Hour)},
		Objects: []ObjectKey{building, space, org},
		Points:  []PointKey{{TerminalID: "meter-1", ItemCode: "active-energy"}},
	}
	meta := versionFixture(start)
	catalog := &catalogStub{
		project: Project{Key: project, Timezone: "Asia/Shanghai", Meta: meta},
		objects: map[ObjectKey]Object{
			building: {Project: project, Key: building, Meta: meta},
			space:    {Project: project, Key: space, Parent: building, BuildingID: building.ID, Meta: meta},
			org:      {Project: project, Key: org, Meta: meta},
		},
	}
	subject := Subject{Issuer: "test-idp", ID: "test-reader"}
	identity := identityStub{subject: subject}
	deny := errors.New("outside permitted project")
	for _, action := range []Action{ReadUsage, ManageBindings, ExportUsage, DownloadReport} {
		policy := policyStub{check: func(got Subject, operation Action, request Scope) error {
			if got != subject || operation != action || !reflect.DeepEqual(request, scope) || catalog.calls != 0 {
				t.Fatalf("authorization input or order is wrong: %+v, %v, %+v, reads=%d", got, operation, request, catalog.calls)
			}
			return deny
		}}
		if _, err := ResolveScope(ctx, identity, policy, catalog, action, scope); !errors.Is(err, ErrAccessDenied) {
			t.Fatalf("unauthorized request for %s: %v", action, err)
		}
	}
	if _, err := ResolveScope(ctx, identityStub{}, nil, catalog, ReadUsage, scope); !errors.Is(err, ErrAccessDenied) || catalog.calls != 0 {
		t.Fatalf("absent adapters exposed directory: err=%v calls=%d", err, catalog.calls)
	}
	allow := policyStub{check: func(Subject, Action, Scope) error { return nil }}
	if _, err := ResolveScope(ctx, identityStub{err: deny}, allow, catalog, ReadUsage, scope); !errors.Is(err, ErrAccessDenied) || catalog.calls != 0 {
		t.Fatalf("absent subject exposed directory: err=%v calls=%d", err, catalog.calls)
	}

	resolved, err := ResolveScope(ctx, identity, allow, catalog, ReadUsage, scope)
	if err != nil || resolved.Project.Key != project || resolved.Location.String() != "Asia/Shanghai" || len(resolved.Objects) != 3 || catalog.calls != 4 {
		t.Fatalf("resolved project scope: %+v (%v), calls=%d", resolved, err, catalog.calls)
	}
	t.Log("cross-scope and missing-identity requests denied before any catalog lookup; exact authorized scope resolved 3 objects")

	old := catalog.objects[org]
	old.Meta.Effective.To = start.Add(30 * time.Minute)
	catalog.objects[org] = old
	if _, err := ResolveScope(ctx, identity, allow, catalog, ReadUsage, scope); !errors.Is(err, ErrCatalogScope) {
		t.Fatalf("accepted object version expiring during the range: %v", err)
	}
	old.Meta = meta
	old.Project.ProjectID = "project-b"
	catalog.objects[org] = old
	if _, err := ResolveScope(ctx, identity, allow, catalog, ReadUsage, scope); !errors.Is(err, ErrCatalogScope) {
		t.Fatalf("accepted an object from another project: %v", err)
	}
}

type meteringStub struct {
	data   MeteringData
	point  PointRef
	period Range
	asOf   time.Time
}

func (s *meteringStub) Load(_ context.Context, point PointRef, period Range, asOf time.Time) (MeteringData, error) {
	s.point, s.period, s.asOf = point, period, asOf
	return s.data, nil
}

func TestM0MeteringSourceKeepsAttributeAndRejectsInstantaneousInputs(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	profile := pointFixture(start, Electricity, Cumulative, "kWh")
	profile.Point.Key.ItemCode = "voltage-a"
	profile.Kind, profile.Unit = Instantaneous, "V"
	window := fixtureWindow(start, start.Add(time.Hour), Hour)
	source := &meteringStub{data: MeteringData{Profile: profile}}
	if _, err := CalculateFromSource(t.Context(), source, profile.Point, window); !errors.Is(err, ErrUnsupportedMeasurement) {
		t.Fatalf("instantaneous channel treated as energy: %v", err)
	}
	other := profile.Point
	other.Key.ItemCode = "current-a"
	if _, err := CalculateFromSource(t.Context(), source, other, window); !errors.Is(err, ErrMeteringScope) {
		t.Fatalf("source returned the wrong terminal attribute: %v", err)
	}
	if source.point != other || !sameRange(source.period, window.Range) || !source.asOf.Equal(window.AsOf) {
		t.Fatalf("source did not receive exact point, window and visibility cutoff: %+v", source)
	}
}
