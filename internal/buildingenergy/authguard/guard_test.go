package authguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

type authStub struct {
	fn func(context.Context, *http.Request) (Principal, error)
}

func (a authStub) Authenticate(ctx context.Context, r *http.Request) (Principal, error) {
	return a.fn(ctx, r)
}

type revocationStub struct {
	revoked map[string]bool
	err     error
}

func (r *revocationStub) IsRevoked(_ context.Context, p Principal) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	return r.revoked[p.Subject.Issuer+"|"+p.Subject.ID] || (p.SessionID != "" && r.revoked[p.SessionID]), nil
}

type catalogStub struct {
	project buildingenergy.Project
	objects map[buildingenergy.ObjectKey]buildingenergy.Object
	calls   int
}

func (c *catalogStub) Project(_ context.Context, _ buildingenergy.ProjectKey, _ time.Time) (buildingenergy.Project, error) {
	c.calls++
	return c.project, nil
}

func (c *catalogStub) Object(_ context.Context, _ buildingenergy.ProjectKey, key buildingenergy.ObjectKey, _ time.Time) (buildingenergy.Object, error) {
	c.calls++
	obj, ok := c.objects[key]
	if !ok {
		return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
	}
	return obj, nil
}

type mappingReaderStub struct {
	timeline buildingenergy.MappingTimeline
	err      error
}

func (m mappingReaderStub) ReadMapping(_ context.Context, _ buildingenergy.PointRef, _ uint64) (buildingenergy.MappingTimeline, error) {
	return m.timeline, m.err
}

func testFixture() (time.Time, buildingenergy.ProjectKey, buildingenergy.ObjectKey, buildingenergy.ObjectKey, buildingenergy.PointKey, buildingenergy.PointKey, *catalogStub) {
	from := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	project := buildingenergy.ProjectKey{TenantID: "t1", ProjectID: "p1"}
	s1 := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s1"}
	s2 := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s2"}
	p1 := buildingenergy.PointKey{TerminalID: "e1", ItemCode: "power_total"}
	p2 := buildingenergy.PointKey{TerminalID: "e2", ItemCode: "power_total"}
	meta := buildingenergy.VersionMeta{
		Version:   "v1",
		Effective: buildingenergy.Range{From: from},
		Source:    buildingenergy.SourceRef{System: "dir", ID: "ref"},
	}
	cat := &catalogStub{
		project: buildingenergy.Project{Key: project, Timezone: "Asia/Shanghai", Meta: meta},
		objects: map[buildingenergy.ObjectKey]buildingenergy.Object{
			s1: {Project: project, Key: s1, BuildingID: "b1", Meta: meta},
			s2: {Project: project, Key: s2, BuildingID: "b1", Meta: meta},
		},
	}
	return from, project, s1, s2, p1, p2, cat
}

