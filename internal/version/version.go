// Package version carries build metadata, stamped at release time.
//
// Defaults describe a local build, so `sail --version` is never blank or misleading when
// the binary was not produced by GoReleaser.
package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the release tag, e.g. v0.1.1. Overridden with -ldflags.
	Version = "dev"
	// Commit is the git SHA the binary was built from.
	Commit = "none"
	// Date is the build timestamp in RFC 3339.
	Date = "unknown"
)

// String returns a single-line version summary, including the toolchain so a bug report
// from a user identifies exactly what they ran.
func String() string {
	return fmt.Sprintf("sail %s (commit %s, built %s, %s)", Version, shortCommit(), Date, goVersion())
}

// Full returns the version plus the platform, for bug reports.
func Full() string {
	return fmt.Sprintf("%s\n%s/%s", String(), runtime.GOOS, runtime.GOARCH)
}

// shortCommit trims a full SHA for display.
func shortCommit() string {
	if len(Commit) > 7 && Commit != "none" {
		return Commit[:7]
	}
	return Commit
}

func goVersion() string {
	return runtime.Version()
}
