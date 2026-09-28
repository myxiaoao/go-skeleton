package repository

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go-skeleton/internal/model"
)

func TestExampleRepositoryCreateUsesTransactionFromContext(t *testing.T) {
	now := time.Now()
	var gotSQL string
	var gotArgs []any
	txDBTX := &mockDBTX{queryRowFunc: func(_ context.Context, query string, args ...any) pgx.Row {
		gotSQL, gotArgs = query, args
		return mockRow{values: []any{int64(7), "example", now, now}}
	}}
	base := &mockDB{mockDBTX: mockDBTX{queryRowFunc: func(context.Context, string, ...any) pgx.Row {
		t.Fatal("base db must not be used inside a transaction")
		return nil
	}}}

	example := &model.Example{Name: "example"}
	if err := NewExampleRepository(base).Create(WithTx(t.Context(), txDBTX), example); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(gotSQL, "INSERT INTO examples") {
		t.Fatalf("unexpected SQL: %q", gotSQL)
	}
	if !slices.Equal(gotArgs, []any{"example"}) {
		t.Fatalf("args = %#v, want [example]", gotArgs)
	}
	if example.ID != 7 || !example.CreatedAt.Equal(now) || !example.UpdatedAt.Equal(now) {
		t.Fatalf("example not populated from RETURNING: %+v", example)
	}
}

func TestExampleRepositoryCreatePropagatesError(t *testing.T) {
	want := errors.New("insert failed")
	db := &mockDB{mockDBTX: mockDBTX{queryRowFunc: func(context.Context, string, ...any) pgx.Row {
		return mockRow{err: want}
	}}}
	if err := NewExampleRepository(db).Create(t.Context(), &model.Example{Name: "x"}); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestExampleRepositoryListRunsInReadOnlySnapshot(t *testing.T) {
	now := time.Now()
	var listSQL string
	var listArgs []any
	tx := &mockTx{dbtx: &mockDBTX{
		queryRowFunc: func(_ context.Context, query string, _ ...any) pgx.Row {
			if !strings.Contains(query, "count(*)") {
				t.Errorf("expected count query, got %q", query)
			}
			return mockRow{values: []any{int64(2)}}
		},
		queryFunc: func(_ context.Context, query string, args ...any) (pgx.Rows, error) {
			listSQL, listArgs = query, args
			return &mockRows{data: [][]any{
				{int64(2), "b", now, now},
				{int64(1), "a", now, now},
			}}, nil
		},
	}}
	var gotOpts pgx.TxOptions

	examples, total, err := NewExampleRepository(newBeginDB(tx, &gotOpts)).List(t.Context(), 10, 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := (pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}); gotOpts != want {
		t.Fatalf("tx opts = %+v, want %+v", gotOpts, want)
	}
	if !tx.committed {
		t.Fatal("read-only tx should be committed")
	}
	if total != 2 || len(examples) != 2 || examples[0].ID != 2 || examples[1].Name != "a" {
		t.Fatalf("total=%d examples=%+v", total, examples)
	}
	for _, frag := range []string{"ORDER BY id DESC", "LIMIT", "OFFSET"} {
		if !strings.Contains(listSQL, frag) {
			t.Fatalf("list SQL missing %q: %q", frag, listSQL)
		}
	}
	// sqlc 生成的 ListExamples 把 $1 绑给 Off、$2 绑给 Lim（q.db.Query(ctx,
	// listExamples, arg.Off, arg.Lim)），所以这里按 (offset, limit) 顺序断言。
	if !slices.Equal(listArgs, []any{int64(3), int64(10)}) {
		t.Fatalf("list args = %#v, want [3 10] (offset, limit) as int64", listArgs)
	}
}

func TestExampleRepositoryListEmptyReturnsNonNilSlice(t *testing.T) {
	tx := &mockTx{dbtx: &mockDBTX{
		queryRowFunc: func(context.Context, string, ...any) pgx.Row { return mockRow{values: []any{int64(0)}} },
		queryFunc:    func(context.Context, string, ...any) (pgx.Rows, error) { return &mockRows{}, nil },
	}}
	examples, total, err := NewExampleRepository(newBeginDB(tx, nil)).List(t.Context(), 20, 0)
	if err != nil || total != 0 || examples == nil || len(examples) != 0 {
		t.Fatalf("examples=%#v total=%d err=%v, want empty non-nil slice", examples, total, err)
	}
}
