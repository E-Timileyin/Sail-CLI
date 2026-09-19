package remote

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/E-Timileyin/sail/internal/domain"
	"github.com/E-Timileyin/sail/internal/sshx"
	"golang.org/x/crypto/ssh"
)

// Result is the outcome of one remote command.
type Result struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

// Runner executes commands on one server over a single SSH connection.
type Runner struct {
	server *domain.Server
	client *ssh.Client
}

// HandshakeTimeout bounds the SSH handshake after the TCP connection is established.
//
// ssh.ClientConfig.Timeout only covers the TCP dial: a server that accepts the connection
// and then stalls leaves Dial blocked forever (verified). A stalled target is what an
// overloaded deploy server looks like, so the handshake needs its own bound.
var HandshakeTimeout = 30 * time.Second

// Dial opens a connection to the server.
//
// The only ssh.Dial-equivalent in the codebase, so host-key verification cannot be skipped
// by a caller building its own client config.
func Dial(s *domain.Server) (*Runner, error) {
	cfg, err := sshx.ClientConfig(s)
	if err != nil {
		return nil, err
	}
	cfg.Timeout = HandshakeTimeout

	netConn, err := net.DialTimeout("tcp", s.Address(), HandshakeTimeout)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to server %q (%s): %w", s.Name, s.Address(), err)
	}

	// A deadline covers the handshake; it is cleared afterwards so long-running commands
	// (docker compose pull on a cold cache) are not cut off.
	if err := netConn.SetDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
		netConn.Close()
		return nil, fmt.Errorf("cannot set handshake deadline: %w", err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, s.Address(), cfg)
	if err != nil {
		netConn.Close()
		switch {
		case sshx.IsKeyMismatchError(err):
			return nil, fmt.Errorf(
				"host key for server %q does not match known_hosts — possible machine-in-the-middle; "+
					"verify the key out of band, then run: ssh-keygen -R %s", s.Name, s.Host)
		case sshx.IsUnknownHostError(err):
			return nil, fmt.Errorf(
				"host %s is not in known_hosts; pass --accept-new to trust it on first connect", s.Host)
		}
		return nil, fmt.Errorf("SSH handshake with server %q (%s) failed: %w", s.Name, s.Address(), err)
	}

	if err := netConn.SetDeadline(time.Time{}); err != nil {
		sshConn.Close()
		return nil, fmt.Errorf("cannot clear handshake deadline: %w", err)
	}

	return &Runner{server: s, client: ssh.NewClient(sshConn, chans, reqs)}, nil
}

// Close releases the connection.
func (r *Runner) Close() error {
	if r.client == nil {
		return nil
	}
	return r.client.Close()
}

// Server returns the target this runner is connected to.
func (r *Runner) Server() *domain.Server { return r.server }

// Run executes a command.
//
// A non-zero exit is reported via Result, not as a transport error: "the command failed"
// and "the connection broke" need different handling.
func (r *Runner) Run(command string) Result {
	res := Result{Command: command}

	session, err := r.client.NewSession()
	if err != nil {
		res.Err = fmt.Errorf("cannot create session: %w", err)
		res.ExitCode = -1
		return res
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(command)
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()

	if err != nil {
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitStatus()
			res.Err = fmt.Errorf("command exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
			return res
		}
		res.ExitCode = -1
		res.Err = fmt.Errorf("command failed: %w", err)
	}
	return res
}

// MustRun runs a command and converts a non-zero exit into an error.
func (r *Runner) MustRun(command string) error {
	res := r.Run(command)
	return res.Err
}

// WriteFile pipes content to a remote path over stdin, creating it 600.
//
// Over stdin, not embedded in the command: compose files contain shell metacharacters.
func (r *Runner) WriteFile(path, content string) error {
	session, err := r.client.NewSession()
	if err != nil {
		return fmt.Errorf("cannot create session: %w", err)
	}
	defer session.Close()

	session.Stdin = strings.NewReader(content)
	var stderr bytes.Buffer
	session.Stderr = &stderr

	if err := session.Run(WriteFileScript(path)); err != nil {
		return fmt.Errorf("cannot write %s: %w: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// DefaultDeadline bounds a single remote command.
const DefaultDeadline = 10 * time.Minute

// SetImageRef records the image repository for an app on the server.
func SetImageRef(r *Runner, l Layout, image string) error {
	if image == "" {
		return fmt.Errorf("image is required")
	}
	return r.MustRun(SetImageRefScript(l, image))
}

// ImageRef reads the recorded image repository, returning "" when none is recorded.
func (r *Runner) ImageRef(l Layout) (string, error) {
	res := r.Run(ReadImageRefScript(l))
	if res.Err != nil {
		return "", res.Err
	}
	return strings.TrimSpace(res.Stdout), nil
}

// Quote single-quotes a string for safe interpolation into a POSIX shell command.
//
// Values reaching here come from config; unquoted, a crafted app name is a command
// injection against the deploy server, which holds the secrets.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// SafePath joins base and name, rejecting names that could escape base.
func SafePath(base, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return "", fmt.Errorf("invalid name %q: must not contain a path separator", name)
	}
	if strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("invalid name %q: must not start with a dash", name)
	}
	return strings.TrimRight(base, "/") + "/" + name, nil
}
