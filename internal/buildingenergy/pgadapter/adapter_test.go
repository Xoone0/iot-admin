package pgadapter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/authguard"
)

// NOTE: All tests in this file are adapter contract tests using in-memory
// SQL/row test doubles. They verify Go-to-PostgreSQL encoding, CAS ordering,
// and raw_item -> M1-A/M1-B/M1-C translation. They do NOT substitute for live
// PostgreSQL isolation execution (M1B-04) or real upstream sample signoff (M1B-01..03).

type fakeRow struct {
	vals []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return copyScan(r.vals, dest)
}

type fakeRows struct {
	rows [][]any
	idx  int
	err  error
}

func (r *fakeRows) Next() bool {
	if r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	return copyScan(r.rows[r.idx-1], dest)
}

func (r *fakeRows) Err() error { return r.err }
func (r *fakeRows) Close()     {}

func copyScan(src []any, dest []any) error {
	if len(src) != len(dest) {
		return errors.New("scan column count mismatch")
	}
	for i := range dest {
		dv := reflect.ValueOf(dest[i])
		if dv.Kind() != reflect.Ptr {
			return errors.New("dest is not pointer")
		}
		if src[i] == nil {
			dv.Elem().Set(reflect.Zero(dv.Elem().Type()))
			continue
		}
		sv := reflect.ValueOf(src[i])
		if sv.Type().AssignableTo(dv.Elem().Type()) {
			dv.Elem().Set(sv)
			continue
		}
		if dv.Elem().Kind() == reflect.Ptr && sv.Type().AssignableTo(dv.Elem().Type().Elem()) {
			ptr := reflect.New(dv.Elem().Type().Elem())
			ptr.Elem().Set(sv)
			dv.Elem().Set(ptr)
			continue
		}
		return errors.New("type mismatch scanning " + sv.Type().String() + " into " + dv.Elem().Type().String())
	}
	return nil
}

func TestSQLTypeBoundaryGuards(t *testing.T) {
	if _, err := ValidatePositiveGeneration(0); !errors.Is(err, ErrOverflowGeneration) {
		t.Fatalf("gen 0 accepted: %v", err)
	}
	if _, err := ValidatePositiveGeneration(uint64(math.MaxInt64) + 1); !errors.Is(err, ErrOverflowGeneration) {
		t.Fatalf("uint64 > MaxInt64 accepted: %v", err)
	}
	if v, err := ValidatePositiveGeneration(math.MaxInt64); err != nil || v != math.MaxInt64 {
		t.Fatalf("MaxInt64 rejected: v=%d err=%v", v, err)
	}
	if err := ValidateMicrosecondTime(time.Date(2026, 11, 1, 0, 0, 0, 123, time.UTC)); !errors.Is(err, ErrSubMicrosecondTime) {
		t.Fatalf("sub-microsecond time accepted: %v", err)
	}
	if err := ValidateMicrosecondTime(time.Date(2026, 11, 1, 0, 0, 0, 1000, time.UTC)); err != nil {
		t.Fatalf("exact microsecond time rejected: %v", err)
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if err := ValidateFiniteFloat64(bad); !errors.Is(err, ErrNonFiniteNumeric) {
			t.Fatalf("non-finite %v accepted: %v", bad, err)
		}
	}
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	openRange, err := FormatTstzrange(buildingenergy.Range{From: from})
	if err != nil || openRange != "[2026-11-01T00:00:00Z,)" {
		t.Fatalf("open range = %q (%v)", openRange, err)
	}
	closedRange, err := FormatTstzrange(buildingenergy.Range{From: from, To: from.Add(time.Hour)})
	if err != nil || closedRange != "[2026-11-01T00:00:00Z,2026-11-01T01:00:00Z)" {
		t.Fatalf("closed range = %q (%v)", closedRange, err)
	}
}

type queryMock struct {
	queryRow func(sql string, args ...any) Row
	query    func(sql string, args ...any) (Rows, error)
}

func (m queryMock) QueryRow(_ context.Context, sql string, args ...any) Row {
	return m.queryRow(sql, args...)
}

func (m queryMock) Query(_ context.Context, sql string, args ...any) (Rows, error) {
	return m.query(sql, args...)
}

func TestDirectoryCatalogEnforcesProjectAndTimezoneContracts(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	project := buildingenergy.ProjectKey{TenantID: "t1", ProjectID: "p1"}
	space := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s1"}

	cat := NewDirectoryCatalog(queryMock{
		queryRow: func(sql string, args ...any) Row {
			if strings.Contains(sql, "project_revision") {
				return fakeRow{vals: []any{"t1", "p1", "p1-v1", from, nil, "Asia/Shanghai", "platform-dir", "p1"}}
			}
			return fakeRow{vals: []any{"t1", "p1", "space", "s1", "s1-v1", from, nil, "building", "b1", "b1", "platform-dir", "s1"}}
		},
	})
	gotProj, err := cat.Project(context.Background(), project, from)
	if err != nil || gotProj.Timezone != "Asia/Shanghai" || gotProj.Meta.Version != "p1-v1" {
		t.Fatalf("project lookup failed: %+v (%v)", gotProj, err)
	}
	gotObj, err := cat.Object(context.Background(), project, space, from)
	if err != nil || gotObj.BuildingID != "b1" || gotObj.Parent.ID != "b1" || gotObj.Meta.Version != "s1-v1" {
		t.Fatalf("object lookup failed: %+v (%v)", gotObj, err)
	}

	// Cross-project row leakage rejected.
	badCat := NewDirectoryCatalog(queryMock{
		queryRow: func(string, ...any) Row {
			return fakeRow{vals: []any{"t1", "p2", "p2-v1", from, nil, "Asia/Shanghai", "platform-dir", "p2"}}
		},
	})
	if _, err := badCat.Project(context.Background(), project, from); !errors.Is(err, buildingenergy.ErrCatalogScope) {
		t.Fatalf("cross-project catalog row was accepted: %v", err)
	}

	// Missing row maps to ErrCatalogScope.
	emptyCat := NewDirectoryCatalog(queryMock{
		queryRow: func(string, ...any) Row { return fakeRow{err: pgx.ErrNoRows} },
	})
	if _, err := emptyCat.Object(context.Background(), project, space, from); !errors.Is(err, buildingenergy.ErrCatalogScope) {
		t.Fatalf("missing catalog row did not return ErrCatalogScope: %v", err)
	}
}

type fakeTx struct {
	ops               []string
	allocationArgs    [][]any
	currentGen        *int64
	casRows           int64
	failOnInsert      string
	lockOnlyViolation string
	committed         bool
	rolledBack        bool
}

func (f *fakeTx) Query(context.Context, string, ...any) (Rows, error) { return &fakeRows{}, nil }
func (f *fakeTx) QueryRow(_ context.Context, sql string, _ ...any) Row {
	switch {
	case strings.Contains(sql, "FOR UPDATE"):
		f.ops = append(f.ops, "lock_head")
		return fakeRow{vals: []any{f.currentGen}}
	case strings.Contains(sql, "energy_point_profile a"):
		f.ops = append(f.ops, "check_profile_overlap")
		return fakeRow{vals: []any{f.lockOnlyViolation == "profile"}}
	case strings.Contains(sql, "energy_point_binding a"):
		f.ops = append(f.ops, "check_binding_overlap")
		return fakeRow{vals: []any{f.lockOnlyViolation == "binding"}}
	case strings.Contains(sql, "HAVING COALESCE(SUM"):
		f.ops = append(f.ops, "check_overallocation")
		return fakeRow{vals: []any{f.lockOnlyViolation == "overallocation"}}
	}
	return fakeRow{err: pgx.ErrNoRows}
}
func (f *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(sql, "INSERT INTO energy_mapping.energy_mapping_head"):
		f.ops = append(f.ops, "init_head")
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(sql, "INSERT INTO energy_mapping.energy_mapping_generation"):
		f.ops = append(f.ops, "insert_generation")
		if f.failOnInsert == "generation" {
			return pgconn.CommandTag{}, errors.New("injected generation error")
		}
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(sql, "INSERT INTO energy_mapping.energy_point_profile"):
		f.ops = append(f.ops, "insert_profile")
		if f.failOnInsert == "profile" {
			return pgconn.CommandTag{}, errors.New("injected profile error")
		}
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(sql, "INSERT INTO energy_mapping.energy_point_binding_allocation"):
		f.ops = append(f.ops, "insert_allocation")
		copied := make([]any, len(args))
		copy(copied, args)
		f.allocationArgs = append(f.allocationArgs, copied)
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(sql, "INSERT INTO energy_mapping.energy_point_binding"):
		f.ops = append(f.ops, "insert_binding")
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(sql, "UPDATE energy_mapping.energy_mapping_head"):
		f.ops = append(f.ops, "cas_update_head")
		return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", f.casRows)), nil
	}
	return pgconn.CommandTag{}, errors.New("unexpected sql: " + sql)
}
func (f *fakeTx) Commit(context.Context) error   { f.committed = true; return nil }
func (f *fakeTx) Rollback(context.Context) error { f.rolledBack = true; return nil }

