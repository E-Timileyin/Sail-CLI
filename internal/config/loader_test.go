package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/E-Timileyin/sail/internal/config"
	"github.com/E-Timileyin/sail/internal/model"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	path := writeConfig(t, `
version: 2
app:
  name: Sail
  environment: development
servers:
  - name: production
    host: example.com
    port: 22
    user: deploy
    key_path: /tmp/id_ed25519
`)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Version != config.CurrentVersion {
		t.Errorf("Version = %d, want %d", cfg.Version, config.CurrentVersion)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(cfg.Servers))
	}
	if got := cfg.Servers[0].Name; got != "production" {
		t.Errorf("server name = %q, want production", got)
	}
}

// TestLoadRejectsUnversionedConfig pins the clean-break behaviour: a v0.1.0 config must
// fail with guidance, not be silently reinterpreted under the new schema.
func TestLoadRejectsUnversionedConfig(t *testing.T) {
	path := writeConfig(t, `
servers:
  - name: ssh-test
    host: localhost
    port: 2222
    user: root
    password: root
`)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected an error for a config with no version field")
	}
	msg := err.Error()
	if !strings.Contains(msg, "version") {
		t.Errorf("error should mention version, got: %v", err)
	}
	if !strings.Contains(msg, "v0.1.0") {
		t.Errorf("error should explain the breaking change from v0.1.0, got: %v", err)
	}
}

// TestLoadRejectsPasswordField covers the secrets fix: a plaintext password must never
// be accepted, and the message must tell the user what to do instead.
func TestLoadRejectsPasswordField(t *testing.T) {
	path := writeConfig(t, `
version: 2
servers:
  - name: production
    host: example.com
    user: deploy
    key_path: /tmp/id_ed25519
    password: hunter2
`)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected an error when a password field is present")
	}
	msg := err.Error()
	if !strings.Contains(msg, "password") {
		t.Errorf("error should name the password field, got: %v", err)
	}
	if !strings.Contains(msg, "key_path") {
		t.Errorf("error should point at key_path, got: %v", err)
	}
}

func TestLoadRejectsRemovedDeploymentBlock(t *testing.T) {
	path := writeConfig(t, `
version: 2
deployment:
  container_name: sail-api
  port: 8080
servers:
  - name: production
    host: example.com
    user: deploy
    key_path: /tmp/id_ed25519
`)

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected an error for the removed deployment block")
	}
	if !strings.Contains(err.Error(), "deployment") {
		t.Errorf("error should name the removed block, got: %v", err)
	}
}

func TestLoadRejectsWrongVersion(t *testing.T) {
	path := writeConfig(t, `
version: 1
servers:
  - name: production
    host: example.com
    user: deploy
    key_path: /tmp/id_ed25519
`)

	if _, err := config.Load(path); err == nil {
		t.Fatal("expected an error for an unsupported version")
	}
}

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "missing key_path",
			body: `
version: 2
servers:
  - name: production
    host: example.com
    user: deploy
`,
			wantErr: "key_path is required",
		},
		{
			name: "missing host",
			body: `
version: 2
servers:
  - name: production
    user: deploy
    key_path: /tmp/id_ed25519
`,
			wantErr: "host is required",
		},
		{
			name: "missing user",
			body: `
version: 2
servers:
  - name: production
    host: example.com
    key_path: /tmp/id_ed25519
`,
			wantErr: "user is required",
		},
		{
			name: "duplicate server names",
			body: `
version: 2
servers:
  - name: production
    host: a.example.com
    user: deploy
    key_path: /tmp/id_ed25519
  - name: production
    host: b.example.com
    user: deploy
    key_path: /tmp/id_ed25519
`,
			wantErr: "more than once",
		},
		{
			name: "no servers",
			body: `
version: 2
servers: []
`,
			wantErr: "no servers",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, tc.body))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := config.Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("expected an error for a missing config file")
	}
}

// TestEnsureKeyFilesReadable covers the permission check that turns a confusing
// "permissions too open" dial failure into a named file.
func TestEnsureKeyFilesReadable(t *testing.T) {
	dir := t.TempDir()

	loose := filepath.Join(dir, "loose_key")
	if err := os.WriteFile(loose, []byte("x"), 0o644); err != nil {
		t.Fatalf("write key: %v", err)
	}
	tight := filepath.Join(dir, "tight_key")
	if err := os.WriteFile(tight, []byte("x"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"0600 key passes", tight, ""},
		{"0644 key is rejected", loose, "chmod 600"},
		{"missing key is reported", filepath.Join(dir, "absent"), "cannot read key_path"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := config.EnsureKeyFilesReadable([]model.ServerStruct{{Name: "s", KeyPath: tc.path}})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
