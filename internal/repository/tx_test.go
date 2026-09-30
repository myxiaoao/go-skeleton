package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---------------------------------------------------------------------------
// 本包共享的手写 mock。放在这里（不是 example_test.go）是为了让它们在
// `make drop-example` 之后依然存在。

// mockDBTX 用带函数字段的方式实现 sqlcdb.DBTX。
type mockDBTX struct {
	execFunc     func(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error)
	queryFunc    func(ctx context.Context, query string, args ...any) (pgx.Rows, error)
	queryRowFunc func(ctx context.Context, query string, args ...any) pgx.Row
}

func (m *mockDBTX) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return m.execFunc(ctx, query, args...)
}

func (m *mockDBTX) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return m.queryFunc(ctx, query, args...)
}

func (m *mockDBTX) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return m.queryRowFunc(ctx, query, args...)
}

// mockDB 实现 DB：既能当 DBTX 用，也能开事务。
type mockDB struct {
	mockDBTX
	beginFunc func(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

func (m *mockDB) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	return m.beginFunc(ctx, opts)
}

// mockTx 内嵌一个 nil 的 pgx.Tx：下面没重写的方法一旦被调用就会 panic，
// 用来暴露测试没预期到的调用，而不是悄悄放行。
type mockTx struct {
	pgx.Tx
	dbtx       *mockDBTX
	committed  bool
	rolledBack bool
	commitErr  error // 非 nil 时 Commit 返回它，且事务不算已提交
}

func (m *mockTx) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return m.dbtx.Exec(ctx, query, args...)
}

func (m *mockTx) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return m.dbtx.Query(ctx, query, args...)
}

func (m *mockTx) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return m.dbtx.QueryRow(ctx, query, args...)
}

func (m *mockTx) Commit(context.Context) error {
	if m.commitErr != nil {
		return m.commitErr
	}
	m.committed = true
	return nil
}

// Rollback 模拟 pgx 的行为：commit/rollback 之后再调返回 ErrTxClosed，
// pgx.BeginTxFunc 里的 deferred rollback 会忽略这个错误。
func (m *mockTx) Rollback(context.Context) error {
	if m.committed || m.rolledBack {
		return pgx.ErrTxClosed
	}
	m.rolledBack = true
	return nil
}

// mockRow 实现 pgx.Row。
type mockRow struct {
	values []any
	err    error
}

func (r mockRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return assign(dest, r.values)
}

// mockRows 实现 sqlc 生成的 :many 代码用到的 pgx.Rows 子集
// （Next / Scan / Close / Err）；内嵌的 nil 接口对其余方法会 panic。
type mockRows struct {
	pgx.Rows
	data [][]any
	pos  int
}

func (r *mockRows) Next() bool {
	r.pos++
	return r.pos <= len(r.data)
}

func (r *mockRows) Scan(dest ...any) error { return assign(dest, r.data[r.pos-1]) }
func (r *mockRows) Close()                 {}
func (r *mockRows) Err() error             { return nil }

// assign 把 values 拷贝进 scan 目标（仅测试用的反射小工具）。
func assign(dest, values []any) error {
	if len(dest) != len(values) {
		return fmt.Errorf("scan: %d destinations, %d values", len(dest), len(values))
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(values[i]))
	}
	return nil
}

// newBeginDB 返回一个 BeginTx 会交出 tx 并记录 opts 的 DB。
func newBeginDB(tx *mockTx, gotOpts *pgx.TxOptions) *mockDB {
	return &mockDB{beginFunc: func(_ context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
		if gotOpts != nil {
			*gotOpts = opts
		}
		return tx, nil
	}}
}

