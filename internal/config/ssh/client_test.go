package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/E-Timileyin/sail/internal/domain"
	"github.com/E-Timileyin/sail/internal/sshx"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// startTestServer runs an in-process SSH server that executes a canned response for each
// command, and returns its address, its public key and the path to a client private key
// accepted by it.
//
// The previous version of this file skipped unless a live server happened to be on
// localhost:2222, so it never ran in CI and asserted nothing. A test that cannot fail is
// worse than no test, because it reports coverage it does not have.
func startTestServer(t *testing.T, commands map[string]string) (addr string, hostPub ssh.PublicKey, clientKeyPath string) {
	t.Helper()

	// Host key.
	hostPubKey, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	hostPub, err = ssh.NewPublicKey(hostPubKey)
	if err != nil {
		t.Fatalf("host public key: %v", err)
	}

	// Client key, written to a temp file for the client to load.
	_, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPriv)
	if err != nil {
		t.Fatalf("client signer: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}
	clientKeyPath = filepath.Join(t.TempDir(), "id_ed25519")
	// MarshalPrivateKey returns a PEM block; the armor must be encoded explicitly or
	// ssh.ParsePrivateKey reports "no key found".
	if err := os.WriteFile(clientKeyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write client key: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(clientSigner.PublicKey().Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, os.ErrPermission
		},
	}
	cfg.AddHostKey(hostSigner)

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
			go serveConn(conn, cfg, commands)
		}
	}()

	return ln.Addr().String(), hostPub, clientKeyPath
}

func serveConn(conn net.Conn, cfg *ssh.ServerConfig, commands map[string]string) {
	defer conn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range requests {
				switch req.Type {
				case "exec":
					// Payload is a uint32 length followed by the command string.
					payload := req.Payload
					cmdLen := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
					cmd := string(payload[4 : 4+cmdLen])

					if out, ok := commands[cmd]; ok {
						req.Reply(true, nil)
						ch.Write([]byte(out))
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					} else {
						req.Reply(true, nil)
						ch.Stderr().Write([]byte("command not found\n"))
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{127}))
					}
					ch.Close()
				case "exit-status":
					// ignore
				default:
					req.Reply(false, nil)
				}
			}
		}()
	}
}

// serverWithKnownHosts builds a ServerStruct wired to a started test server.
func serverWithKnownHosts(t *testing.T, srvAddr string, hostPub ssh.PublicKey, keyPath string) domain.Server {
	t.Helper()

	host, portStr, err := net.SplitHostPort(srvAddr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	khPath := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{"[" + host + "]:" + portStr}, hostPub) + "\n"
	if err := os.WriteFile(khPath, []byte(line), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	return domain.Server{
		Name:           "test",
		Host:           host,
		Port:           port,
		User:           "deploy",
		KeyPath:        keyPath,
		TrustPolicy:    domain.Strict,
		KnownHostsPath: khPath,
	}
}

func TestExecuteSSHCommand(t *testing.T) {
	addr, hostPub, keyPath := startTestServer(t, map[string]string{
		"echo hello": "hello\n",
		"uname -a":   "Linux test 6.0.0\n",
	})

	cfg := serverWithKnownHosts(t, addr, hostPub, keyPath)

	t.Run("successful command execution", func(t *testing.T) {
		result, err := ExecuteSSHCommand(cfg, "echo hello")
		if err != nil {
			t.Fatalf("ExecuteSSHCommand: %v", err)
		}
		if result.Status != "success" {
			t.Errorf("Status = %q, want success", result.Status)
		}
		if result.Message != "hello\n" {
			t.Errorf("Message = %q, want %q", result.Message, "hello\n")
		}
		if result.Command != "echo hello" {
			t.Errorf("Command = %q", result.Command)
		}
	})

	t.Run("failing command reports failure", func(t *testing.T) {
		result, err := ExecuteSSHCommand(cfg, "definitely-not-a-command")
		if err == nil {
			t.Fatal("expected an error for a failing remote command")
		}
		if result.Status != "fail" {
			t.Errorf("Status = %q, want fail", result.Status)
		}
	})

	t.Run("missing key file is reported", func(t *testing.T) {
		bad := cfg
		bad.KeyPath = filepath.Join(t.TempDir(), "absent")
		if _, err := ExecuteSSHCommand(bad, "echo hello"); err == nil {
			t.Fatal("expected an error for a missing private key")
		}
	})
}

// TestExecuteSSHCommandRejectsUnknownHost is the security assertion for this helper: it
// previously used ssh.InsecureIgnoreHostKey(), so it happily ran commands against any
// host. Now an unrecorded host must fail.
func TestExecuteSSHCommandRejectsUnknownHost(t *testing.T) {
	addr, _, keyPath := startTestServer(t, map[string]string{"echo hello": "hello\n"})

	_, portStr, _ := net.SplitHostPort(addr)
	host, _, _ := net.SplitHostPort(addr)
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	cfg := domain.Server{
		Name:           "test",
		Host:           host,
		Port:           port,
		User:           "deploy",
		KeyPath:        keyPath,
		TrustPolicy:    domain.Strict,
		KnownHostsPath: filepath.Join(t.TempDir(), "empty_known_hosts"),
	}
	if err := os.WriteFile(cfg.KnownHostsPath, nil, 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}

	if _, err := ExecuteSSHCommand(cfg, "echo hello"); err == nil {
		t.Fatal("a host absent from known_hosts must be rejected")
	}
}

func TestExecuteSSHCommandHonoursTimeout(t *testing.T) {
	// Not a timing test: asserts the config carries a bounded timeout, which is what
	// stops a hung server from blocking a deploy indefinitely.
	handler, err := sshx.ClientConfig(&domain.Server{
		Name:        "test",
		Host:        "127.0.0.1",
		Port:        1,
		User:        "deploy",
		KeyPath:     writeThrowawayKey(t),
		TrustPolicy: domain.Strict,
	})
	if err != nil {
		t.Fatalf("ClientConfig: %v", err)
	}
	if handler.Timeout <= 0 {
		t.Error("SSH client config must set a bounded timeout")
	}
	if handler.HostKeyCallback == nil {
		t.Error("SSH client config must set HostKeyCallback")
	}
}

func writeThrowawayKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}
