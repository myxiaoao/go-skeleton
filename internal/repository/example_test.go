package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"go-skeleton/internal/model"
)

type dbCapture struct {
	createCalls int
	queries     []string
}

func newDryRunDB(t *testing.T, capture *dbCapture) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(postgres.Open("postgres://u:p@127.0.0.1:5432/db?sslmode=disable"), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("gorm.Open dry run: %v", err)
	}

	if capture == nil {
		return db
	}

	if err := db.Callback().Create().After("gorm:create").Register("test:capture_create", func(tx *gorm.DB) {
		capture.createCalls++
		if tx.Statement != nil {
			capture.queries = append(capture.queries, tx.Statement.SQL.String())
		}
	}); err != nil {
		t.Fatalf("register create callback: %v", err)
	}

	if err := db.Callback().Query().After("gorm:query").Register("test:capture_query", func(tx *gorm.DB) {
		if tx.Statement != nil {
			capture.queries = append(capture.queries, tx.Statement.SQL.String())
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}

	return db
}

func TestWithTxAndDBFromContext(t *testing.T) {
	base := new(gorm.DB)
	tx := new(gorm.DB)

	ctx := WithTx(context.Background(), tx)
	if got := dbFromContext(ctx, base); got != tx {
		t.Fatalf("expected tx db from context, got %#v", got)
	}

	if got := dbFromContext(context.Background(), base); got != base {
		t.Fatalf("expected fallback base db, got %#v", got)
	}
}

func TestExampleRepositoryCreateUsesTransactionFromContext(t *testing.T) {
	baseCapture := &dbCapture{}
	txCapture := &dbCapture{}
	baseDB := newDryRunDB(t, baseCapture)
	txDB := newDryRunDB(t, txCapture)

	repo := NewExampleRepository(baseDB)
	example := &model.Example{Name: "example"}

	if err := repo.Create(WithTx(context.Background(), txDB), example); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if txCapture.createCalls != 1 {
		t.Fatalf("expected tx db create callback once, got %d", txCapture.createCalls)
	}
	if baseCapture.createCalls != 0 {
		t.Fatalf("expected base db create callback not to run, got %d", baseCapture.createCalls)
	}
}

// TestInTxNilArgs 覆盖错误兜底：fn=nil 返 errNilTxFn；外层 ctx 无事务且
// db=nil 返 errNilDB。fn=nil 优先级高于 db=nil（调用者既没说要干什么、
// 又没给连接，先抱怨更具体的 fn）。
func TestInTxNilArgs(t *testing.T) {
	if err := InTx(context.Background(), nil, nil); !errors.Is(err, errNilTxFn) {
		t.Fatalf("fn=nil err = %v, want errNilTxFn", err)
	}
	if err := InTx(context.Background(), nil, func(context.Context) error { return nil }); !errors.Is(err, errNilDB) {
		t.Fatalf("db=nil err = %v, want errNilDB", err)
	}
	// InTxWithOptions 同样的兜底。
	if err := InTxWithOptions(context.Background(), nil, &sql.TxOptions{ReadOnly: true}, nil); !errors.Is(err, errNilTxFn) {
		t.Fatalf("WithOptions fn=nil err = %v, want errNilTxFn", err)
	}
	if err := InTxWithOptions(context.Background(), nil, nil, func(context.Context) error { return nil }); !errors.Is(err, errNilDB) {
		t.Fatalf("WithOptions db=nil err = %v, want errNilDB", err)
	}
}

// TestInTxReusesActiveTransaction 验证嵌套行为：ctx 已携带活跃事务时，
// InTx / InTxWithOptions 都不开新事务、直接调 fn。这是回滚语义的核心保证
// （避免 SAVEPOINT 让外层 rollback 范围被子事务影响），也意味着子调用传
// 的 opts 必须被忽略（isolation 只能在最外层 BeginTx 定，子事务改不动）。
func TestInTxReusesActiveTransaction(t *testing.T) {
	stubTx := new(gorm.DB)
	ctx := WithTx(context.Background(), stubTx)

	called := 0
	if err := InTx(ctx, nil, func(inner context.Context) error {
		called++
		if got := txFromContext(inner); got != stubTx {
			t.Fatalf("expected reused stubTx, got %#v", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("InTx nested: %v", err)
	}

	// 子调用传 ReadOnly opts 也必须被忽略：父事务 RW 时子层无法降级成
	// 只读，强行做会让回滚语义混乱。这里 db=nil + opts 非 nil 都能跑通
	// 就证明走的是"复用 ctx tx"分支而不是"开新事务"分支。
	if err := InTxWithOptions(ctx, nil, &sql.TxOptions{ReadOnly: true}, func(context.Context) error {
		called++
		return nil
	}); err != nil {
		t.Fatalf("InTxWithOptions nested: %v", err)
	}

	if called != 2 {
		t.Fatalf("fn called %d times, want 2", called)
	}
}

// TestInTxPropagatesFnError 验证 fn 报错时 InTx 透传 error，让 GORM
// 回滚事务而不是吞掉错误。嵌套路径（ctx 已有事务）测起来最直接。
func TestInTxPropagatesFnError(t *testing.T) {
	want := errors.New("biz boom")
	ctx := WithTx(context.Background(), new(gorm.DB))
	err := InTxWithOptions(ctx, nil, nil, func(context.Context) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestExampleRepositoryListBuildsCountAndPaginationQuery(t *testing.T) {
	capture := &dbCapture{}
	db := newDryRunDB(t, capture)
	repo := NewExampleRepository(db)

	examples, total, err := repo.List(context.Background(), 10, 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Fatalf("expected dry-run total 0, got %d", total)
	}
	if len(examples) != 0 {
		t.Fatalf("expected dry-run examples empty, got %d", len(examples))
	}
	if len(capture.queries) != 2 {
		t.Fatalf("expected 2 queries (count + list), got %d: %#v", len(capture.queries), capture.queries)
	}

	if got := capture.queries[0]; got != `SELECT count(*) FROM "examples"` {
		t.Fatalf("unexpected count query: %q", got)
	}

	listQuery := capture.queries[1]
	if !strings.Contains(listQuery, `SELECT * FROM "examples"`) {
		t.Fatalf("expected list query to select examples, got %q", listQuery)
	}
	if !strings.Contains(listQuery, "ORDER BY id DESC") {
		t.Fatalf("expected list query to order by id desc, got %q", listQuery)
	}
	if !strings.Contains(listQuery, "LIMIT") || !strings.Contains(listQuery, "OFFSET") {
		t.Fatalf("expected list query to contain limit/offset, got %q", listQuery)
	}
}
