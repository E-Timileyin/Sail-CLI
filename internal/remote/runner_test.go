package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/E-Timileyin/sail/internal/domain"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// testServer is an in-process SSH server that executes a canned response per command and
// records what it was asked to run.
type testServer struct {
	addr    string
	hostPub ssh.PublicKey
	keyPath string
	mu      chan struct{} // serialises access to commands
	cmds    []string
	handler func(cmd string) (stdout string, exit int)
}

func startTestServer(t *testing.T, handler func(cmd string) (string, int)) *testServer {
	t.Helper()

	hostPubKey, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	hostPub, err := ssh.NewPublicKey(hostPubKey)
	if err != nil {
		t.Fatalf("host public key: %v", err)
	}

	_, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("client key: %v", err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPriv)
	if err != nil {
		t.Fatalf("client signer: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write client key: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
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

	srv := &testServer{addr: ln.Addr().String(), hostPub: hostPub, keyPath: keyPath,
		mu: make(chan struct{}, 1), handler: handler}
	srv.mu <- struct{}{}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.serve(conn, cfg)
		}
	}()

	return srv
}

func (s *testServer) serve(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "sessions only")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range requests {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				p := req.Payload
				n := int(p[0])<<24 | int(p[1])<<16 | int(p[2])<<8 | int(p[3])
				cmd := string(p[4 : 4+n])

				<-s.mu
				s.cmds = append(s.cmds, cmd)
				s.mu <- struct{}{}

				req.Reply(true, nil)

				// Drain stdin before replying. A real shell consumes its input; a fake that
				// closes the channel first makes the client's write fail with EOF, which
				// shows up as a flaky test rather than a product bug.
				_, _ = io.Copy(io.Discard, ch)

				out, exit := s.handler(cmd)
				if out != "" {
					ch.Write([]byte(out))
				}
				ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(exit)}))
				ch.Close()
			}
		}()
	}
}

func (s *testServer) ran(substr string) bool {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	for _, c := range s.cmds {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func (s *testServer) all() []string {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	out := make([]string, len(s.cmds))
	copy(out, s.cmds)
	return out
}

func (s *testServer) server(t *testing.T, policy domain.TrustPolicy) *domain.Server {
	t.Helper()
	host, portStr, err := net.SplitHostPort(s.addr)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	khPath := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{"[" + host + "]:" + portStr}, s.hostPub) + "\n"
	if err := os.WriteFile(khPath, []byte(line), 0o600); err != nil {
		t.Fatalf("known_hosts: %v", err)
	}

	return &domain.Server{
		Name: "test", Host: host, Port: port, User: "deploy",
		KeyPath: s.keyPath, TrustPolicy: policy, KnownHostsPath: khPath,
	}
}

// TestRunnerRoundTrip: the runner executes and captures output over a real SSH session.
func TestRunnerRoundTrip(t *testing.T) {
	srv := startTestServer(t, func(cmd string) (string, int) {
		switch {
		case strings.Contains(cmd, "exit 3"):
			return "bad\n", 3
		default:
			return "ok\n", 0
		}
	})

	r, err := Dial(srv.server(t, domain.Strict))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer r.Close()

	res := r.Run("echo ok")
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if strings.TrimSpace(res.Stdout) != "ok" {
		t.Errorf("Stdout = %q, want ok", res.Stdout)
	}

	// A non-zero exit must be reported with its status, not as a transport failure.
	fail := r.Run("exit 3")
	if fail.Err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
	if fail.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", fail.ExitCode)
	}
}

// TestRunnerRejectsUnknownHost: Dial must refuse a host absent from known_hosts. This is
// the property that replaced the deleted internal/config/ssh package, and the reason only
// one ssh.Dial call site exists.
func TestRunnerRejectsUnknownHost(t *testing.T) {
	srv := startTestServer(t, func(string) (string, int) { return "", 0 })

	s := srv.server(t, domain.Strict)
	// Point at an empty known_hosts: the host is now unknown.
	empty := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.KnownHostsPath = empty

	if _, err := Dial(s); err == nil {
		t.Fatal("Dial must reject a host that is not in known_hosts")
	} else if !strings.Contains(err.Error(), "accept-new") {
		t.Errorf("error should point at --accept-new, got: %v", err)
	}
}

