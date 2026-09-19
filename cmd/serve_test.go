package cmd

import (
	"strings"
	"testing"
)

func TestRunServeReturnsNotImplemented(t *testing.T) {
	// Even with --deploy, serve must not deploy an app and then claim to be
	// listening when the HTTP server is still a stub.
	autoDeploy = true
	t.Cleanup(func() { autoDeploy = false })

	err := runServe(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "serve is not implemented") {
		t.Fatalf("runServe() error = %v, want not-implemented error", err)
	}
}