type fakeTransactor struct {
	queryMock
	tx *fakeTx
}

func (f fakeTransactor) BeginReadCommitted(context.Context) (Tx, error) { return f.tx, nil }

func sampleTimeline(from time.Time, gen uint64) buildingenergy.MappingTimeline {
	point := buildingenergy.PointRef{
		Project: buildingenergy.ProjectKey{TenantID: "t1", ProjectID: "p1"},
		Key:     buildingenergy.PointKey{TerminalID: "e1", ItemCode: "power_total"},
	}
	return buildingenergy.MappingTimeline{
		Point:      point,
		Generation: gen,
		Profiles: []buildingenergy.ProfileRevision{{
			Profile: buildingenergy.PointProfile{
				Point: point, Kind: buildingenergy.Cumulative, Energy: buildingenergy.Electricity,
				Unit: "kWh", Multiplier: 2, Cadence: 15 * time.Minute,
				Meta: buildingenergy.VersionMeta{
					Version: "prof-v1", Effective: buildingenergy.Range{From: from},
					Source: buildingenergy.SourceRef{System: "meter-reg", ID: "e1"},
				},
			},
			Channel:       buildingenergy.SourceRef{System: "chan", ID: "c-old"},
			RegisterEpoch: "epoch-A",
		}},
		Bindings: []buildingenergy.BindingRevision{{
			View: buildingenergy.Space,
			Meta: buildingenergy.VersionMeta{
				Version: "bind-v1", Effective: buildingenergy.Range{From: from},
				Source: buildingenergy.SourceRef{System: "platform-dir", ID: "s1-v1"},
			},
			Allocations: []buildingenergy.Allocation{{
				Target:         buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s1"},
				TargetRevision: "s1-v1",
				WeightPPM:      buildingenergy.FullWeightPPM,
			}},
		}},
	}
}

func staticDirectoryCatalog(from time.Time) *DirectoryCatalog {
	return NewDirectoryCatalog(queryMock{
		queryRow: func(sql string, args ...any) Row {
			if strings.Contains(sql, "project_revision") {
				return fakeRow{vals: []any{args[0].(string), args[1].(string), args[1].(string) + "-v1", from, nil, "Asia/Shanghai", "platform-dir", args[1].(string)}}
			}
			objID := args[3].(string)
			return fakeRow{vals: []any{args[0].(string), args[1].(string), args[2].(string), objID, objID + "-v1", from, nil, "building", "b1", "b1", "platform-dir", objID}}
		},
	})
}

func TestMappingStoreCASSequenceConflictRollbackAndReadRoundTrip(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	timeline := sampleTimeline(from, 1)
	cat := staticDirectoryCatalog(from)
	audit := buildingenergy.MappingAudit{
		Actor: buildingenergy.Subject{Issuer: "corp-idp", ID: "pub1"},
		Approval: buildingenergy.MappingApproval{
			At: from, Reason: "initial publish", Ref: buildingenergy.SourceRef{System: "ticket", ID: "CHG-1"},
		},
	}

	// Happy path expected=0 -> gen 1, writing authoritative object_revision.revision ("s1-v1"), NOT BindingRevision.Meta.Version ("bind-v1").
	tx := &fakeTx{currentGen: nil, casRows: 1}
	store := NewMappingStore(fakeTransactor{tx: tx}, cat)
	if err := store.CompareAndSwapMapping(context.Background(), 0, timeline, audit); err != nil {
		t.Fatalf("CAS publish failed: %v", err)
	}
	wantOps := []string{"init_head", "lock_head", "insert_generation", "insert_profile", "insert_binding", "insert_allocation", "cas_update_head"}
	if !tx.committed || tx.rolledBack || !reflect.DeepEqual(tx.ops, wantOps) {
		t.Fatalf("CAS sequence mismatch: committed=%v rolledBack=%v ops=%v", tx.committed, tx.rolledBack, tx.ops)
	}
	if len(tx.allocationArgs) != 1 || tx.allocationArgs[0][8] != "s1-v1" || tx.allocationArgs[0][8] == "bind-v1" {
		t.Fatalf("allocation target_revision did not persist real object_revision.revision: args=%v", tx.allocationArgs)
	}

	// Conflict when observed generation != expected -> rolls back before any insert.
	one := int64(1)
	conflictTx := &fakeTx{currentGen: &one, casRows: 1}
	if err := NewMappingStore(fakeTransactor{tx: conflictTx}, cat).CompareAndSwapMapping(context.Background(), 0, timeline, audit); !errors.Is(err, buildingenergy.ErrMappingConflict) {
		t.Fatalf("expected ErrMappingConflict, got %v", err)
	}
	if conflictTx.committed || !conflictTx.rolledBack || !reflect.DeepEqual(conflictTx.ops, []string{"init_head", "lock_head"}) {
		t.Fatalf("conflict did not roll back cleanly: committed=%v rolledBack=%v ops=%v", conflictTx.committed, conflictTx.rolledBack, conflictTx.ops)
	}

	// Injected insert failure -> rolls back cleanly.
	failTx := &fakeTx{currentGen: nil, casRows: 1, failOnInsert: "profile"}
	if err := NewMappingStore(fakeTransactor{tx: failTx}, cat).CompareAndSwapMapping(context.Background(), 0, timeline, audit); err == nil {
		t.Fatal("expected injected error")
	}
	if failTx.committed || !failTx.rolledBack {
		t.Fatalf("failed publish did not roll back: committed=%v rolledBack=%v", failTx.committed, failTx.rolledBack)
	}

	// ReadMapping round-trip with SQL NULL register_modulus -> Go 0 AND distinct per-object target_revision restored.
	readStore := NewMappingStore(fakeTransactor{
		queryMock: queryMock{
			queryRow: func(sql string, _ ...any) Row {
				return fakeRow{vals: []any{int64(1)}}
			},
			query: func(sql string, _ ...any) (Rows, error) {
				switch {
				case strings.Contains(sql, "energy_point_profile"):
					return &fakeRows{rows: [][]any{{
						"prof-v1", from, nil, "meter-reg", "e1", "chan", "c-old",
						"epoch-A", "cumulative", "electricity", "kWh", 2.0, nil, 900,
					}}}, nil
				case strings.Contains(sql, "energy_point_binding_allocation"):
					return &fakeRows{rows: [][]any{
						{"space", "bind-v1", "s1", "s1-rev-3", 600_000},
						{"space", "bind-v1", "s2", "s2-rev-7", 400_000},
					}}, nil
				default:
					return &fakeRows{rows: [][]any{{
						"space", "bind-v1", from, nil, "platform-dir", "s1-v1",
					}}}, nil
				}
			},
		},
	}, cat)
	got, err := readStore.ReadMapping(context.Background(), timeline.Point, 1)
	if err != nil || got.Generation != 1 || got.Profiles[0].Profile.Modulus != 0 || got.Profiles[0].Profile.Multiplier != 2 ||
		len(got.Bindings[0].Allocations) != 2 ||
		got.Bindings[0].Allocations[0].TargetRevision != "s1-rev-3" ||
		got.Bindings[0].Allocations[1].TargetRevision != "s2-rev-7" {
		t.Fatalf("ReadMapping round-trip mismatch: %+v (%v)", got, err)
	}
}

type mapRevocationChecker struct {
	revoked map[string]bool
	err     error
}

func (r *mapRevocationChecker) IsRevoked(_ context.Context, p authguard.Principal) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	return r.revoked[p.Subject.Issuer+"|"+p.Subject.ID], nil
}

type mapMappingReader struct {
	timelines map[buildingenergy.PointRef]buildingenergy.MappingTimeline
}

func (r mapMappingReader) ReadMapping(_ context.Context, point buildingenergy.PointRef, gen uint64) (buildingenergy.MappingTimeline, error) {
	tl, ok := r.timelines[point]
	if !ok || tl.Generation != gen {
		return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
	}
	return tl, nil
}

type eventResolverFunc func(valueTime time.Time) (string, bool)

func (f eventResolverFunc) ResolveRegisterState(_ context.Context, _ buildingenergy.PointRef, valueTime, _ time.Time, _ string) (string, bool, error) {
	ep, roll := f(valueTime)
	return ep, roll, nil
}

