// Package buildinfo holds the build-time identity of this binary.
//
// Values are injected via -ldflags at link time (see Makefile):
//
//	-X 'go-skeleton/pkg/buildinfo.Version=...'
//	-X 'go-skeleton/pkg/buildinfo.Commit=...'
//	-X 'go-skeleton/pkg/buildinfo.BuildTime=...'
//
// Reasonable defaults are used for `go run` and `go build` without
// ldflags so test code never sees empty strings.
package buildinfo

import "fmt"

// Build-time metadata. Replaced at link time when produced by the
// project Makefile; defaults are intentionally non-empty so consumers
// don't need nil/empty-string guards.
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

// String returns a human-readable one-line summary, useful for startup
// logs and `binary -version`.
func String() string {
	return fmt.Sprintf("version=%s commit=%s buildTime=%s", Version, Commit, BuildTime)
}

// Map returns the metadata as a structured map for JSON responses
// (e.g. /health) and zap log fields.
func Map() map[string]string {
	return map[string]string{
		"version":    Version,
		"commit":     Commit,
		"build_time": BuildTime,
	}
}