func TestFormalRouterFailsClosedWithoutUpstreamAuth(t *testing.T) {
	now := time.Date(2026, time.November, 15, 12, 0, 0, 0, time.UTC)
	validAuth := authStub{fn: func(context.Context, *http.Request) (Principal, error) {
		return Principal{
			Subject:   buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"},
			Audience:  "energy-api",
			IssuedAt:  now.Add(-time.Minute),
			ExpiresAt: now.Add(time.Hour),
		}, nil
	}}
	rev := &revocationStub{revoked: map[string]bool{}}
	validPolicy := &ScopedPolicy{Revocations: rev}

	for _, tc := range []struct {
		name  string
		guard *Guard
	}{
		{"nil guard", nil},
		{"missing authenticator", &Guard{
			Identity: ContextIdentitySource{AllowedIssuers: []string{"corp-idp"}, ExpectedAudience: "energy-api", MaxCredentialTTL: 4 * time.Hour, Revocations: rev, Now: func() time.Time { return now }},
			Policy:   validPolicy,
		}},
		{"missing policy", &Guard{
			Authenticator: validAuth,
			Identity:      ContextIdentitySource{AllowedIssuers: []string{"corp-idp"}, ExpectedAudience: "energy-api", MaxCredentialTTL: 4 * time.Hour, Revocations: rev, Now: func() time.Time { return now }},
		}},
		{"missing AllowedIssuers", &Guard{
			Authenticator: validAuth,
			Identity:      ContextIdentitySource{ExpectedAudience: "energy-api", MaxCredentialTTL: 4 * time.Hour, Revocations: rev, Now: func() time.Time { return now }},
			Policy:        validPolicy,
		}},
		{"synthetic issuer in AllowedIssuers", &Guard{
			Authenticator: validAuth,
			Identity:      ContextIdentitySource{AllowedIssuers: []string{"corp-idp", "f06-preview"}, ExpectedAudience: "energy-api", MaxCredentialTTL: 4 * time.Hour, Revocations: rev, Now: func() time.Time { return now }},
			Policy:        validPolicy,
		}},
		{"missing ExpectedAudience", &Guard{
			Authenticator: validAuth,
			Identity:      ContextIdentitySource{AllowedIssuers: []string{"corp-idp"}, MaxCredentialTTL: 4 * time.Hour, Revocations: rev, Now: func() time.Time { return now }},
			Policy:        validPolicy,
		}},
		{"missing MaxCredentialTTL", &Guard{
			Authenticator: validAuth,
			Identity:      ContextIdentitySource{AllowedIssuers: []string{"corp-idp"}, ExpectedAudience: "energy-api", Revocations: rev, Now: func() time.Time { return now }},
			Policy:        validPolicy,
		}},
		{"over-cap MaxCredentialTTL", &Guard{
			Authenticator: validAuth,
			Identity:      ContextIdentitySource{AllowedIssuers: []string{"corp-idp"}, ExpectedAudience: "energy-api", MaxCredentialTTL: HardMaxCredentialTTL + time.Minute, Revocations: rev, Now: func() time.Time { return now }},
			Policy:        validPolicy,
		}},
		{"missing Revocations on Identity", &Guard{
			Authenticator: validAuth,
			Identity:      ContextIdentitySource{AllowedIssuers: []string{"corp-idp"}, ExpectedAudience: "energy-api", MaxCredentialTTL: 4 * time.Hour, Now: func() time.Time { return now }},
			Policy:        validPolicy,
		}},
		{"missing Revocations on ScopedPolicy", &Guard{
			Authenticator: validAuth,
			Identity:      ContextIdentitySource{AllowedIssuers: []string{"corp-idp"}, ExpectedAudience: "energy-api", MaxCredentialTTL: 4 * time.Hour, Revocations: rev, Now: func() time.Time { return now }},
			Policy:        &ScopedPolicy{},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewFormalRouter(tc.guard)
			req := httptest.NewRequest(http.MethodGet, FormalRoutePrefix+"usage?project=p1", nil)
			req.Header.Set("X-User-ID", "admin")
			req.Header.Set("X-Project-ID", "p1")
			req.Header.Set("X-Tenant-ID", "t1")
			req.Header.Set("X-Forwarded-User", "root")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || rec.Header().Get("Cache-Control") != "no-store" ||
				!strings.Contains(rec.Body.String(), "energy_auth_unconfigured") {
				t.Fatalf("expected fail-closed 401, got %d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
			}
		})
	}
}

func TestMiddlewareRejectsSpoofedSyntheticExpiredAndRevokedIdentities(t *testing.T) {
	now := time.Date(2026, time.November, 15, 12, 0, 0, 0, time.UTC)
	rev := &revocationStub{revoked: map[string]bool{"corp-idp|revoked-user": true, "sess-revoked": true}}

	cases := []struct {
		name       string
		principal  Principal
		authErr    error
		revErr     error
		wantStatus int
	}{
		{"missing credentials", Principal{}, errors.New("no token"), nil, http.StatusUnauthorized},
		{"empty subject", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: ""}}, nil, nil, http.StatusUnauthorized},
		{"untrusted issuer", Principal{Subject: buildingenergy.Subject{Issuer: "rogue-idp", ID: "u1"}, Audience: "energy-api"}, nil, nil, http.StatusUnauthorized},
		{"wrong audience", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "other-app"}, nil, nil, http.StatusUnauthorized},
		{"synthetic preview issuer rejected", Principal{Subject: buildingenergy.Subject{Issuer: "f06-preview", ID: "local-viewer"}, Audience: "energy-api", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}, nil, nil, http.StatusForbidden},
		{"zero IssuedAt and SessionCreatedAt rejected", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", ExpiresAt: now.Add(time.Hour)}, nil, nil, http.StatusUnauthorized},
		{"future IssuedAt rejected", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", IssuedAt: now.Add(time.Second), ExpiresAt: now.Add(time.Hour)}, nil, nil, http.StatusUnauthorized},
		{"future SessionCreatedAt rejected", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", SessionCreatedAt: now.Add(10 * time.Second), ExpiresAt: now.Add(time.Hour)}, nil, nil, http.StatusUnauthorized},
		{"missing expiry rejected", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", IssuedAt: now.Add(-time.Minute)}, nil, nil, http.StatusUnauthorized},
		{"excessive remaining ttl rejected", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", IssuedAt: now, ExpiresAt: now.Add(5 * time.Hour)}, nil, nil, http.StatusUnauthorized},
		{"excessive total lifetime with short remaining ttl rejected", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", IssuedAt: now.Add(-5 * time.Hour), ExpiresAt: now.Add(30 * time.Minute)}, nil, nil, http.StatusUnauthorized},
		{"excessive session lifetime despite fresh IssuedAt rejected", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", SessionCreatedAt: now.Add(-5 * time.Hour), IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(30 * time.Minute)}, nil, nil, http.StatusUnauthorized},
		{"expired token at exact now", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", IssuedAt: now.Add(-time.Hour), ExpiresAt: now}, nil, nil, http.StatusUnauthorized},
		{"expired token in past", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Second)}, nil, nil, http.StatusUnauthorized},
		{"revoked user", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "revoked-user"}, Audience: "energy-api", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}, nil, nil, http.StatusUnauthorized},
		{"revoked session", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", SessionID: "sess-revoked", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}, nil, nil, http.StatusUnauthorized},
		{"revocation backend failure fails closed", Principal{Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}, nil, errors.New("revocation store down"), http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			revCheck := &revocationStub{revoked: rev.revoked, err: tc.revErr}
			guard := &Guard{
				Authenticator: authStub{fn: func(context.Context, *http.Request) (Principal, error) {
					return tc.principal, tc.authErr
				}},
				Identity: ContextIdentitySource{
					AllowedIssuers:   []string{"corp-idp"},
					ExpectedAudience: "energy-api",
					MaxCredentialTTL: 4 * time.Hour,
					Revocations:      revCheck,
					Now:              func() time.Time { return now },
				},
				Policy: &ScopedPolicy{Revocations: revCheck},
			}
			h := guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, FormalRoutePrefix+"usage", nil)
			req.Header.Set("X-User-ID", "spoofed")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus || called {
				t.Fatalf("%s: got status=%d called=%v want=%d body=%s", tc.name, rec.Code, called, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestScopedPolicyEnforcesProjectPartialScopeActionsAndDownloadRevocation(t *testing.T) {
	from, project, s1, s2, p1, p2, cat := testFixture()
	period := buildingenergy.Range{From: from, To: from.Add(24 * time.Hour)}
	now := from.Add(2 * time.Hour)

	reader := buildingenergy.Subject{Issuer: "corp-idp", ID: "reader1"}
	exporter := buildingenergy.Subject{Issuer: "corp-idp", ID: "exporter1"}
	rev := &revocationStub{revoked: map[string]bool{}}

	policy := &ScopedPolicy{
		Revocations: rev,
		Grants: []Grant{
			{
				Subject:    reader,
				Actions:    []buildingenergy.Action{buildingenergy.ReadUsage},
				Project:    project,
				Objects:    []buildingenergy.ObjectKey{s1},
				Points:     []buildingenergy.PointKey{p1},
				ValidRange: buildingenergy.Range{From: from, To: from.Add(48 * time.Hour)},
			},
			{
				Subject:    exporter,
				Actions:    []buildingenergy.Action{buildingenergy.ReadUsage, buildingenergy.ExportUsage, buildingenergy.DownloadReport},
				Project:    project,
				Objects:    []buildingenergy.ObjectKey{s1},
				Points:     []buildingenergy.PointKey{p1},
				ValidRange: buildingenergy.Range{From: from},
			},
		},
	}

	guard := &Guard{
		Identity: ContextIdentitySource{
			AllowedIssuers:   []string{"corp-idp"},
			ExpectedAudience: "energy-api",
			MaxCredentialTTL: 4 * time.Hour,
			Revocations:      rev,
			Now:              func() time.Time { return now },
		},
		Policy: policy,
	}

	readerCtx := WithPrincipal(context.Background(), Principal{Subject: reader, Audience: "energy-api", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)})
	allowedScope := buildingenergy.Scope{Project: project, Period: period, Objects: []buildingenergy.ObjectKey{s1}, Points: []buildingenergy.PointKey{p1}}

	// Authorized read succeeds.
	if _, err := guard.AuthorizeScope(readerCtx, cat, buildingenergy.ReadUsage, allowedScope); err != nil {
		t.Fatalf("authorized read denied: %v", err)
	}

	// Reset catalog call counter and verify all denials happen BEFORE catalog lookup.
	cat.calls = 0
	deniedCases := []struct {
		name   string
		action buildingenergy.Action
		scope  buildingenergy.Scope
	}{
		{"cross-project", buildingenergy.ReadUsage, buildingenergy.Scope{Project: buildingenergy.ProjectKey{TenantID: "t1", ProjectID: "p2"}, Period: period, Objects: []buildingenergy.ObjectKey{s1}, Points: []buildingenergy.PointKey{p1}}},
		{"cross-tenant", buildingenergy.ReadUsage, buildingenergy.Scope{Project: buildingenergy.ProjectKey{TenantID: "t2", ProjectID: "p1"}, Period: period, Objects: []buildingenergy.ObjectKey{s1}, Points: []buildingenergy.PointKey{p1}}},
		{"partial object bundle includes unauthorized s2", buildingenergy.ReadUsage, buildingenergy.Scope{Project: project, Period: period, Objects: []buildingenergy.ObjectKey{s1, s2}, Points: []buildingenergy.PointKey{p1}}},
		{"implicit project-wide denied", buildingenergy.ReadUsage, buildingenergy.Scope{Project: project, Period: period, Points: []buildingenergy.PointKey{p1}}},
		{"unauthorized point p2", buildingenergy.ReadUsage, buildingenergy.Scope{Project: project, Period: period, Objects: []buildingenergy.ObjectKey{s1}, Points: []buildingenergy.PointKey{p1, p2}}},
		{"outside granted period", buildingenergy.ReadUsage, buildingenergy.Scope{Project: project, Period: buildingenergy.Range{From: from.Add(48 * time.Hour), To: from.Add(72 * time.Hour)}, Objects: []buildingenergy.ObjectKey{s1}, Points: []buildingenergy.PointKey{p1}}},
		{"read-only user cannot export", buildingenergy.ExportUsage, allowedScope},
		{"read-only user cannot download", buildingenergy.DownloadReport, allowedScope},
		{"read-only user cannot manage bindings", buildingenergy.ManageBindings, allowedScope},
	}
	for _, tc := range deniedCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := guard.AuthorizeScope(readerCtx, cat, tc.action, tc.scope); !errors.Is(err, buildingenergy.ErrAccessDenied) {
				t.Fatalf("%s: expected ErrAccessDenied, got %v", tc.name, err)
			}
			if cat.calls != 0 {
				t.Fatalf("%s: unauthorized request touched catalog %d times", tc.name, cat.calls)
			}
		})
	}

	// Report export succeeds, then revocation blocks secondary download check.
	exporterCtx := WithPrincipal(context.Background(), Principal{Subject: exporter, Audience: "energy-api", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)})
	if _, err := guard.AuthorizeScope(exporterCtx, cat, buildingenergy.ExportUsage, allowedScope); err != nil {
		t.Fatalf("authorized export denied: %v", err)
	}
	if _, err := guard.AuthorizeDownload(exporterCtx, cat, allowedScope); err != nil {
		t.Fatalf("authorized download denied before revocation: %v", err)
	}
	rev.revoked["corp-idp|exporter1"] = true
	cat.calls = 0
	if _, err := guard.AuthorizeDownload(exporterCtx, cat, allowedScope); !errors.Is(err, buildingenergy.ErrAccessDenied) || cat.calls != 0 {
		t.Fatalf("revoked exporter was allowed to download report: err=%v calls=%d", err, cat.calls)
	}
}

func TestScopedPolicyMappingChangeRequiresOldAndNewTargets(t *testing.T) {
	from, project, s1, s2, p1, _, _ := testFixture()
	point := buildingenergy.PointRef{Project: project, Key: p1}
	profileMeta := buildingenergy.VersionMeta{
		Version:   "prof-v1",
		Effective: buildingenergy.Range{From: from},
		Source:    buildingenergy.SourceRef{System: "meter-reg", ID: "e1"},
	}
	profile := buildingenergy.ProfileRevision{
		Profile: buildingenergy.PointProfile{
			Point: point, Kind: buildingenergy.Cumulative, Energy: buildingenergy.Electricity,
			Unit: "kWh", Multiplier: 1, Cadence: time.Hour, Meta: profileMeta,
		},
		Channel:       buildingenergy.SourceRef{System: "chan", ID: "c1"},
		RegisterEpoch: "epoch-1",
	}
	oldTimeline := buildingenergy.MappingTimeline{
		Point: point, Generation: 1,
		Profiles: []buildingenergy.ProfileRevision{profile},
		Bindings: []buildingenergy.BindingRevision{{
			View: buildingenergy.Space,
			Meta: buildingenergy.VersionMeta{Version: "b1", Effective: buildingenergy.Range{From: from}, Source: buildingenergy.SourceRef{System: "dir", ID: "s1"}},
			Allocations: []buildingenergy.Allocation{{Target: s1, WeightPPM: buildingenergy.FullWeightPPM}},
		}},
	}
	nextTimeline := buildingenergy.MappingTimeline{
		Point: point, Generation: 2,
		Profiles: []buildingenergy.ProfileRevision{profile},
		Bindings: []buildingenergy.BindingRevision{{
			View: buildingenergy.Space,
			Meta: buildingenergy.VersionMeta{Version: "b2", Effective: buildingenergy.Range{From: from}, Source: buildingenergy.SourceRef{System: "dir", ID: "s2"}},
			Allocations: []buildingenergy.Allocation{{Target: s2, WeightPPM: buildingenergy.FullWeightPPM}},
		}},
	}
	publisher := buildingenergy.Subject{Issuer: "corp-idp", ID: "pub1"}
	audit := buildingenergy.MappingAudit{
		Actor: publisher,
		Approval: buildingenergy.MappingApproval{
			At:     from,
			Reason: "space reassignment",
			Ref:    buildingenergy.SourceRef{System: "ticket", ID: "CHG-100"},
		},
	}

	// Only authorized for new target s2 (missing old target s1) -> denied.
	rev := &revocationStub{revoked: map[string]bool{}}
	partialPolicy := &ScopedPolicy{
		Revocations:    rev,
		PreviousReader: mappingReaderStub{timeline: oldTimeline},
		Grants: []Grant{{
			Subject:          publisher,
			Actions:          []buildingenergy.Action{buildingenergy.ManageBindings},
			Project:          project,
			Objects:          []buildingenergy.ObjectKey{s2},
			AllowProjectWide: false,
			Points:           []buildingenergy.PointKey{p1},
			ValidRange:       buildingenergy.Range{From: from},
		}},
	}
	if err := partialPolicy.RequireMappingChange(context.Background(), audit, 1, nextTimeline); !errors.Is(err, buildingenergy.ErrAccessDenied) {
		t.Fatalf("missing old-target permission was accepted: %v", err)
	}

	// Authorized for both s1 and s2 + point p1 (even without AllowProjectWide) -> allowed.
	scopedFullPolicy := &ScopedPolicy{
		Revocations:    rev,
		PreviousReader: mappingReaderStub{timeline: oldTimeline},
		Grants: []Grant{{
			Subject:          publisher,
			Actions:          []buildingenergy.Action{buildingenergy.ManageBindings},
			Project:          project,
			Objects:          []buildingenergy.ObjectKey{s1, s2},
			AllowProjectWide: false,
			Points:           []buildingenergy.PointKey{p1},
			ValidRange:       buildingenergy.Range{From: from},
		}},
	}
	if err := scopedFullPolicy.RequireMappingChange(context.Background(), audit, 1, nextTimeline); err != nil {
		t.Fatalf("scoped publisher with old+new targets was rejected: %v", err)
	}

	// Missing approval reference -> denied.
	badAudit := audit
	badAudit.Approval.Ref = buildingenergy.SourceRef{}
	if err := scopedFullPolicy.RequireMappingChange(context.Background(), badAudit, 1, nextTimeline); !errors.Is(err, buildingenergy.ErrAccessDenied) {
		t.Fatalf("mapping change without approval ref was accepted: %v", err)
	}
}

func TestContextIdentitySourceRejectsIncompleteFormalConfigAndAllowsValidPrincipal(t *testing.T) {
	now := time.Date(2026, time.November, 15, 12, 0, 0, 0, time.UTC)
	rev := &revocationStub{revoked: map[string]bool{}}
	validPrincipal := Principal{
		Subject:   buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"},
		Audience:  "energy-api",
		SessionID: "sess-1",
		IssuedAt:  now.Add(-5 * time.Minute),
		ExpiresAt: now.Add(2 * time.Hour),
	}
	ctx := WithPrincipal(context.Background(), validPrincipal)

	for _, tc := range []struct {
		name string
		src  ContextIdentitySource
	}{
		{"blank issuer entry", ContextIdentitySource{AllowedIssuers: []string{" "}, ExpectedAudience: "energy-api", MaxCredentialTTL: 4 * time.Hour, Revocations: rev, Now: func() time.Time { return now }}},
		{"missing audience", ContextIdentitySource{AllowedIssuers: []string{"corp-idp"}, MaxCredentialTTL: 4 * time.Hour, Revocations: rev, Now: func() time.Time { return now }}},
		{"negative ttl", ContextIdentitySource{AllowedIssuers: []string{"corp-idp"}, ExpectedAudience: "energy-api", MaxCredentialTTL: -time.Minute, Revocations: rev, Now: func() time.Time { return now }}},
		{"missing revocation checker", ContextIdentitySource{AllowedIssuers: []string{"corp-idp"}, ExpectedAudience: "energy-api", MaxCredentialTTL: 4 * time.Hour, Now: func() time.Time { return now }}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.src.ValidateConfig(); !errors.Is(err, ErrIdentityConfigInvalid) {
				t.Fatalf("%s: expected ErrIdentityConfigInvalid, got %v", tc.name, err)
			}
			if _, err := tc.src.AuthenticatedSubject(ctx); !errors.Is(err, ErrIdentityConfigInvalid) {
				t.Fatalf("%s: AuthenticatedSubject bypassed config check: %v", tc.name, err)
			}
		})
	}

	// Valid formal configuration passes middleware and injects verified Principal.
	validSrc := ContextIdentitySource{
		AllowedIssuers:   []string{"corp-idp"},
		ExpectedAudience: "energy-api",
		MaxCredentialTTL: 4 * time.Hour,
		Revocations:      rev,
		Now:              func() time.Time { return now },
	}
	guard := &Guard{
		Authenticator: authStub{fn: func(context.Context, *http.Request) (Principal, error) { return validPrincipal, nil }},
		Identity:      validSrc,
		Policy:        &ScopedPolicy{Revocations: rev},
	}
	called := false
	h := guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		p, ok := PrincipalFromContext(r.Context())
		if !ok || p.Subject != validPrincipal.Subject {
			t.Fatalf("verified principal missing in downstream handler: ok=%v p=%+v", ok, p)
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, FormalRoutePrefix+"usage", nil))
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("valid formal config and principal rejected: code=%d called=%v body=%s", rec.Code, called, rec.Body.String())
	}

	// SessionCreatedAt (equivalent trusted session creation time) without IssuedAt is accepted when within MaxCredentialTTL.
	sessionPrincipal := Principal{
		Subject:          buildingenergy.Subject{Issuer: "corp-idp", ID: "u-session"},
		Audience:         "energy-api",
		SessionID:        "sess-2",
		SessionCreatedAt: now.Add(-10 * time.Minute),
		ExpiresAt:        now.Add(2 * time.Hour),
	}
	if err := validSrc.ValidatePrincipal(context.Background(), sessionPrincipal); err != nil {
		t.Fatalf("valid SessionCreatedAt principal rejected: %v", err)
	}

	// Explicit sentinel checks for zero IssuedAt/SessionCreatedAt, future IssuedAt, expired, over-TTL, and invalid MaxClockSkew.
	if err := validSrc.ValidatePrincipal(context.Background(), Principal{
		Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api", ExpiresAt: now.Add(time.Hour),
	}); !errors.Is(err, ErrIdentityMissingIssuedAt) {
		t.Fatalf("expected ErrIdentityMissingIssuedAt, got %v", err)
	}
	if err := validSrc.ValidatePrincipal(context.Background(), Principal{
		Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api",
		IssuedAt: now.Add(time.Second), ExpiresAt: now.Add(time.Hour),
	}); !errors.Is(err, ErrIdentityFutureIssuedAt) {
		t.Fatalf("expected ErrIdentityFutureIssuedAt, got %v", err)
	}
	if err := validSrc.ValidatePrincipal(context.Background(), Principal{
		Subject: buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"}, Audience: "energy-api",
		IssuedAt: now.Add(-5 * time.Hour), ExpiresAt: now.Add(time.Minute),
	}); !errors.Is(err, ErrIdentityTTLTooLong) {
		t.Fatalf("expected ErrIdentityTTLTooLong, got %v", err)
	}
	badSkew := validSrc
	badSkew.MaxClockSkew = HardMaxClockSkew + time.Second
	if err := badSkew.ValidateConfig(); !errors.Is(err, ErrIdentityConfigInvalid) {
		t.Fatalf("over-cap MaxClockSkew accepted: %v", err)
	}
}