// TestRowMocksScan 保证共享的 row mock 本身是正确的；同时让它们在
// `make drop-example` 删掉 example_test.go 之后仍被使用（否则 unused 检查
// 会失败）。
func TestRowMocksScan(t *testing.T) {
	var (
		id   int64
		name string
	)
	if err := (mockRow{values: []any{int64(1), "a"}}).Scan(&id, &name); err != nil || id != 1 || name != "a" {
		t.Fatalf("mockRow scan: id=%d name=%q err=%v", id, name, err)
	}
	if err := (mockRow{values: []any{int64(1)}}).Scan(&id, &name); err == nil {
		t.Fatal("expected arity mismatch error")
	}

	rows := &mockRows{data: [][]any{{int64(2)}, {int64(3)}}}
	var got []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("mockRows scan: %v", err)
		}
		got = append(got, v)
	}
	rows.Close()
	if rows.Err() != nil || !slices.Equal(got, []int64{2, 3}) {
		t.Fatalf("mockRows = %v, want [2 3]", got)
	}
}

// ---------------------------------------------------------------------------
// tx.go 测试

func TestWithTxAndDBFromContext(t *testing.T) {
	base := &mockDB{}
	tx := &mockDBTX{}

	if got := dbFromContext(WithTx(t.Context(), tx), base); got != tx {
		t.Fatalf("expected tx from context, got %#v", got)
	}
	if got := dbFromContext(t.Context(), base); got != base {
		t.Fatalf("expected fallback base db, got %#v", got)
	}
}

// TestInTxNilArgs：fn=nil 优先于 db=nil 报出（更具体的错误信息）。
func TestInTxNilArgs(t *testing.T) {
	if err := InTx(t.Context(), nil, nil); !errors.Is(err, errNilTxFn) {
		t.Fatalf("fn=nil err = %v, want errNilTxFn", err)
	}
	if err := InTx(t.Context(), nil, func(context.Context) error { return nil }); !errors.Is(err, errNilDB) {
		t.Fatalf("db=nil err = %v, want errNilDB", err)
	}
	if err := InTxWithOptions(t.Context(), nil, &sql.TxOptions{ReadOnly: true}, nil); !errors.Is(err, errNilTxFn) {
		t.Fatalf("WithOptions fn=nil err = %v, want errNilTxFn", err)
	}
	if err := InTxWithOptions(t.Context(), nil, nil, func(context.Context) error { return nil }); !errors.Is(err, errNilDB) {
		t.Fatalf("WithOptions db=nil err = %v, want errNilDB", err)
	}
}

// TestInTxReturnsCommitError：fn 成功但 Commit 失败时，InTx 必须把 Commit 的
// 错误返回给调用方，而不是吞掉当成功。
func TestInTxReturnsCommitError(t *testing.T) {
	commitErr := errors.New("commit failed")
	tx := &mockTx{dbtx: &mockDBTX{}, commitErr: commitErr}
	db := newBeginDB(tx, nil)

	err := InTx(t.Context(), db, func(context.Context) error { return nil })
	if !errors.Is(err, commitErr) {
		t.Fatalf("InTx err = %v, want commit error", err)
	}
	if tx.committed {
		t.Fatal("tx should not be marked committed when Commit fails")
	}
}