func TestRawMeteringSourceIntegratesWithM1AAndAuthGuard(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	to := from.Add(time.Hour)
	project := buildingenergy.ProjectKey{TenantID: "t1", ProjectID: "p1"}
	point := buildingenergy.PointRef{Project: project, Key: buildingenergy.PointKey{TerminalID: "e1", ItemCode: "power_total"}}
	space := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s1"}

	// 1. Cumulative register delta (1000 -> 1040, multiplier 2 => 80 kWh), late-arrival as_of replay,
	//    dirty raw quality bitmask => Suspect, and clock-skew detection.
	profile := buildingenergy.ProfileRevision{
		Profile: buildingenergy.PointProfile{
			Point: point, Kind: buildingenergy.Cumulative, Energy: buildingenergy.Electricity,
			Unit: "kWh", Multiplier: 2, Modulus: 10000, Cadence: 15 * time.Minute,
			Meta: buildingenergy.VersionMeta{
				Version: "prof-v1", Effective: buildingenergy.Range{From: from},
				Source: buildingenergy.SourceRef{System: "meter-reg", ID: "e1"},
			},
		},
		Channel:       buildingenergy.SourceRef{System: "chan", ID: "c1"},
		RegisterEpoch: "epoch-A",
	}
	timeline := sampleTimeline(from, 1)
	timeline.Profiles[0] = profile

	subject := buildingenergy.Subject{Issuer: "corp-idp", ID: "pilot-reader"}
	rev := &mapRevocationChecker{revoked: map[string]bool{}}
	idSource := authguard.ContextIdentitySource{
		AllowedIssuers:   []string{"corp-idp"},
		ExpectedAudience: "energy-api",
		MaxCredentialTTL: 4 * time.Hour,
		Revocations:      rev,
		Now:              func() time.Time { return to },
	}
	policy := &authguard.ScopedPolicy{
		Revocations: rev,
		Grants: []authguard.Grant{{
			Subject: subject, Actions: []buildingenergy.Action{buildingenergy.ReadUsage},
			Project: project, Objects: []buildingenergy.ObjectKey{space}, Points: []buildingenergy.PointKey{point.Key},
			ValidRange: buildingenergy.Range{From: from},
		}},
	}
	cat := NewDirectoryCatalog(queryMock{
		queryRow: func(sql string, _ ...any) Row {
			if strings.Contains(sql, "project_revision") {
				return fakeRow{vals: []any{"t1", "p1", "p1-v1", from, nil, "Asia/Shanghai", "platform-dir", "p1"}}
			}
			return fakeRow{vals: []any{"t1", "p1", "space", "s1", "s1-v1", from, nil, "building", "b1", "b1", "platform-dir", "s1"}}
		},
	})
	authorizer := &MappingPointAuthorizer{
		Identity: idSource,
		Policy:   policy,
		Catalog:  cat,
		Mappings: mapMappingReader{timelines: map[buildingenergy.PointRef]buildingenergy.MappingTimeline{point: timeline}},
	}
	authCtx := authguard.WithPrincipal(context.Background(), authguard.Principal{
		Subject: subject, Audience: "energy-api", IssuedAt: to.Add(-time.Minute), ExpiresAt: to.Add(time.Hour),
	})
	readCtx := WithPointReadBinding(authCtx, PointReadBinding{
		View: buildingenergy.Space, Generation: 1, Objects: []buildingenergy.ObjectKey{space},
	})

	rawData := []struct {
		id       int64
		at       time.Time
		received time.Time
		val      float64
		unit     string
		quality  int16
		status   string
	}{
		{101, from, from.Add(time.Second), 1000, "kWh", 0, "written"},
		{102, from.Add(15 * time.Minute), from.Add(15*time.Minute + time.Second), 1010, "kWh", 0, "written"},
		{103, from.Add(30 * time.Minute), from.Add(30*time.Minute + time.Second), 1020, "kWh", 0, "written"},
		{104, from.Add(45 * time.Minute), from.Add(45*time.Minute + time.Second), 1030, "kWh", 0, "written"},
		// Final endpoint arrives late at 01:10.
		{105, to, to.Add(10 * time.Minute), 1040, "kWh", 0, "written"},
	}

	mkSource := func(rows [][]any, events RegisterEventResolver) *RawMeteringSource {
		return &RawMeteringSource{
			DB: queryMock{
				query: func(_ string, args ...any) (Rows, error) {
					asOf := args[2].(time.Time)
					var filtered [][]any
					for _, row := range rows {
						recAt := row[1].(time.Time)
						if !recAt.After(asOf) {
							filtered = append(filtered, row)
						}
					}
					return &fakeRows{rows: filtered}, nil
				},
			},
			Authorizer: authorizer,
			Events:     events,
		}
	}

	toRows := func() [][]any {
		out := make([][]any, 0, len(rawData))
		for _, r := range rawData {
			v := r.val
			out = append(out, []any{r.id, r.received.UTC(), point.Key.TerminalID, point.Key.ItemCode, &v, r.unit, r.at.UTC(), r.quality, r.status})
		}
		return out
	}

	// Before late-arrival cutoff (asOf = to + 5m): 3/4 intervals valid => 60 kWh, 1 missing.
	src := mkSource(toRows(), nil)
	earlyWindow := buildingenergy.Window{
		Range: buildingenergy.Range{From: from, To: to}, Timezone: "Asia/Shanghai",
		AsOf: to.Add(5 * time.Minute), Granularity: buildingenergy.Hour,
	}
	earlyRes, err := buildingenergy.CalculateFromSource(readCtx, src, point, earlyWindow)
	if err != nil || earlyRes.Usage == nil || *earlyRes.Usage != 60 || earlyRes.Valid != 3 || earlyRes.Missing != 1 {
		t.Fatalf("as_of early cutoff mismatch: %+v (%v)", earlyRes, err)
	}

	// After late-arrival cutoff (asOf = to + 15m): 4/4 valid => 80 kWh, traceable to iot-raw-item composite key.
	fullWindow := earlyWindow
	fullWindow.AsOf = to.Add(15 * time.Minute)
	fullRes, err := buildingenergy.CalculateFromSource(readCtx, src, point, fullWindow)
	if err != nil || fullRes.Usage == nil || *fullRes.Usage != 80 || fullRes.Valid != 4 || fullRes.Missing != 0 {
		t.Fatalf("full cutoff mismatch: %+v (%v)", fullRes, err)
	}
	if !strings.HasPrefix(fullRes.Intervals[0].Sources[0].ID, "101@") || fullRes.Intervals[0].Sources[0].System != "iot-raw-item" {
		t.Fatalf("missing composite raw_item source lineage: %+v", fullRes.Intervals[0].Sources)
	}

	// Verify Guard.AuthorizeScope also succeeds with the same hardened formal config.
	guard := &authguard.Guard{Identity: idSource, Policy: policy}
	resolved, err := guard.AuthorizeScope(authCtx, cat, buildingenergy.ReadUsage, buildingenergy.Scope{
		Project: project, Period: fullWindow.Range, Objects: []buildingenergy.ObjectKey{space}, Points: []buildingenergy.PointKey{point.Key},
	})
	if err != nil || resolved.Location.String() != "Asia/Shanghai" {
		t.Fatalf("authorized pilot scope failed: %+v (%v)", resolved, err)
	}

	// Dirty raw_item quality bitmask (bit 1 = range violation) => Suspect, never Normal.
	dirtyRows := toRows()
	dirtyRows[2][7] = int16(1) // 00:30 reading flagged quality=1
	dirtyRes, err := buildingenergy.CalculateFromSource(readCtx, mkSource(dirtyRows, nil), point, fullWindow)
	if err != nil || dirtyRes.Suspect != 2 || dirtyRes.Valid != 2 {
		t.Fatalf("dirty raw quality was not mapped to Suspect: %+v (%v)", dirtyRes, err)
	}

	// Confirmed rollover (9998 -> 3, modulus 10000, multiplier 2 => 10 kWh) vs unconfirmed jump (Suspect).
	v1, v2 := 9998.0, 3.0
	wrapRows := [][]any{
		{int64(201), from.Add(time.Second).UTC(), point.Key.TerminalID, point.Key.ItemCode, &v1, "kWh", from.UTC(), int16(0), "written"},
		{int64(202), from.Add(15*time.Minute + time.Second).UTC(), point.Key.TerminalID, point.Key.ItemCode, &v2, "kWh", from.Add(15 * time.Minute).UTC(), int16(0), "written"},
	}
	wrapWin := buildingenergy.Window{Range: buildingenergy.Range{From: from, To: from.Add(time.Hour)}, Timezone: "Asia/Shanghai", AsOf: to, Granularity: buildingenergy.Hour}
	wrapSrc := mkSource(wrapRows, eventResolverFunc(func(at time.Time) (string, bool) {
		return "epoch-A", at.Equal(from.Add(15 * time.Minute))
	}))
	wrapRes, err := buildingenergy.CalculateFromSource(readCtx, wrapSrc, point, wrapWin)
	if err != nil || wrapRes.Usage == nil || *wrapRes.Usage != 10 {
		t.Fatalf("confirmed rollover mismatch: %+v (%v)", wrapRes, err)
	}
	unconfirmedRes, err := buildingenergy.CalculateFromSource(readCtx, mkSource(wrapRows, nil), point, wrapWin)
	if err != nil || unconfirmedRes.Intervals[0].Quality != buildingenergy.Suspect || unconfirmedRes.Intervals[0].Reason != buildingenergy.NegativeJump {
		t.Fatalf("unconfirmed negative jump did not yield Suspect/NegativeJump: %+v (%v)", unconfirmedRes.Intervals[0], err)
	}

	// Clock-skewed raw_item (received_at < value_time) surfaces M1-A ErrInvalidSample explicitly.
	skewVal := 1000.0
	skewRows := [][]any{
		{int64(301), from.Add(-2 * time.Second).UTC(), point.Key.TerminalID, point.Key.ItemCode, &skewVal, "kWh", from.UTC(), int16(0), "written"},
	}
	if _, err := buildingenergy.CalculateFromSource(readCtx, mkSource(skewRows, nil), point, fullWindow); !errors.Is(err, buildingenergy.ErrInvalidSample) {
		t.Fatalf("clock-skewed raw row did not trigger ErrInvalidSample: %v", err)
	}

	// End-to-end M1-A (RawMeteringSource) + M1-B (MappingTimeline) + M1-C (CalculateClassifiedUsage)
	// contract dry-run: 80 kWh split 75% HVAC + 25% Lighting reconciles exactly to total.
	classSnap := buildingenergy.ClassificationSnapshot{
		Project: project,
		Meta: buildingenergy.VersionMeta{
			Version: "cls-v1", Effective: buildingenergy.Range{From: from},
			Source: buildingenergy.SourceRef{System: "taxonomy", ID: "cls-v1"},
		},
		Nodes: []buildingenergy.CategoryNode{
			{ID: "elec-hvac", Name: "HVAC", Energy: buildingenergy.Electricity},
			{ID: "elec-light", Name: "Lighting", Energy: buildingenergy.Electricity},
		},
		Bindings: []buildingenergy.CategoryBinding{{
			Point: point,
			Meta: buildingenergy.VersionMeta{
				Version: "cb-v1", Effective: buildingenergy.Range{From: from},
				Source: buildingenergy.SourceRef{System: "taxonomy", ID: "cb-v1"},
			},
			Allocations: []buildingenergy.CategoryAllocation{
				{CategoryID: "elec-hvac", WeightPPM: 750_000},
				{CategoryID: "elec-light", WeightPPM: 250_000},
			},
		}},
	}
	topoSnap := buildingenergy.CategoryTopologySnapshot{
		Project: project,
		Meta: buildingenergy.VersionMeta{
			Version: "topo-v1", Effective: buildingenergy.Range{From: from},
			Source: buildingenergy.SourceRef{System: "topology", ID: "topo-v1"},
		},
		Boundaries: [][]buildingenergy.CategoryBoundaryContributor{{
			{
				BoundaryContributor: buildingenergy.BoundaryContributor{Point: point, Role: buildingenergy.BoundaryTotal},
				MappingGeneration:   1,
				Channel:             profile.Channel,
			},
		}},
	}
	classReport, err := buildingenergy.CalculateClassifiedUsage(
		buildingenergy.ClassifiedUsageQuery{Project: project, Window: fullWindow, Object: space},
		classSnap,
		topoSnap,
		[]buildingenergy.CategoryPointResult{{Result: fullRes, Mapping: timeline}},
	)
	if err != nil {
		t.Fatalf("M1-C classified usage contract dry-run failed: %v", err)
	}
	if len(classReport.Energy) != 1 || classReport.Energy[0].Total.Usage == nil || *classReport.Energy[0].Total.Usage != 80 {
		t.Fatalf("unexpected classified total: %+v", classReport.Energy)
	}
	var catSum float64
	for _, row := range classReport.Energy[0].Categories {
		if row.Usage != nil {
			catSum += *row.Usage
		}
	}
	if catSum != 80 {
		t.Fatalf("same-window category reconciliation mismatch: sum=%v want=80", catSum)
	}
}

