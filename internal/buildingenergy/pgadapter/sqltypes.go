// Package pgadapter provides PostgreSQL integration adapters for building-energy
// directory lookup, versioned mapping CAS storage, and raw_item metering reads.
//
// It keeps internal/buildingenergy free of SQL/pgx dependencies while enforcing
// exact Go-to-PostgreSQL type bounds (positive bigint, microsecond timestamptz,
// finite numeric, half-open tstzrange) and read-committed CAS semantics.
package pgadapter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

func isNilInterface(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

// rangeCovers reports whether half-open outer [outer.From, outer.To) completely
// covers half-open inner [inner.From, inner.To). A zero To represents +infinity.
func rangeCovers(outer, inner buildingenergy.Range) bool {
	if outer.From.IsZero() || inner.From.IsZero() {
		return false
	}
	if !outer.To.IsZero() && !outer.From.Before(outer.To) {
		return false
	}
	if !inner.To.IsZero() && !inner.From.Before(inner.To) {
		return false
	}
	if inner.From.Before(outer.From) {
		return false
	}
	if inner.To.IsZero() {
		return outer.To.IsZero()
	}
	return outer.To.IsZero() || !inner.To.After(outer.To)
}

var (
	ErrOverflowGeneration = errors.New("generation exceeds PostgreSQL positive bigint range")
	ErrSubMicrosecondTime = errors.New("timestamp has sub-microsecond precision unsupported by PostgreSQL timestamptz")
	ErrNonFiniteNumeric   = errors.New("non-finite float64 cannot be written to PostgreSQL numeric")
)

// Row mirrors pgx.Row for narrow, testable SQL scanning.
type Row interface {
	Scan(dest ...any) error
}

// Rows mirrors pgx.Rows for narrow, testable SQL iteration.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

// Querier executes read-only queries against a pool or transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) Row
}

// Tx represents a single READ COMMITTED PostgreSQL transaction.
type Tx interface {
	Querier
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Transactor starts READ COMMITTED transactions and supports read queries.
type Transactor interface {
	Querier
	BeginReadCommitted(ctx context.Context) (Tx, error)
}

// PGPool adapts a live *pgxpool.Pool to Transactor / Querier.
type PGPool struct {
	Pool *pgxpool.Pool
}

// NewPGPool wraps an initialized pgxpool.Pool.
func NewPGPool(pool *pgxpool.Pool) PGPool { return PGPool{Pool: pool} }

func (p PGPool) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	if p.Pool == nil {
		return nil, errors.New("pgadapter: nil pgxpool")
	}
	return p.Pool.Query(ctx, sql, args...)
}

func (p PGPool) QueryRow(ctx context.Context, sql string, args ...any) Row {
	if p.Pool == nil {
		return errRow{err: errors.New("pgadapter: nil pgxpool")}
	}
	return p.Pool.QueryRow(ctx, sql, args...)
}

func (p PGPool) BeginReadCommitted(ctx context.Context) (Tx, error) {
	if p.Pool == nil {
		return nil, errors.New("pgadapter: nil pgxpool")
	}
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	return pgTx{tx: tx}, nil
}

type pgTx struct{ tx pgx.Tx }

func (t pgTx) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	return t.tx.Query(ctx, sql, args...)
}

func (t pgTx) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return t.tx.QueryRow(ctx, sql, args...)
}

func (t pgTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.tx.Exec(ctx, sql, args...)
}

func (t pgTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t pgTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// ValidatePositiveGeneration verifies Go uint64 fits into positive PostgreSQL bigint.
func ValidatePositiveGeneration(g uint64) (int64, error) {
	if g == 0 || g > math.MaxInt64 {
		return 0, ErrOverflowGeneration
	}
	return int64(g), nil
}

// ValidateExpectedGeneration allows 0 (initial publish) or a positive bigint.
func ValidateExpectedGeneration(g uint64) (*int64, error) {
	if g == 0 {
		return nil, nil
	}
	if g > math.MaxInt64 {
		return nil, ErrOverflowGeneration
	}
	v := int64(g)
	return &v, nil
}

// ValidateMicrosecondTime enforces PostgreSQL timestamptz microsecond exactness.
func ValidateMicrosecondTime(t time.Time) error {
	if t.IsZero() {
		return buildingenergy.ErrInvalidMapping
	}
	if t.Nanosecond()%1000 != 0 {
		return ErrSubMicrosecondTime
	}
	return nil
}

// ValidateFiniteFloat64 rejects NaN and Inf before writing PostgreSQL numeric.
func ValidateFiniteFloat64(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return ErrNonFiniteNumeric
	}
	return nil
}

// FormatTstzrange encodes a half-open [from, to) or open-ended [from,) range
// for PostgreSQL tstzrange columns after checking microsecond precision.
func FormatTstzrange(r buildingenergy.Range) (string, error) {
	if err := ValidateMicrosecondTime(r.From); err != nil {
		return "", err
	}
	fromStr := r.From.UTC().Format("2006-01-02T15:04:05.999999Z07:00")
	if r.To.IsZero() {
		return fmt.Sprintf("[%s,)", fromStr), nil
	}
	if !r.From.Before(r.To) {
		return "", buildingenergy.ErrInvalidMapping
	}
	if err := ValidateMicrosecondTime(r.To); err != nil {
		return "", err
	}
	toStr := r.To.UTC().Format("2006-01-02T15:04:05.999999Z07:00")
	return fmt.Sprintf("[%s,%s)", fromStr, toStr), nil
}

// RangeFromBounds rehydrates buildingenergy.Range from SQL lower(effective)/upper(effective).
func RangeFromBounds(from time.Time, to *time.Time) (buildingenergy.Range, error) {
	if from.IsZero() {
		return buildingenergy.Range{}, buildingenergy.ErrInvalidMapping
	}
	r := buildingenergy.Range{From: from.UTC()}
	if to != nil && !to.IsZero() {
		if !from.Before(*to) {
			return buildingenergy.Range{}, buildingenergy.ErrInvalidMapping
		}
		r.To = to.UTC()
	}
	return r, nil
}
