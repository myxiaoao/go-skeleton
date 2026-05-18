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
	// Guards consumers from needing empty-string checks when running via
	// `go run` without ldflags. If you intentionally set one to empty,
	// update the package doc and remove this test.
	if Version == "" || Commit == "" || BuildTime == "" {
		t.Errorf("defaults must be non-empty, got Version=%q Commit=%q BuildTime=%q",
			Version, Commit, BuildTime)
	}
}