func TestRawMeteringSourceRejectsUnverifiedProjectOwnershipAndUnauthorizedReads(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	period := buildingenergy.Range{From: from, To: to}
	project := buildingenergy.ProjectKey{TenantID: "t1", ProjectID: "p1"}
	otherProject := buildingenergy.ProjectKey{TenantID: "t1", ProjectID: "p2"}
	s1 := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s1"}
	s2 := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s2"}
	p1 := buildingenergy.PointRef{Project: project, Key: buildingenergy.PointKey{TerminalID: "e1", ItemCode: "power_total"}}
	p2 := buildingenergy.PointRef{Project: project, Key: buildingenergy.PointKey{TerminalID: "e2", ItemCode: "power_total"}}
	pForeign := buildingenergy.PointRef{Project: project, Key: buildingenergy.PointKey{TerminalID: "e-other-tenant", ItemCode: "power_total"}}
	pReassigned := buildingenergy.PointRef{Project: project, Key: buildingenergy.PointKey{TerminalID: "e-moved", ItemCode: "power_total"}}
	pUnbound := buildingenergy.PointRef{Project: project, Key: buildingenergy.PointKey{TerminalID: "e-unbound", ItemCode: "power_total"}}

	// Build verified timelines:
	// - p1 bound to s1 over [from, ∞)
	// - p2 bound to s2 over [from, ∞)
	// - pReassigned only moved into p1 at from+30m (did NOT belong to p1 during [from, from+30m))
	// - pUnbound has a profile in p1 but ZERO object bindings
	tl1 := sampleTimeline(from, 1)
	tl2 := sampleTimeline(from, 1)
	tl2.Point = p2
	tl2.Profiles[0].Profile.Point = p2
	tl2.Bindings[0].Allocations = []buildingenergy.Allocation{{Target: s2, TargetRevision: "s2-v1", WeightPPM: buildingenergy.FullWeightPPM}}

	tlReassigned := sampleTimeline(from.Add(30*time.Minute), 1)
	tlReassigned.Point = pReassigned
	tlReassigned.Profiles[0].Profile.Point = pReassigned

	tlUnbound := sampleTimeline(from, 1)
	tlUnbound.Point = pUnbound
	tlUnbound.Profiles[0].Profile.Point = pUnbound
	tlUnbound.Bindings = nil

	mappings := mapMappingReader{timelines: map[buildingenergy.PointRef]buildingenergy.MappingTimeline{
		p1:          tl1,
		p2:          tl2,
		pReassigned: tlReassigned,
		pUnbound:    tlUnbound,
	}}
	cat := NewDirectoryCatalog(queryMock{
		queryRow: func(sql string, args ...any) Row {
			if strings.Contains(sql, "project_revision") {
				return fakeRow{vals: []any{"t1", "p1", "p1-v1", from, nil, "Asia/Shanghai", "platform-dir", "p1"}}
			}
			objID := args[3].(string)
			return fakeRow{vals: []any{"t1", "p1", "space", objID, objID + "-v1", from, nil, "building", "b1", "b1", "platform-dir", objID}}
		},
	})

	subject := buildingenergy.Subject{Issuer: "corp-idp", ID: "reader-p1-s1"}
	wideSubject := buildingenergy.Subject{Issuer: "corp-idp", ID: "reader-p1-wide"}
	otherProjSubject := buildingenergy.Subject{Issuer: "corp-idp", ID: "reader-p2"}
	rev := &mapRevocationChecker{revoked: map[string]bool{}}

	idSource := authguard.ContextIdentitySource{
		AllowedIssuers:   []string{"corp-idp"},
		ExpectedAudience: "energy-api",
		MaxCredentialTTL: 4 * time.Hour,
		Revocations:      rev,
		Now:              func() time.Time { return to },
	}
	policy := &authguard.ScopedPolicy{
		Revocations: rev,
		Grants: []authguard.Grant{
			{
				Subject: subject, Actions: []buildingenergy.Action{buildingenergy.ReadUsage},
				Project: project, Objects: []buildingenergy.ObjectKey{s1}, Points: []buildingenergy.PointKey{p1.Key, p2.Key},
				ValidRange: buildingenergy.Range{From: from},
			},
			{
				Subject: wideSubject, Actions: []buildingenergy.Action{buildingenergy.ReadUsage},
				Project: project, AllowProjectWide: true, AllowAllPoints: true,
				ValidRange: buildingenergy.Range{From: from},
			},
			{
				Subject: otherProjSubject, Actions: []buildingenergy.Action{buildingenergy.ReadUsage},
				Project: otherProject, AllowProjectWide: true, AllowAllPoints: true,
				ValidRange: buildingenergy.Range{From: from},
			},
		},
	}

	makeCtx := func(sub buildingenergy.Subject, objs ...buildingenergy.ObjectKey) context.Context {
		ctx := authguard.WithPrincipal(context.Background(), authguard.Principal{
			Subject: sub, Audience: "energy-api", IssuedAt: to.Add(-time.Minute), ExpiresAt: to.Add(time.Hour),
		})
		return WithPointReadBinding(ctx, PointReadBinding{View: buildingenergy.Space, Generation: 1, Objects: objs})
	}

	dbQueries := 0
	db := queryMock{
		query: func(_ string, _ ...any) (Rows, error) {
			dbQueries++
			return &fakeRows{}, nil
		},
	}
	authorizer := &MappingPointAuthorizer{Identity: idSource, Policy: policy, Catalog: cat, Mappings: mappings}
	src := &RawMeteringSource{DB: db, Authorizer: authorizer}

	cases := []struct {
		name    string
		source  *RawMeteringSource
		ctx     context.Context
		point   buildingenergy.PointRef
		wantErr error
	}{
		{
			name:    "nil authorizer fails closed without touching raw_item",
			source:  &RawMeteringSource{DB: db},
			ctx:     makeCtx(subject, s1),
			point:   p1,
			wantErr: buildingenergy.ErrAccessDenied,
		},
		{
			name:    "request carrying project ID without authenticated principal is denied",
			source:  src,
			ctx:     WithPointReadBinding(context.Background(), PointReadBinding{View: buildingenergy.Space, Generation: 1, Objects: []buildingenergy.ObjectKey{s1}}),
			point:   p1,
			wantErr: buildingenergy.ErrAccessDenied,
		},
		{
			name:    "missing point read binding context is denied",
			source:  src,
			ctx:     authguard.WithPrincipal(context.Background(), authguard.Principal{Subject: subject, Audience: "energy-api", IssuedAt: to.Add(-time.Minute), ExpiresAt: to.Add(time.Hour)}),
			point:   p1,
			wantErr: buildingenergy.ErrAccessDenied,
		},
		{
			name: "incomplete ContextIdentitySource config fails closed",
			source: &RawMeteringSource{
				DB: db,
				Authorizer: &MappingPointAuthorizer{
					Identity: authguard.ContextIdentitySource{AllowedIssuers: []string{"corp-idp"}, Now: func() time.Time { return to }},
					Policy:   policy, Catalog: cat, Mappings: mappings,
				},
			},
			ctx:     makeCtx(subject, s1),
			point:   p1,
			wantErr: buildingenergy.ErrAccessDenied,
		},
		{
			name:    "cross-project caller carrying p1 ProjectID cannot read p1 raw_item",
			source:  src,
			ctx:     makeCtx(otherProjSubject, s1),
			point:   p1,
			wantErr: buildingenergy.ErrAccessDenied,
		},
		{
			name:    "same-project caller authorized only on s1 cannot read terminal e2 bound to s2",
			source:  src,
			ctx:     makeCtx(subject, s1),
			point:   p2,
			wantErr: buildingenergy.ErrAccessDenied,
		},
		{
			name:    "project-wide caller cannot read foreign terminal with no mapping in p1",
			source:  src,
			ctx:     makeCtx(wideSubject, s1),
			point:   pForeign,
			wantErr: buildingenergy.ErrMappingUnavailable,
		},
		{
			name:    "historically reassigned terminal before ownership start is rejected",
			source:  src,
			ctx:     makeCtx(wideSubject, s1),
			point:   pReassigned,
			wantErr: buildingenergy.ErrMappingBoundary,
		},
		{
			name:    "terminal without verified object binding ownership is rejected",
			source:  src,
			ctx:     makeCtx(wideSubject, s1),
			point:   pUnbound,
			wantErr: buildingenergy.ErrMeteringScope,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbQueries = 0
			_, err := tc.source.Load(tc.ctx, tc.point, period, to.Add(15*time.Minute))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("%s: got err=%v, want %v", tc.name, err, tc.wantErr)
			}
			if dbQueries != 0 {
				t.Fatalf("%s: denied read touched raw_item %d times", tc.name, dbQueries)
			}
		})
	}

	// Revoked principal fails closed without touching raw_item.
	rev.revoked["corp-idp|reader-p1-s1"] = true
	dbQueries = 0
	if _, err := src.Load(makeCtx(subject, s1), p1, period, to.Add(15*time.Minute)); !errors.Is(err, buildingenergy.ErrAccessDenied) || dbQueries != 0 {
		t.Fatalf("revoked caller read raw_item: err=%v dbQueries=%d", err, dbQueries)
	}
	delete(rev.revoked, "corp-idp|reader-p1-s1")

	// Defense-in-depth: if DB returns a row with mismatched terminal_id, reject with ErrMeteringScope.
	val := 100.0
	mismatchSrc := &RawMeteringSource{
		DB: queryMock{
			query: func(_ string, _ ...any) (Rows, error) {
				return &fakeRows{rows: [][]any{
					{int64(999), from.Add(time.Second).UTC(), "e-wrong", p1.Key.ItemCode, &val, "kWh", from.UTC(), int16(0), "written"},
				}}, nil
			},
		},
		Authorizer: authorizer,
	}
	if _, err := mismatchSrc.Load(makeCtx(subject, s1), p1, period, to.Add(15*time.Minute)); !errors.Is(err, buildingenergy.ErrMeteringScope) {
		t.Fatalf("mismatched terminal_id row was accepted: %v", err)
	}
}