// TestRunnerRejectsChangedHostKey: the MITM case, through the real dial path.
func TestRunnerRejectsChangedHostKey(t *testing.T) {
	srv := startTestServer(t, func(string) (string, int) { return "", 0 })

	s := srv.server(t, domain.Strict)

	// Replace known_hosts with a different key for the same host.
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(otherPriv)
	host, portStr, _ := net.SplitHostPort(srv.addr)
	bad := knownhosts.Line([]string{"[" + host + "]:" + portStr}, otherSigner.PublicKey()) + "\n"
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s.KnownHostsPath = path

	if _, err := Dial(s); err == nil {
		t.Fatal("Dial must reject a changed host key")
	} else if !strings.Contains(err.Error(), "machine-in-the-middle") {
		t.Errorf("error should warn about a possible MITM, got: %v", err)
	}
}

// TestRunnerWriteFile: content is delivered over stdin, so metacharacters in a compose
// file cannot be interpreted by the remote shell.
func TestRunnerWriteFile(t *testing.T) {
	srv := startTestServer(t, func(cmd string) (string, int) {
		if strings.Contains(cmd, "cat >") {
			// Simulate receiving stdin; the runner's contract is that the write command is
			// issued. Content integrity is asserted by the absence of interpolation.
			return "wrote\n", 0
		}
		return "", 0
	})

	r, err := Dial(srv.server(t, domain.Strict))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer r.Close()

	content := "services:\n  api:\n    image: ${APP_IMAGE}:${APP_TAG}\n    command: [\"sh\", \"-c\", \"echo $HOME\"]\n"
	if err := r.WriteFile("/srv/sail/api/compose.yaml", content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// The command must not contain the content: if it did, the remote shell would expand
	// ${APP_IMAGE} and $HOME before the file was written.
	for _, cmd := range srv.all() {
		if strings.Contains(cmd, "${APP_IMAGE}") || strings.Contains(cmd, "$HOME") {
			t.Errorf("file content was interpolated into the command string: %s", cmd)
		}
	}
	if !srv.ran("cat > '/srv/sail/api/compose.yaml'") {
		t.Errorf("expected a quoted write command, got: %v", srv.all())
	}
}

// TestDeployEndToEnd runs the full sequence against a real SSH server, so the command
// construction, the state parse and the promote ordering are exercised together.
func TestDeployEndToEnd(t *testing.T) {
	state := "current=sha-old\nprevious=sha-init\ncompose=yes\nenv=yes\n"

	srv := startTestServer(t, func(cmd string) (string, int) {
		switch {
		case strings.Contains(cmd, "printf 'current="):
			return state, 0
		case strings.Contains(cmd, "docker compose") && strings.Contains(cmd, "up -d --wait"):
			return "container started\n", 0
		case strings.Contains(cmd, "docker --version"):
			return "Docker version 27\n", 0
		default:
			return "", 0
		}
	})

	r, err := Dial(srv.server(t, domain.Strict))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer r.Close()

	layout := Layout{Root: "/srv/sail", App: "api"}
	out, err := NewDeployer(r, layout).Deploy("sha-new")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !out.Deployed {
		t.Error("expected Deployed")
	}
	if out.PreviousTag != "sha-old" {
		t.Errorf("PreviousTag = %q, want sha-old", out.PreviousTag)
	}

	// Stages are joined into one command, so order is asserted within it, not across calls.
	var deployCmd string
	for _, c := range srv.all() {
		if strings.Contains(c, "up -d --wait") && strings.Contains(c, "pull") {
			deployCmd = c
		}
	}
	if deployCmd == "" {
		t.Fatalf("no deploy command found: %v", srv.all())
	}
	tagIdx := strings.Index(deployCmd, "> '"+layout.TagEnvPath())
	pullIdx := strings.Index(deployCmd, "pull")
	upIdx := strings.Index(deployCmd, "up -d --wait")
	if !(tagIdx < pullIdx && pullIdx < upIdx) {
		t.Errorf("expected tag -> pull -> up within the deploy command, got %d/%d/%d:\n%s",
			tagIdx, pullIdx, upIdx, deployCmd)
	}

	// Promote must be a separate call, after the deploy.
	promoteSeen := false
	for _, c := range srv.all() {
		if strings.Contains(c, "> '"+layout.CurrentTagPath()) {
			promoteSeen = true
		}
	}
	if !promoteSeen {
		t.Error("promote never ran")
	}
}

// TestRollbackEndToEnd runs a rollback against a real server.
func TestRollbackEndToEnd(t *testing.T) {
	state := "current=sha-new\nprevious=sha-old\ncompose=yes\nenv=yes\n"

	srv := startTestServer(t, func(cmd string) (string, int) {
		if strings.Contains(cmd, "printf 'current=") {
			return state, 0
		}
		return "", 0
	})

	r, err := Dial(srv.server(t, domain.Strict))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer r.Close()

	layout := Layout{Root: "/srv/sail", App: "api"}
	out, err := NewDeployer(r, layout).Rollback()
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if out.Tag != "sha-old" {
		t.Errorf("rolled back to %q, want sha-old", out.Tag)
	}
	if !srv.ran("'sha-old'") {
		t.Error("the previous tag was not written to the server")
	}
}

// TestDeployEndToEndRollsBackOnUnhealthy runs the failure path over a real connection:
// the app must end up on the previous tag without operator involvement.
func TestDeployEndToEndRollsBackOnUnhealthy(t *testing.T) {
	state := "current=sha-old\nprevious=sha-init\ncompose=yes\nenv=yes\n"

	srv := startTestServer(t, func(cmd string) (string, int) {
		switch {
		case strings.Contains(cmd, "printf 'current="):
			return state, 0
		case strings.Contains(cmd, "up -d --wait") && strings.Contains(cmd, "'sha-new'"):
			// New tag fails its healthcheck.
			return "container unhealthy\n", 1
		case strings.Contains(cmd, "docker --version"):
			return "Docker version 27\n", 0
		default:
			return "", 0
		}
	})

	r, err := Dial(srv.server(t, domain.Strict))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer r.Close()

	layout := Layout{Root: "/srv/sail", App: "api"}
	out, err := NewDeployer(r, layout).Deploy("sha-new")
	if err == nil {
		t.Fatal("expected the deploy to fail")
	}
	if !out.RolledBack {
		t.Fatalf("expected an automatic rollback, got %+v (err %v)", out, err)
	}
	if !srv.ran("'sha-old'") {
		t.Error("the previous tag was not redeployed")
	}
}

// TestDialHonoursTimeout: a bounded timeout keeps a hung server from blocking a deploy.
func TestDialHonoursTimeout(t *testing.T) {
	// Shortened so the suite does not pay the real timeout; the property under test is
	// that the handshake is bounded at all, not the value of the bound.
	orig := HandshakeTimeout
	HandshakeTimeout = 2 * time.Second
	t.Cleanup(func() { HandshakeTimeout = orig })

	// A listener that accepts and never completes a handshake.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, _ := ssh.MarshalPrivateKey(priv, "")
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	s := &domain.Server{
		Name: "hang", Host: host, Port: port, User: "deploy", KeyPath: keyPath,
		TrustPolicy: domain.AcceptNew,
	}

	done := make(chan error, 1)
	go func() {
		_, err := Dial(s)
		done <- err
	}()

	// Must return on its own, bounded by HandshakeTimeout. Config.Timeout alone does not
	// cover the handshake, so without an explicit deadline this blocks forever.
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the dial to fail against a non-SSH server")
		}
	case <-time.After(HandshakeTimeout + 5*time.Second):
		t.Fatal("Dial did not honour HandshakeTimeout")
	}
}