func TestAuthGuardTypedNilDependenciesFailClosedWithoutPanic(t *testing.T) {
	now := time.Date(2026, time.November, 15, 12, 0, 0, 0, time.UTC)
	from, project, s1, _, p1, _, cat := testFixture()
	rev := &revocationStub{revoked: map[string]bool{}}
	validPrincipal := Principal{
		Subject:   buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"},
		Audience:  "energy-api",
		IssuedAt:  now.Add(-time.Minute),
		ExpiresAt: now.Add(time.Hour),
	}
	validAuth := authStub{fn: func(context.Context, *http.Request) (Principal, error) { return validPrincipal, nil }}
	validID := ContextIdentitySource{
		AllowedIssuers:   []string{"corp-idp"},
		ExpectedAudience: "energy-api",
		MaxCredentialTTL: 4 * time.Hour,
		Revocations:      rev,
		Now:              func() time.Time { return now },
	}
	validPolicy := &ScopedPolicy{Revocations: rev}

	var (
		nilAuthPtr *authStub
		nilRevPtr  *revocationStub
		nilPolPtr  *ScopedPolicy
		nilCatPtr  *catalogStub
		nilMapPtr  *mappingReaderStub
		nilNext    http.HandlerFunc
	)

	// 1. Typed nil Authenticator, Policy, Identity.Revocations, ScopedPolicy.Revocations fail closed in Middleware (401, no panic).
	for _, tc := range []struct {
		name  string
		guard *Guard
	}{
		{"typed nil Authenticator", &Guard{Authenticator: nilAuthPtr, Identity: validID, Policy: validPolicy}},
		{"typed nil Policy", &Guard{Authenticator: validAuth, Identity: validID, Policy: nilPolPtr}},
		{"typed nil Identity.Revocations", &Guard{Authenticator: validAuth, Identity: ContextIdentitySource{
			AllowedIssuers: []string{"corp-idp"}, ExpectedAudience: "energy-api", MaxCredentialTTL: 4 * time.Hour, Revocations: nilRevPtr, Now: func() time.Time { return now },
		}, Policy: validPolicy}},
		{"typed nil ScopedPolicy.Revocations", &Guard{Authenticator: validAuth, Identity: validID, Policy: &ScopedPolicy{Revocations: nilRevPtr}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			NewFormalRouter(tc.guard).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, FormalRoutePrefix+"usage", nil))
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "energy_auth_unconfigured") {
				t.Fatalf("%s: expected fail-closed 401, got %d body=%s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}

	// 2. Typed nil next handler returns 501 without panicking.
	rec := httptest.NewRecorder()
	(&Guard{Authenticator: validAuth, Identity: validID, Policy: validPolicy}).Middleware(nilNext).ServeHTTP(
		rec, httptest.NewRequest(http.MethodGet, FormalRoutePrefix+"usage", nil),
	)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("typed nil next handler: expected 501, got %d", rec.Code)
	}

	// 3. Typed nil Policy or Catalog in AuthorizeScope fails closed without panicking.
	ctx := WithPrincipal(context.Background(), validPrincipal)
	scope := buildingenergy.Scope{Project: project, Period: buildingenergy.Range{From: from, To: from.Add(time.Hour)}, Objects: []buildingenergy.ObjectKey{s1}, Points: []buildingenergy.PointKey{p1}}
	if _, err := (&Guard{Identity: validID, Policy: nilPolPtr}).AuthorizeScope(ctx, cat, buildingenergy.ReadUsage, scope); !errors.Is(err, buildingenergy.ErrAccessDenied) {
		t.Fatalf("typed nil Policy in AuthorizeScope: got %v", err)
	}
	allowPolicy := &ScopedPolicy{
		Revocations: rev,
		Grants: []Grant{{
			Subject: validPrincipal.Subject, Actions: []buildingenergy.Action{buildingenergy.ReadUsage},
			Project: project, Objects: []buildingenergy.ObjectKey{s1}, Points: []buildingenergy.PointKey{p1}, ValidRange: buildingenergy.Range{From: from},
		}},
	}
	if _, err := (&Guard{Identity: validID, Policy: allowPolicy}).AuthorizeScope(ctx, nilCatPtr, buildingenergy.ReadUsage, scope); !errors.Is(err, buildingenergy.ErrCatalogScope) {
		t.Fatalf("typed nil Catalog in AuthorizeScope: got %v", err)
	}

	// 4. Typed nil Revocations or PreviousReader in ScopedPolicy fails closed without panicking.
	if err := (&ScopedPolicy{Revocations: nilRevPtr}).Require(ctx, validPrincipal.Subject, buildingenergy.ReadUsage, scope); !errors.Is(err, buildingenergy.ErrAccessDenied) {
		t.Fatalf("typed nil Revocations in ScopedPolicy.Require: got %v", err)
	}
	audit := buildingenergy.MappingAudit{
		Actor:    validPrincipal.Subject,
		Approval: buildingenergy.MappingApproval{At: from, Reason: "chg", Ref: buildingenergy.SourceRef{System: "chg", ID: "1"}},
	}
	point := buildingenergy.PointRef{Project: project, Key: p1}
	tl := buildingenergy.MappingTimeline{
		Point: point, Generation: 2,
		Profiles: []buildingenergy.ProfileRevision{{
			Profile: buildingenergy.PointProfile{
				Point: point, Kind: buildingenergy.Cumulative, Energy: buildingenergy.Electricity, Unit: "kWh", Multiplier: 1, Cadence: time.Hour,
				Meta: buildingenergy.VersionMeta{Version: "p1", Effective: buildingenergy.Range{From: from}, Source: buildingenergy.SourceRef{System: "m", ID: "e1"}},
			},
			Channel: buildingenergy.SourceRef{System: "c", ID: "1"}, RegisterEpoch: "ep1",
		}},
		Bindings: []buildingenergy.BindingRevision{{
			View: buildingenergy.Space,
			Meta: buildingenergy.VersionMeta{Version: "b1", Effective: buildingenergy.Range{From: from}, Source: buildingenergy.SourceRef{System: "d", ID: "s1"}},
			Allocations: []buildingenergy.Allocation{{Target: s1, TargetRevision: "s1-rev1", WeightPPM: buildingenergy.FullWeightPPM}},
		}},
	}
	if err := (&ScopedPolicy{Revocations: rev, PreviousReader: nilMapPtr}).RequireMappingChange(ctx, audit, 1, tl); !errors.Is(err, buildingenergy.ErrAccessDenied) {
		t.Fatalf("typed nil PreviousReader in RequireMappingChange: got %v", err)
	}
}