func TestMappingStoreLockOnlyCheckAndAuthorizedPublish(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	timeline := sampleTimeline(from, 1)
	subject := buildingenergy.Subject{Issuer: "corp-idp", ID: "admin1"}
	approval := buildingenergy.MappingApproval{
		At: from, Reason: "lock-only check", Ref: buildingenergy.SourceRef{System: "ticket", ID: "CHG-2"},
	}

	// Authorized PublishMapping through hardened authguard + LockOnlyCheck happy path.
	cat := staticDirectoryCatalog(from)
	okTx := &fakeTx{currentGen: nil, casRows: 1}
	store := NewMappingStore(fakeTransactor{tx: okTx}, cat)
	store.LockOnlyCheck = true

	rev := &mapRevocationChecker{revoked: map[string]bool{}}
	idSource := authguard.ContextIdentitySource{
		AllowedIssuers:   []string{"corp-idp"},
		ExpectedAudience: "energy-api",
		MaxCredentialTTL: 4 * time.Hour,
		Revocations:      rev,
		Now:              func() time.Time { return from },
	}
	policy := &authguard.ScopedPolicy{
		Revocations: rev,
		Grants: []authguard.Grant{{
			Subject: subject, Actions: []buildingenergy.Action{buildingenergy.ManageBindings},
			Project: timeline.Point.Project, Objects: []buildingenergy.ObjectKey{{Kind: buildingenergy.Space, ID: "s1"}},
			Points: []buildingenergy.PointKey{timeline.Point.Key}, ValidRange: buildingenergy.Range{From: from},
		}},
	}
	ctx := authguard.WithPrincipal(context.Background(), authguard.Principal{
		Subject: subject, Audience: "energy-api", IssuedAt: from.Add(-time.Minute), ExpiresAt: from.Add(time.Hour),
	})
	if err := buildingenergy.PublishMapping(ctx, idSource, policy, store, 0, timeline, approval); err != nil {
		t.Fatalf("authorized lock-only PublishMapping failed: %v", err)
	}
	wantOps := []string{
		"init_head", "lock_head", "insert_generation", "insert_profile", "insert_binding", "insert_allocation",
		"check_profile_overlap", "check_binding_overlap", "check_overallocation", "cas_update_head",
	}
	if !okTx.committed || okTx.rolledBack || !reflect.DeepEqual(okTx.ops, wantOps) {
		t.Fatalf("lock-only ops mismatch: committed=%v rolledBack=%v ops=%v", okTx.committed, okTx.rolledBack, okTx.ops)
	}

	// Lock-only overallocation violation rolls back before CAS update.
	badTx := &fakeTx{currentGen: nil, casRows: 1, lockOnlyViolation: "overallocation"}
	badStore := NewMappingStore(fakeTransactor{tx: badTx}, cat)
	badStore.LockOnlyCheck = true
	if err := buildingenergy.PublishMapping(ctx, idSource, policy, badStore, 0, timeline, approval); !errors.Is(err, buildingenergy.ErrInvalidMapping) {
		t.Fatalf("expected ErrInvalidMapping on lock-only overallocation violation, got %v", err)
	}
	if badTx.committed || !badTx.rolledBack {
		t.Fatalf("lock-only violation did not roll back: committed=%v rolledBack=%v", badTx.committed, badTx.rolledBack)
	}
}

