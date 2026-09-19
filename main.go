package main

import (
	"os"

	"github.com/E-Timileyin/sail/cmd"
	"github.com/E-Timileyin/sail/internal/version"
)

// buildVersion is injected by GoReleaser via -ldflags -X main.buildVersion=...
var buildVersion = "dev"

func main() {
	version.Version = buildVersion
	cmd.SetVersion(version.Full())

	// Banner is suppressed for --version, so the output stays parseable.
	if !wantsMachineOutput(os.Args[1:]) {
		cmd.PrintBanner()
	}
	cmd.Execute()
}

func wantsMachineOutput(args []string) bool {
	for _, a := range args {
		switch a {
		case "--version", "-v", "version", "--help", "-h", "help":
			return true
		}
		if a == "--" {
			return false
		}
	}
	return false
}