func TestInTxCommitsOnSuccess(t *testing.T) {
	tx := &mockTx{dbtx: &mockDBTX{}}
	db := newBeginDB(tx, nil)

	err := InTx(t.Context(), db, func(txCtx context.Context) error {
		if got := dbFromContext(txCtx, db); got != tx {
			t.Fatalf("txCtx should carry the tx, got %#v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("committed=%v rolledBack=%v, want commit only", tx.committed, tx.rolledBack)
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	tx := &mockTx{dbtx: &mockDBTX{}}
	want := errors.New("biz boom")

	err := InTx(t.Context(), newBeginDB(tx, nil), func(context.Context) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("committed=%v rolledBack=%v, want rollback only", tx.committed, tx.rolledBack)
	}
}

func TestInTxRollsBackAndRepanicsOnPanic(t *testing.T) {
	tx := &mockTx{dbtx: &mockDBTX{}}
	defer func() {
		if r := recover(); r != "boom" {
			t.Fatalf("recover = %v, want boom", r)
		}
		if tx.committed || !tx.rolledBack {
			t.Fatalf("committed=%v rolledBack=%v, want rollback only", tx.committed, tx.rolledBack)
		}
	}()
	_ = InTx(t.Context(), newBeginDB(tx, nil), func(context.Context) error { panic("boom") })
	t.Fatal("InTx should re-panic")
}

func TestInTxPropagatesBeginError(t *testing.T) {
	want := errors.New("begin failed")
	db := &mockDB{beginFunc: func(context.Context, pgx.TxOptions) (pgx.Tx, error) { return nil, want }}
	if err := InTx(t.Context(), db, func(context.Context) error { return nil }); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// TestInTxReusesActiveTransaction：嵌套调用复用外层事务，并且忽略 opts
// （isolation 只能在 BEGIN 时设置）。
func TestInTxReusesActiveTransaction(t *testing.T) {
	outer := &mockDBTX{}
	ctx := WithTx(t.Context(), outer)
	db := &mockDB{beginFunc: func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
		t.Fatal("nested call must not begin a new transaction")
		return nil, nil
	}}

	called := 0
	for _, opts := range []*sql.TxOptions{nil, {Isolation: sql.LevelSerializable, ReadOnly: true}} {
		err := InTxWithOptions(ctx, db, opts, func(inner context.Context) error {
			called++
			if got := dbFromContext(inner, db); got != outer {
				t.Fatalf("expected reused outer tx, got %#v", got)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("nested InTxWithOptions: %v", err)
		}
	}
	if called != 2 {
		t.Fatalf("fn called %d times, want 2", called)
	}
}

func TestToPgxTxOptions(t *testing.T) {
	cases := []struct {
		name    string
		in      *sql.TxOptions
		want    pgx.TxOptions
		wantErr bool
	}{
		{"nil", nil, pgx.TxOptions{}, false},
		{"default", &sql.TxOptions{}, pgx.TxOptions{}, false},
		{"read uncommitted", &sql.TxOptions{Isolation: sql.LevelReadUncommitted}, pgx.TxOptions{IsoLevel: pgx.ReadUncommitted}, false},
		{"read committed", &sql.TxOptions{Isolation: sql.LevelReadCommitted}, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, false},
		{"repeatable read only", &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, false},
		{"serializable", &sql.TxOptions{Isolation: sql.LevelSerializable}, pgx.TxOptions{IsoLevel: pgx.Serializable}, false},
		{"snapshot unsupported", &sql.TxOptions{Isolation: sql.LevelSnapshot}, pgx.TxOptions{}, true},
		{"linearizable unsupported", &sql.TxOptions{Isolation: sql.LevelLinearizable}, pgx.TxOptions{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := toPgxTxOptions(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestInTxUnsupportedIsolationDoesNotBegin(t *testing.T) {
	db := &mockDB{beginFunc: func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
		t.Fatal("BeginTx must not be called for unsupported isolation")
		return nil, nil
	}}
	err := InTxWithOptions(t.Context(), db, &sql.TxOptions{Isolation: sql.LevelSnapshot}, func(context.Context) error { return nil })
	if err == nil {
		t.Fatal("expected error for unsupported isolation")
	}
}

func TestTxManagerDelegates(t *testing.T) {
	tx := &mockTx{dbtx: &mockDBTX{}}
	var gotOpts pgx.TxOptions
	m := NewTxManager(newBeginDB(tx, &gotOpts))

	if err := m.InTx(t.Context(), func(context.Context) error { return nil }); err != nil || !tx.committed {
		t.Fatalf("InTx err=%v committed=%v", err, tx.committed)
	}

	tx2 := &mockTx{dbtx: &mockDBTX{}}
	m = NewTxManager(newBeginDB(tx2, &gotOpts))
	opts := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	if err := m.InTxWithOptions(t.Context(), opts, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("InTxWithOptions: %v", err)
	}
	if want := (pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}); gotOpts != want {
		t.Fatalf("opts = %+v, want %+v", gotOpts, want)
	}
}