func TestMappingStoreTargetRevisionValidationAcrossDirectoryChangesAndCrossProject(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	change := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	audit := buildingenergy.MappingAudit{
		Actor:    buildingenergy.Subject{Issuer: "corp-idp", ID: "pub1"},
		Approval: buildingenergy.MappingApproval{At: from, Reason: "reorg", Ref: buildingenergy.SourceRef{System: "chg", ID: "10"}},
	}
	s1 := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s1"}
	s2 := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s2"}

	// Directory where s1 has rev "s1-oct" in [Oct 1, Nov 1) and "s1-nov" in [Nov 1, ∞); s2 has "s2-v7" in [Oct 1, ∞).
	versionedCat := NewDirectoryCatalog(queryMock{
		queryRow: func(sql string, args ...any) Row {
			if strings.Contains(sql, "project_revision") {
				return fakeRow{vals: []any{"t1", "p1", "p1-v1", from, nil, "Asia/Shanghai", "platform-dir", "p1"}}
			}
			objID := args[3].(string)
			at := args[4].(time.Time)
			if objID == "s1" {
				if at.Before(change) {
					return fakeRow{vals: []any{"t1", "p1", "space", "s1", "s1-oct", from, &change, "building", "b1", "b1", "platform-dir", "s1"}}
				}
				return fakeRow{vals: []any{"t1", "p1", "space", "s1", "s1-nov", change, nil, "building", "b2", "b2", "platform-dir", "s1"}}
			}
			if objID == "s2" {
				return fakeRow{vals: []any{"t1", "p1", "space", "s2", "s2-v7", from, nil, "building", "b1", "b1", "platform-dir", "s2"}}
			}
			// Cross-project leaked row.
			return fakeRow{vals: []any{"t1", "p2", "space", objID, objID + "-v1", from, nil, "building", "b9", "b9", "platform-dir", objID}}
		},
	})

	// 1. Valid multi-revision split publish persists exact distinct object revisions ("s1-oct", "s2-v7", "s1-nov") — never binding version.
	multiTL := sampleTimeline(from, 1)
	multiTL.Bindings = []buildingenergy.BindingRevision{
		{
			View: buildingenergy.Space,
			Meta: buildingenergy.VersionMeta{Version: "bind-oct", Effective: buildingenergy.Range{From: from, To: change}, Source: buildingenergy.SourceRef{System: "dir", ID: "snap-1"}},
			Allocations: []buildingenergy.Allocation{
				{Target: s1, TargetRevision: "s1-oct", WeightPPM: 600_000},
				{Target: s2, TargetRevision: "s2-v7", WeightPPM: 400_000},
			},
		},
		{
			View: buildingenergy.Space,
			Meta: buildingenergy.VersionMeta{Version: "bind-nov", Effective: buildingenergy.Range{From: change}, Source: buildingenergy.SourceRef{System: "dir", ID: "snap-2"}},
			Allocations: []buildingenergy.Allocation{
				{Target: s1, TargetRevision: "s1-nov", WeightPPM: buildingenergy.FullWeightPPM},
			},
		},
	}
	tx := &fakeTx{currentGen: nil, casRows: 1}
	if err := NewMappingStore(fakeTransactor{tx: tx}, versionedCat).CompareAndSwapMapping(context.Background(), 0, multiTL, audit); err != nil {
		t.Fatalf("multi-revision publish failed: %v", err)
	}
	if len(tx.allocationArgs) != 3 ||
		tx.allocationArgs[0][8] != "s1-oct" ||
		tx.allocationArgs[1][8] != "s2-v7" ||
		tx.allocationArgs[2][8] != "s1-nov" {
		t.Fatalf("persisted target_revision args mismatch: %v", tx.allocationArgs)
	}

	// 2. Rejects BindingRevision.Meta.Version masquerading as TargetRevision, empty TargetRevision,
	//    stale post-change revision, binding outliving object revision, cross-project target, and nil catalog.
	rejectCases := []struct {
		name    string
		cat     buildingenergy.Catalog
		mutate  func(*buildingenergy.MappingTimeline)
		wantErr error
	}{
		{
			name:    "BindingRevision.Meta.Version masquerading as TargetRevision rejected",
			cat:     versionedCat,
			mutate:  func(tl *buildingenergy.MappingTimeline) { tl.Bindings[0].Allocations[0].TargetRevision = "bind-oct" },
			wantErr: buildingenergy.ErrCatalogScope,
		},
		{
			name:    "empty TargetRevision rejected",
			cat:     versionedCat,
			mutate:  func(tl *buildingenergy.MappingTimeline) { tl.Bindings[0].Allocations[0].TargetRevision = "" },
			wantErr: buildingenergy.ErrInvalidMapping,
		},
		{
			name:    "stale revision after directory change rejected",
			cat:     versionedCat,
			mutate:  func(tl *buildingenergy.MappingTimeline) { tl.Bindings[1].Allocations[0].TargetRevision = "s1-oct" },
			wantErr: buildingenergy.ErrCatalogScope,
		},
		{
			name: "open-ended binding referencing closed object revision rejected",
			cat:  versionedCat,
			mutate: func(tl *buildingenergy.MappingTimeline) {
				tl.Bindings = []buildingenergy.BindingRevision{{
					View:        buildingenergy.Space,
					Meta:        buildingenergy.VersionMeta{Version: "bind-bad", Effective: buildingenergy.Range{From: from}, Source: buildingenergy.SourceRef{System: "dir", ID: "snap"}},
					Allocations: []buildingenergy.Allocation{{Target: s1, TargetRevision: "s1-oct", WeightPPM: buildingenergy.FullWeightPPM}},
				}}
			},
			wantErr: buildingenergy.ErrCatalogScope,
		},
		{
			name: "cross-project object rejected",
			cat:  versionedCat,
			mutate: func(tl *buildingenergy.MappingTimeline) {
				tl.Bindings = []buildingenergy.BindingRevision{{
					View:        buildingenergy.Space,
					Meta:        buildingenergy.VersionMeta{Version: "bind-x", Effective: buildingenergy.Range{From: from, To: change}, Source: buildingenergy.SourceRef{System: "dir", ID: "snap"}},
					Allocations: []buildingenergy.Allocation{{Target: buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s-foreign"}, TargetRevision: "s-foreign-v1", WeightPPM: buildingenergy.FullWeightPPM}},
				}}
			},
			wantErr: buildingenergy.ErrCatalogScope,
		},
		{
			name:    "nil catalog fails closed before opening transaction",
			cat:     nil,
			mutate:  func(*buildingenergy.MappingTimeline) {},
			wantErr: buildingenergy.ErrCatalogScope,
		},
	}
	for _, tc := range rejectCases {
		t.Run(tc.name, func(t *testing.T) {
			tl := multiTL
			tl.Bindings = append([]buildingenergy.BindingRevision(nil), multiTL.Bindings...)
			for i := range tl.Bindings {
				tl.Bindings[i].Allocations = append([]buildingenergy.Allocation(nil), tl.Bindings[i].Allocations...)
			}
			tc.mutate(&tl)
			rejTx := &fakeTx{currentGen: nil, casRows: 1}
			err := NewMappingStore(fakeTransactor{tx: rejTx}, tc.cat).CompareAndSwapMapping(context.Background(), 0, tl, audit)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("%s: got err=%v, want %v", tc.name, err, tc.wantErr)
			}
			if len(rejTx.ops) != 0 || rejTx.committed {
				t.Fatalf("%s: rejected publish touched DB tx: ops=%v", tc.name, rejTx.ops)
			}
		})
	}
}

func TestRawMeteringSourceHalfOpenAndCumulativeEffectiveToBoundaries(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	t1 := t0.Add(time.Hour) // Exact boundary between revision 1 [t0, t1) and revision 2 [t1, t2)
	t2 := t1.Add(time.Hour)
	project := buildingenergy.ProjectKey{TenantID: "t1", ProjectID: "p1"}
	point := buildingenergy.PointRef{Project: project, Key: buildingenergy.PointKey{TerminalID: "e1", ItemCode: "power_total"}}
	s1 := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s1"}
	s2 := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s2"}

	// Two adjacent cumulative revisions meeting at effective.To == t1:
	// [t0, t1) -> s1 (s1-v1), [t1, t2) -> s2 (s2-v1), Cadence 30m, Multiplier 1.
	cumTL := buildingenergy.MappingTimeline{
		Point: point, Generation: 1,
		Profiles: []buildingenergy.ProfileRevision{
			{
				Profile: buildingenergy.PointProfile{
					Point: point, Kind: buildingenergy.Cumulative, Energy: buildingenergy.Electricity,
					Unit: "kWh", Multiplier: 1, Cadence: 30 * time.Minute,
					Meta: buildingenergy.VersionMeta{Version: "p-1", Effective: buildingenergy.Range{From: t0, To: t1}, Source: buildingenergy.SourceRef{System: "m", ID: "e1"}},
				},
				Channel: buildingenergy.SourceRef{System: "c", ID: "c1"}, RegisterEpoch: "ep-1",
			},
			{
				Profile: buildingenergy.PointProfile{
					Point: point, Kind: buildingenergy.Cumulative, Energy: buildingenergy.Electricity,
					Unit: "kWh", Multiplier: 1, Cadence: 30 * time.Minute,
					Meta: buildingenergy.VersionMeta{Version: "p-2", Effective: buildingenergy.Range{From: t1, To: t2}, Source: buildingenergy.SourceRef{System: "m", ID: "e1"}},
				},
				Channel: buildingenergy.SourceRef{System: "c", ID: "c1"}, RegisterEpoch: "ep-1",
			},
		},
		Bindings: []buildingenergy.BindingRevision{
			{
				View: buildingenergy.Space,
				Meta: buildingenergy.VersionMeta{Version: "b-1", Effective: buildingenergy.Range{From: t0, To: t1}, Source: buildingenergy.SourceRef{System: "d", ID: "s1"}},
				Allocations: []buildingenergy.Allocation{{Target: s1, TargetRevision: "s1-v1", WeightPPM: buildingenergy.FullWeightPPM}},
			},
			{
				View: buildingenergy.Space,
				Meta: buildingenergy.VersionMeta{Version: "b-2", Effective: buildingenergy.Range{From: t1, To: t2}, Source: buildingenergy.SourceRef{System: "d", ID: "s2"}},
				Allocations: []buildingenergy.Allocation{{Target: s2, TargetRevision: "s2-v1", WeightPPM: buildingenergy.FullWeightPPM}},
			},
		},
	}

	subject := buildingenergy.Subject{Issuer: "corp-idp", ID: "boundary-reader"}
	rev := &mapRevocationChecker{revoked: map[string]bool{}}
	idSource := authguard.ContextIdentitySource{
		AllowedIssuers: []string{"corp-idp"}, ExpectedAudience: "energy-api",
		MaxCredentialTTL: 4 * time.Hour, Revocations: rev, Now: func() time.Time { return t2 },
	}
	policy := &authguard.ScopedPolicy{
		Revocations: rev,
		Grants: []authguard.Grant{{
			Subject: subject, Actions: []buildingenergy.Action{buildingenergy.ReadUsage},
			Project: project, Objects: []buildingenergy.ObjectKey{s1, s2}, Points: []buildingenergy.PointKey{point.Key},
			ValidRange: buildingenergy.Range{From: t0},
		}},
	}
	cat := staticDirectoryCatalog(t0)
	authBase := authguard.WithPrincipal(context.Background(), authguard.Principal{
		Subject: subject, Audience: "energy-api", IssuedAt: t2.Add(-time.Minute), ExpiresAt: t2.Add(time.Hour),
	})
	ctxS1 := WithPointReadBinding(authBase, PointReadBinding{View: buildingenergy.Space, Generation: 1, Objects: []buildingenergy.ObjectKey{s1}})
	ctxS2 := WithPointReadBinding(authBase, PointReadBinding{View: buildingenergy.Space, Generation: 1, Objects: []buildingenergy.ObjectKey{s2}})

	v0, v30, v60, v60Plus1us, v90, v120 := 100.0, 115.0, 140.0, 999.0, 170.0, 200.0
	allCumRows := [][]any{
		{int64(1), t0.Add(time.Second).UTC(), "e1", "power_total", &v0, "kWh", t0.UTC(), int16(0), "written"},
		{int64(2), t0.Add(30*time.Minute + time.Second).UTC(), "e1", "power_total", &v30, "kWh", t0.Add(30 * time.Minute).UTC(), int16(0), "written"},
		// Shared closing/opening boundary reading at exact effective.To == t1.
		{int64(3), t1.Add(time.Second).UTC(), "e1", "power_total", &v60, "kWh", t1.UTC(), int16(0), "written"},
		{int64(5), t1.Add(30*time.Minute + time.Second).UTC(), "e1", "power_total", &v90, "kWh", t1.Add(30 * time.Minute).UTC(), int16(0), "written"},
		{int64(6), t2.Add(time.Second).UTC(), "e1", "power_total", &v120, "kWh", t2.UTC(), int16(0), "written"},
	}

	cumSrc := &RawMeteringSource{
		DB:         queryMock{query: func(_ string, _ ...any) (Rows, error) { return &fakeRows{rows: allCumRows}, nil }},
		Authorizer: &MappingPointAuthorizer{Identity: idSource, Policy: policy, Catalog: cat, Mappings: mapMappingReader{timelines: map[buildingenergy.PointRef]buildingenergy.MappingTimeline{point: cumTL}}},
	}

	// 1. Exact energy conservation across [t0, t1) + [t1, t2):
	//    [t0, t1) gets 140 - 100 = 40 kWh; [t1, t2) gets 200 - 140 = 60 kWh; sum == 100 kWh == 200 - 100.
	res1, err := buildingenergy.CalculateFromSource(ctxS1, cumSrc, point, buildingenergy.Window{
		Range: buildingenergy.Range{From: t0, To: t1}, Timezone: "Asia/Shanghai", AsOf: t2.Add(time.Minute), Granularity: buildingenergy.Hour,
	})
	if err != nil || res1.Usage == nil || *res1.Usage != 40 || res1.Valid != 2 {
		t.Fatalf("[t0, t1) cumulative boundary mismatch: %+v (%v)", res1, err)
	}
	res2, err := buildingenergy.CalculateFromSource(ctxS2, cumSrc, point, buildingenergy.Window{
		Range: buildingenergy.Range{From: t1, To: t2}, Timezone: "Asia/Shanghai", AsOf: t2.Add(time.Minute), Granularity: buildingenergy.Hour,
	})
	if err != nil || res2.Usage == nil || *res2.Usage != 60 || res2.Valid != 2 {
		t.Fatalf("[t1, t2) cumulative boundary mismatch: %+v (%v)", res2, err)
	}
	if *res1.Usage+*res2.Usage != 100 {
		t.Fatalf("adjacent revisions failed energy conservation: %v + %v != 100", *res1.Usage, *res2.Usage)
	}

	// 1b. Row 1 microsecond AFTER effective.To (t1 + 1µs) is strictly excluded from [t0, t1).
	postBoundaryRows := append(append([][]any(nil), allCumRows[:3]...),
		[]any{int64(4), t1.Add(2 * time.Second).UTC(), "e1", "power_total", &v60Plus1us, "kWh", t1.Add(time.Microsecond).UTC(), int16(0), "written"},
	)
	postBoundarySrc := &RawMeteringSource{
		DB:         queryMock{query: func(_ string, _ ...any) (Rows, error) { return &fakeRows{rows: postBoundaryRows}, nil }},
		Authorizer: cumSrc.Authorizer,
	}
	postData, err := postBoundarySrc.Load(ctxS1, point, buildingenergy.Range{From: t0, To: t1}, t2.Add(time.Minute))
	if err != nil || len(postData.Samples) != 3 {
		t.Fatalf("row 1µs after effective.To was not excluded: len=%d err=%v", len(postData.Samples), err)
	}

	// 2. Direct Load on [t0, t0+30m) strictly excludes leaked row at t1 (> period.To).
	subData, err := cumSrc.Load(ctxS1, point, buildingenergy.Range{From: t0, To: t0.Add(30 * time.Minute)}, t2.Add(time.Minute))
	if err != nil || len(subData.Samples) != 2 {
		t.Fatalf("sub-window Load leaked rows beyond period.To: len=%d err=%v", len(subData.Samples), err)
	}

	// 3. Meter swap at exact effective.To == t1 (Events reports new epoch "ep-2") -> final interval Suspect/EpochChange, never negative delta.
	swapSrc := *cumSrc
	swapSrc.Events = eventResolverFunc(func(at time.Time) (string, bool) {
		if at.Equal(t1) {
			return "ep-2", false
		}
		return "ep-1", false
	})
	swapRes, err := buildingenergy.CalculateFromSource(ctxS1, &swapSrc, point, buildingenergy.Window{
		Range: buildingenergy.Range{From: t0, To: t1}, Timezone: "Asia/Shanghai", AsOf: t2.Add(time.Minute), Granularity: buildingenergy.Hour,
	})
	if err != nil || swapRes.Valid != 1 || swapRes.Suspect != 1 || swapRes.Intervals[1].Reason != buildingenergy.EpochChange {
		t.Fatalf("meter swap at effective.To did not mark EpochChange: %+v (%v)", swapRes.Intervals, err)
	}

	// 4. Interval half-open semantics (both start-stamped and IntervalTimeIsEnd=true):
	//    [t0+30m, t1) IS included; [t1, t1+30m) starting at effective.To IS strictly excluded.
	intTL := cumTL
	intTL.Profiles = []buildingenergy.ProfileRevision{{
		Profile: buildingenergy.PointProfile{
			Point: point, Kind: buildingenergy.Interval, Energy: buildingenergy.Electricity,
			Unit: "kWh", Multiplier: 1, Cadence: 30 * time.Minute,
			Meta: buildingenergy.VersionMeta{Version: "pi-1", Effective: buildingenergy.Range{From: t0, To: t1}, Source: buildingenergy.SourceRef{System: "m", ID: "e1"}},
		},
		Channel: buildingenergy.SourceRef{System: "c", ID: "c1"},
	}}
	intAuth := &MappingPointAuthorizer{Identity: idSource, Policy: policy, Catalog: cat, Mappings: mapMappingReader{timelines: map[buildingenergy.PointRef]buildingenergy.MappingTimeline{point: intTL}}}
	q1, q2, qAfter := 12.0, 18.0, 99.0

	// Start-stamped: t0 ([t0, t0+30m)), t0+30m ([t0+30m, t1)), t1 ([t1, t1+30m) -> MUST BE EXCLUDED).
	startRows := [][]any{
		{int64(11), t0.Add(31 * time.Minute).UTC(), "e1", "power_total", &q1, "kWh", t0.UTC(), int16(0), "written"},
		{int64(12), t1.Add(time.Minute).UTC(), "e1", "power_total", &q2, "kWh", t0.Add(30 * time.Minute).UTC(), int16(0), "written"},
		{int64(13), t1.Add(31 * time.Minute).UTC(), "e1", "power_total", &qAfter, "kWh", t1.UTC(), int16(0), "written"},
	}
	intStartSrc := &RawMeteringSource{
		DB:         queryMock{query: func(_ string, _ ...any) (Rows, error) { return &fakeRows{rows: startRows}, nil }},
		Authorizer: intAuth,
	}
	intStartData, err := intStartSrc.Load(ctxS1, point, buildingenergy.Range{From: t0, To: t1}, t2)
	if err != nil || len(intStartData.Samples) != 2 {
		t.Fatalf("start-stamped interval included slot at effective.To: len=%d err=%v", len(intStartData.Samples), err)
	}
	intStartRes, err := buildingenergy.CalculateFromSource(ctxS1, intStartSrc, point, buildingenergy.Window{
		Range: buildingenergy.Range{From: t0, To: t1}, Timezone: "Asia/Shanghai", AsOf: t2, Granularity: buildingenergy.Hour,
	})
	if err != nil || intStartRes.Usage == nil || *intStartRes.Usage != 30 {
		t.Fatalf("start-stamped interval usage mismatch: %+v (%v)", intStartRes, err)
	}

	// End-stamped (IntervalTimeIsEnd=true): t0 ([t0-30m, t0) -> EXCLUDED), t0+30m ([t0, t0+30m)), t1 ([t0+30m, t1)), t1+30m ([t1, t1+30m) -> EXCLUDED).
	endRows := [][]any{
		{int64(20), t0.Add(time.Minute).UTC(), "e1", "power_total", &qAfter, "kWh", t0.UTC(), int16(0), "written"},
		{int64(21), t0.Add(31 * time.Minute).UTC(), "e1", "power_total", &q1, "kWh", t0.Add(30 * time.Minute).UTC(), int16(0), "written"},
		{int64(22), t1.Add(time.Minute).UTC(), "e1", "power_total", &q2, "kWh", t1.UTC(), int16(0), "written"},
		{int64(23), t1.Add(31 * time.Minute).UTC(), "e1", "power_total", &qAfter, "kWh", t1.Add(30 * time.Minute).UTC(), int16(0), "written"},
	}
	intEndSrc := &RawMeteringSource{
		DB:                queryMock{query: func(_ string, _ ...any) (Rows, error) { return &fakeRows{rows: endRows}, nil }},
		Authorizer:        intAuth,
		IntervalTimeIsEnd: true,
	}
	intEndData, err := intEndSrc.Load(ctxS1, point, buildingenergy.Range{From: t0, To: t1}, t2)
	if err != nil || len(intEndData.Samples) != 2 {
		t.Fatalf("end-stamped interval leaked out-of-window slots: len=%d err=%v", len(intEndData.Samples), err)
	}
	intEndRes, err := buildingenergy.CalculateFromSource(ctxS1, intEndSrc, point, buildingenergy.Window{
		Range: buildingenergy.Range{From: t0, To: t1}, Timezone: "Asia/Shanghai", AsOf: t2, Granularity: buildingenergy.Hour,
	})
	if err != nil || intEndRes.Usage == nil || *intEndRes.Usage != 30 {
		t.Fatalf("end-stamped interval usage mismatch: %+v (%v)", intEndRes, err)
	}
}

func TestPGAdapterTypedNilDependenciesFailClosedWithoutPanic(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	project := buildingenergy.ProjectKey{TenantID: "t1", ProjectID: "p1"}
	point := buildingenergy.PointRef{Project: project, Key: buildingenergy.PointKey{TerminalID: "e1", ItemCode: "power_total"}}
	space := buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: "s1"}
	tl := sampleTimeline(from, 1)
	audit := buildingenergy.MappingAudit{
		Actor:    buildingenergy.Subject{Issuer: "corp-idp", ID: "u1"},
		Approval: buildingenergy.MappingApproval{At: from, Reason: "test", Ref: buildingenergy.SourceRef{System: "chg", ID: "1"}},
	}

	var (
		nilPool     *PGPool
		nilCat      *DirectoryCatalog
		nilStore    *MappingStore
		nilAuth     *MappingPointAuthorizer
		nilPolicy   *authguard.ScopedPolicy
		nilEvents   eventResolverFunc
		nilSelector PointBindingSelector
	)

	if _, err := NewDirectoryCatalog(nilPool).Project(context.Background(), project, from); !errors.Is(err, buildingenergy.ErrCatalogScope) {
		t.Fatalf("typed nil pool in DirectoryCatalog.Project: got %v", err)
	}
	if _, err := NewDirectoryCatalog(nilPool).Object(context.Background(), project, space, from); !errors.Is(err, buildingenergy.ErrCatalogScope) {
		t.Fatalf("typed nil pool in DirectoryCatalog.Object: got %v", err)
	}
	if _, err := NewMappingStore(nilPool, nilCat).ReadMapping(context.Background(), point, 1); !errors.Is(err, buildingenergy.ErrMappingUnavailable) {
		t.Fatalf("typed nil pool in MappingStore.ReadMapping: got %v", err)
	}
	if err := NewMappingStore(fakeTransactor{tx: &fakeTx{}}, nilCat).CompareAndSwapMapping(context.Background(), 0, tl, audit); !errors.Is(err, buildingenergy.ErrCatalogScope) {
		t.Fatalf("typed nil catalog in MappingStore.CompareAndSwapMapping: got %v", err)
	}
	if _, err := (&RawMeteringSource{DB: nilPool, Authorizer: nilAuth, Events: nilEvents}).Load(context.Background(), point, buildingenergy.Range{From: from, To: to}, to); !errors.Is(err, buildingenergy.ErrAccessDenied) {
		t.Fatalf("typed nil Authorizer in RawMeteringSource.Load: got %v", err)
	}
	if _, err := (&MappingPointAuthorizer{
		Identity: authguard.ContextIdentitySource{}, Policy: nilPolicy, Catalog: nilCat, Mappings: nilStore, Selector: nilSelector,
	}).AuthorizePointRead(context.Background(), point, buildingenergy.Range{From: from, To: to}); !errors.Is(err, buildingenergy.ErrAccessDenied) {
		t.Fatalf("typed nil Policy in MappingPointAuthorizer: got %v", err)
	}
}
