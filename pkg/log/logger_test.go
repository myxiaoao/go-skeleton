package log

import (
	"context"
	"testing"
)

func TestTraceIDFromMissing(t *testing.T) {
	if got := TraceIDFrom(context.Background()); got != "" {
		t.Errorf("TraceIDFrom on empty ctx = %q, want empty", got)
	}
	//nolint:staticcheck // intentional: verify nil-ctx safe path
	if got := TraceIDFrom(nil); got != "" {
		t.Errorf("TraceIDFrom(nil) = %q, want empty", got)
	}
}

func TestWithTraceIDAndTraceIDFromRoundtrip(t *testing.T) {
	ctx := WithTraceID(context.Background(), "abc")
	if got := TraceIDFrom(ctx); got != "abc" {
		t.Errorf("roundtrip = %q, want abc", got)
	}
}

func TestEnsureTraceIDPreservesExisting(t *testing.T) {
	ctx := WithTraceID(context.Background(), "first")
	ctx = EnsureTraceID(ctx, "second")
	if got := TraceIDFrom(ctx); got != "first" {
		t.Errorf("EnsureTraceID overwrote existing: %q", got)
	}
}

func TestEnsureTraceIDFillsWhenMissing(t *testing.T) {
	ctx := EnsureTraceID(context.Background(), " trace-1 ")
	if got := TraceIDFrom(ctx); got != "trace-1" {
		t.Errorf("EnsureTraceID = %q, want trace-1 (trimmed)", got)
	}
}

func TestEnsureTraceIDIgnoresEmptyInput(t *testing.T) {
	ctx := EnsureTraceID(context.Background(), "   ")
	if got := TraceIDFrom(ctx); got != "" {
		t.Errorf("expected no trace id from blank input, got %q", got)
	}
}

func TestNewTraceIDJoinsTrimmedParts(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		want  string
	}{
		{"plain", []string{"asynq", "task-1"}, "asynq:task-1"},
		{"trims whitespace", []string{" asynq ", " task-2 "}, "asynq:task-2"},
		{"drops empty", []string{"asynq", "", "task-3"}, "asynq:task-3"},
		{"all empty", []string{"", "  "}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NewTraceID(c.parts...); got != c.want {
				t.Errorf("NewTraceID(%v) = %q, want %q", c.parts, got, c.want)
			}
		})
	}
}
