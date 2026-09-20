package cmd

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/E-Timileyin/sail/internal/logger"
	"github.com/sirupsen/logrus"
)

func TestServeReportsNotImplemented(t *testing.T) {
	var buf bytes.Buffer
	logger.Initialize(logger.Config{Level: logger.InfoLevel, Format: "text", Output: &buf})

	serverPort = "8080"
	autoDeploy = false
	err := serveCmd.RunE(serveCmd, nil)
	if err == nil {
		t.Fatal("expected sail serve to fail while it has no listener")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not implemented") {
		t.Fatalf("error should say the command is not implemented, got %q", msg)
	}
	if !strings.Contains(msg, "8080") {
		t.Fatalf("error should name the requested port, got %q", msg)
	}

	out := buf.String()
	if strings.Contains(out, "Server started successfully") {
		t.Fatalf("must not log success before a listener is accepting: %q", out)
	}
}

func TestServeHonorsPortInError(t *testing.T) {
	logger.Initialize(logger.Config{Level: logger.InfoLevel, Format: "text", Output: io.Discard})
	serverPort = "9999"
	err := serveCmd.RunE(serveCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "9999") {
		t.Fatalf("got %q", err.Error())
	}
}

func TestServeDoesNotLogSuccess(t *testing.T) {
	var buf bytes.Buffer
	logger.Initialize(logger.Config{Level: logger.InfoLevel, Format: "text", Output: &buf})
	if logger.Log != nil {
		logger.Log.SetOutput(&buf)
		logger.Log.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true})
	}
	serverPort = "8080"
	_ = serveCmd.RunE(serveCmd, nil)
	if strings.Contains(buf.String(), "Server started successfully") {
		t.Fatalf("success log still emitted: %q", buf.String())
	}
}
