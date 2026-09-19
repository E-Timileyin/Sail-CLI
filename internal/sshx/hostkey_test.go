package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/E-Timileyin/sail/internal/domain"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// testKey generates a real ed25519 signer. Host-key verification deals in real
// ssh.PublicKey values, so a fake would not exercise the code under test.
func testKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap public key: %v", err)
	}
	return sshPub
}

// writeKnownHosts writes entries for host:port into a temp file.
func writeKnownHosts(t *testing.T, entries map[string]ssh.PublicKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")

	var b strings.Builder
	for addr, key := range entries {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("bad addr %q: %v", addr, err)
		}
		hostField := host
		if port != "22" {
			hostField = "[" + host + "]:" + port
		}
		b.WriteString(knownhosts.Line([]string{hostField}, key) + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	return path
}

func TestHostKeyCallback(t *testing.T) {
	const addr = "example.com:22"
	good := testKey(t)
	other := testKey(t)

	tests := []struct {
		name            string
		policy          domain.TrustPolicy
		known           map[string]ssh.PublicKey
		connectAddr     string
		presentedKey    ssh.PublicKey
		wantErr         bool
		wantUnknownHost bool
		wantMismatch    bool
	}{
		{
			name:         "matching key is accepted",
			policy:       domain.Strict,
			known:        map[string]ssh.PublicKey{addr: good},
			connectAddr:  addr,
			presentedKey: good,
		},
		{
			name:         "unknown host is rejected under strict policy",
			policy:       domain.Strict,
			known:        map[string]ssh.PublicKey{"other.com:22": other},
			connectAddr:  addr,
			presentedKey: good,
			wantErr:      true,
		},
		{
			name:         "mismatched key is rejected even under accept-new",
			policy:       domain.AcceptNew,
			known:        map[string]ssh.PublicKey{addr: other},
			connectAddr:  addr,
			presentedKey: good,
			wantErr:      true,
			wantMismatch: true,
		},
		{
			name:         "mismatched key is rejected under strict policy",
			policy:       domain.Strict,
			known:        map[string]ssh.PublicKey{addr: other},
			connectAddr:  addr,
			presentedKey: good,
			wantErr:      true,
			wantMismatch: true,
		},
		{
			name:         "unknown host is accepted under accept-new",
			policy:       domain.AcceptNew,
			known:        map[string]ssh.PublicKey{"other.com:22": other},
			connectAddr:  addr,
			presentedKey: good,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeKnownHosts(t, tc.known)
			cb, err := HostKeyCallback(path, tc.policy, tc.connectAddr)
			if err != nil {
				t.Fatalf("HostKeyCallback: %v", err)
			}

			err = cb(tc.connectAddr, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}, tc.presentedKey)

			if tc.wantErr && err == nil {
				t.Fatal("expected rejection, got acceptance")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected acceptance, got: %v", err)
			}
			if tc.wantMismatch && !IsKeyMismatchError(err) {
				t.Errorf("expected a mismatch error, got: %v", err)
			}
			if tc.wantUnknownHost && !IsUnknownHostError(err) {
				t.Errorf("expected an unknown-host error, got: %v", err)
			}
		})
	}
}

// TestAcceptNewAppendsUnknownHost covers the TOFU write path: a second connection to the
// same host must verify against the recorded key without appending again.
func TestAcceptNewAppendsUnknownHost(t *testing.T) {
	const addr = "first-time.example.com:22"
	key := testKey(t)
	path := filepath.Join(t.TempDir(), "known_hosts")

	cb, err := HostKeyCallback(path, domain.AcceptNew, addr)
	if err != nil {
		t.Fatalf("HostKeyCallback: %v", err)
	}
	remote := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}

	if err := cb(addr, remote, key); err != nil {
		t.Fatalf("first connection should be accepted under accept-new: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if !strings.Contains(string(content), "first-time.example.com") {
		t.Fatalf("host should have been recorded, file contains: %q", string(content))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("known_hosts permissions = %04o, want 0600", perm)
	}

	// Rebuild the callback from the file: the recorded key must now verify.
	cb2, err := HostKeyCallback(path, domain.AcceptNew, addr)
	if err != nil {
		t.Fatalf("HostKeyCallback (second): %v", err)
	}
	if err := cb2(addr, remote, key); err != nil {
		t.Fatalf("second connection should verify against the recorded key: %v", err)
	}

	// A different key must not be appended or accepted.
	cb3, err := HostKeyCallback(path, domain.AcceptNew, addr)
	if err != nil {
		t.Fatalf("HostKeyCallback (third): %v", err)
	}
	if err := cb3(addr, remote, testKey(t)); err == nil {
		t.Fatal("a changed key must not be accepted under accept-new")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(content) {
		t.Error("a mismatched key must never be appended to known_hosts")
	}
}

func TestStrictPolicyRejectsMissingKnownHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := HostKeyCallback(path, domain.Strict, "example.com:22")
	if err == nil {
		t.Fatal("expected an error when known_hosts is missing under strict policy")
	}
	if !strings.Contains(err.Error(), "--accept-new") {
		t.Errorf("error should point at --accept-new, got: %v", err)
	}
}

// TestAcceptNewCreatesMissingKnownHosts covers the recovery path verified against
// x/crypto: knownhosts.New fails on a missing file, so the file is created and re-read.
func TestAcceptNewCreatesMissingKnownHosts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "known_hosts")
	key := testKey(t)

	cb, err := HostKeyCallback(path, domain.AcceptNew, "new.example.com:22")
	if err != nil {
		t.Fatalf("accept-new should recover from a missing known_hosts: %v", err)
	}
	if err := cb("new.example.com:22", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}, key); err != nil {
		t.Fatalf("first connection should be accepted: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("known_hosts should now exist: %v", err)
	}
}

func TestAcceptNewRequiresAddress(t *testing.T) {
	path := writeKnownHosts(t, map[string]ssh.PublicKey{})
	if _, err := HostKeyCallback(path, domain.AcceptNew, "example.com"); err == nil {
		t.Fatal("expected an error when addr has no port under accept-new")
	}
}

func TestNormalizeAddr(t *testing.T) {
	tests := []struct {
		host string
		port int
		want string
	}{
		{"example.com", 22, "example.com:22"},
		{"example.com", 0, "example.com:22"},
		{"example.com", 2222, "example.com:2222"},
		{" example.com ", 22, "example.com:22"},
		{"2001:db8::1", 22, "[2001:db8::1]:22"},
	}
	for _, tc := range tests {
		if got := NormalizeAddr(tc.host, tc.port); got != tc.want {
			t.Errorf("NormalizeAddr(%q, %d) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}
