package cmd

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/E-Timileyin/sail/internal/logger"
)

func runServe(t *testing.T) (err error, out string) {
	t.Helper()
	var buf bytes.Buffer
	logger.Initialize(logger.Config{Level: logger.InfoLevel, Format: "text", Output: &buf})
	prevArgs := rootCmd.Args
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"serve"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(io.Discard)
		rootCmd.SetErr(io.Discard)
		_ = prevArgs
	})
	err = rootCmd.Execute()
	return err, buf.String()
}

func TestServeReportsNotImplemented(t *testing.T) {
	err, out := runServe(t)
	if err == nil {
		t.Fatal("expected sail serve to fail while it has no listener")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("error should say the command is not implemented, got %q", err.Error())
	}
	if strings.Contains(out, "Server started successfully") {
		t.Fatalf("must not log success before a listener is accepting: %q", out)
	}
}

func TestServeRejectedUnknownFlags(t *testing.T) {
	var buf bytes.Buffer
	logger.Initialize(logger.Config{Level: logger.InfoLevel, Format: "text", Output: &buf})
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"serve", "--port", "9090"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(io.Discard)
		rootCmd.SetErr(io.Discard)
	})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected unknown --port to fail while serve is a stub")
	}
}

func TestServeDoesNotLogSuccess(t *testing.T) {
	_, out := runServe(t)
	if strings.Contains(out, "Server started successfully") {
		t.Fatalf("success log still emitted: %q", out)
	}
}
