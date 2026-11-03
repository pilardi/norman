// Package version provides build-time injected version information.
//
// These variables are overwritten at build time via ldflags:
//
//	go build -ldflags "-X github.com/norman/norman/pkg/version.Version=v0.1.0-9-gabc1234" \
//	          -ldflags "-X github.com/norman/norman/pkg/version.Date=2026-11-02T19:00:00Z"
package version

import "fmt"

// Version is the git describe output (e.g. "v0.1.0-9-gabc1234") or "dev".
var Version = "dev"

// Date is the ISO 8601 build timestamp (e.g. "2026-11-02T19:00:00Z").
var Date = "now"

// String returns a human-readable version string for the given binary name.
func String(binaryName string) string {
	return fmt.Sprintf("%s %s (%s)", binaryName, Version, Date)
}
