package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/E-Timileyin/sail/internal/domain"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// newSigner returns a fresh ed25519 signer and its public key.
func newSigner(t *testing.T) (ssh.Signer, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("new public key: %v", err)
	}
	return signer, sshPub
}

// startSSHServer runs a real SSH server presenting the given host key, and returns its
// address. This exercises the actual handshake rather than calling a callback by hand —
// the first version of these tests passed while the deployment path could still not
// reach the verification code at all.
func startSSHServer(t *testing.T, hostKey ssh.Signer) string {
	t.Helper()

	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(hostKey)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				// Handshake only; no session is needed to test host-key verification.
				if _, chans, reqs, err := ssh.NewServerConn(conn, cfg); err == nil {
					go ssh.DiscardRequests(reqs)
					for ch := range chans {
						ch.Reject(ssh.UnknownChannelType, "no sessions")
					}
				}
			}()
		}
	}()

	return ln.Addr().String()
}

func knownHostsEntry(t *testing.T, addr string, key ssh.PublicKey) string {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	field := host
	if port != "22" {
		field = "[" + host + "]:" + port
	}
	return knownhosts.Line([]string{field}, key) + "\n"
}

// TestHandshakeRejectsChangedHostKey is the machine-in-the-middle test.
//
// A server presenting a key that differs from the recorded one must be rejected at
// handshake, under both policies, and the presented key must not be recorded. This is
// the behaviour the whole fix exists for, so it is tested against a real handshake
// rather than a synthetic callback invocation.
func TestHandshakeRejectsChangedHostKey(t *testing.T) {
	legitSigner, legitPub := newSigner(t)
	impostorSigner, _ := newSigner(t)

	addr := startSSHServer(t, impostorSigner)

	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	original := knownHostsEntry(t, addr, legitPub)
	if err := os.WriteFile(khPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	_ = legitSigner

	for _, policy := range []struct {
		name string
		p    domain.TrustPolicy
	}{{"strict", domain.Strict}, {"accept-new", domain.AcceptNew}} {
		t.Run(policy.name, func(t *testing.T) {
			cb, err := HostKeyCallback(khPath, policy.p, addr)
			if err != nil {
				t.Fatalf("HostKeyCallback: %v", err)
			}

			client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
				User:            "deploy",
				HostKeyCallback: cb,
				Auth:            []ssh.AuthMethod{ssh.Password("irrelevant")},
				Timeout:         5 * time.Second,
			})
			if err == nil {
				client.Close()
				t.Fatal("handshake with a changed host key must fail")
			}
			if !IsKeyMismatchError(err) {
				t.Errorf("expected a key-mismatch error, got: %v", err)
			}
			if policy.p == domain.AcceptNew && !strings.Contains(err.Error(), "machine-in-the-middle") {
				t.Errorf("accept-new should surface the MITM warning, got: %v", err)
			}
		})
	}

	after, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if string(after) != original {
		t.Error("a changed host key must never be written to known_hosts")
	}
}

// TestHandshakeAcceptsKnownHost covers the positive case end to end: a server presenting
// the recorded key connects.
func TestHandshakeAcceptsKnownHost(t *testing.T) {
	signer, pub := newSigner(t)
	addr := startSSHServer(t, signer)

	khPath := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(khPath, []byte(knownHostsEntry(t, addr, pub)), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}

	cb, err := HostKeyCallback(khPath, domain.Strict, addr)
	if err != nil {
		t.Fatalf("HostKeyCallback: %v", err)
	}
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "deploy",
		HostKeyCallback: cb,
		Auth:            []ssh.AuthMethod{ssh.Password("irrelevant")},
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("a known, matching host should connect: %v", err)
	}
	client.Close()
}

// TestHandshakeAcceptNewRecordsThenVerifies covers trust-on-first-use against a real
// server: the first connection records the key, the second verifies against it.
func TestHandshakeAcceptNewRecordsThenVerifies(t *testing.T) {
	signer, _ := newSigner(t)
	addr := startSSHServer(t, signer)
	khPath := filepath.Join(t.TempDir(), "known_hosts")

	for attempt := 1; attempt <= 2; attempt++ {
		cb, err := HostKeyCallback(khPath, domain.AcceptNew, addr)
		if err != nil {
			t.Fatalf("attempt %d: HostKeyCallback: %v", attempt, err)
		}
		client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
			User:            "deploy",
			HostKeyCallback: cb,
			Auth:            []ssh.AuthMethod{ssh.Password("irrelevant")},
			Timeout:         5 * time.Second,
		})
		if err != nil {
			t.Fatalf("attempt %d: connect: %v", attempt, err)
		}
		client.Close()
	}

	content, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if n := strings.Count(strings.TrimSpace(string(content)), "\n") + 1; n != 1 {
		t.Errorf("expected exactly 1 known_hosts entry, got %d: %q", n, string(content))
	}
}
