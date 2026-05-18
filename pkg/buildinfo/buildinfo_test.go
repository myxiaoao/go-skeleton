package buildinfo

import (
	"strings"
	"testing"
)

func TestStringContainsAllFields(t *testing.T) {
	got := String()
	for _, want := range []string{"version=", "commit=", "buildTime="} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}

func TestMapHasExpectedKeys(t *testing.T) {
	m := Map()
	for _, want := range []string{"version", "commit", "build_time"} {
		if _, ok := m[want]; !ok {
			t.Errorf("Map() missing key %q (got %#v)", want, m)
		}
	}
}

func TestDefaultsAreNonEmpty(t *testing.T) {
	// 防止下游消费方在 `go run`（没经 ldflags 注入）时遇到空串还得做额外
	// 兜底。如果某个字段确实要允许空，先改 package doc 再删本测试。
	if Version == "" || Commit == "" || BuildTime == "" {
		t.Errorf("defaults must be non-empty, got Version=%q Commit=%q BuildTime=%q",
			Version, Commit, BuildTime)
	}
}
