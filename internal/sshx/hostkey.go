// Package sshx owns Sail's SSH trust policy and is the only place an ssh.ClientConfig is
// built: verification duplicated across call sites is how InsecureIgnoreHostKey survived.
package sshx

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/E-Timileyin/sail/internal/domain"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// DefaultKnownHostsPath returns ~/.ssh/known_hosts.
func DefaultKnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home directory: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

// ErrUnknownHost is returned when a host's key is absent from known_hosts and the
// policy is Strict.
var ErrUnknownHost = errors.New("host key is not in known_hosts")

// HostKeyCallback verifies host keys against knownHostsPath.
//
// A key that does not match a recorded entry is rejected even under AcceptNew: tofu may
// add a key, never replace one.
func HostKeyCallback(knownHostsPath string, policy domain.TrustPolicy, addr string) (ssh.HostKeyCallback, error) {
	if knownHostsPath == "" {
		p, err := DefaultKnownHostsPath()
		if err != nil {
			return nil, err
		}
		knownHostsPath = p
	}

	base, err := loadKnownHosts(knownHostsPath, policy)
	if err != nil {
		return nil, err
	}

	if policy == domain.Strict {
		return base, nil
	}
	return tofuCallback(base, knownHostsPath, addr)
}

// loadKnownHosts builds the base verifier.
//
// knownhosts.New fails on a missing file (returns a nil callback, os.ErrNotExist). Empty
// known_hosts is valid and rejects everything, so AcceptNew creates the file and retries.
func loadKnownHosts(path string, policy domain.TrustPolicy) (ssh.HostKeyCallback, error) {
	cb, err := knownhosts.New(path)
	if err == nil {
		return cb, nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf(
			"cannot read known_hosts at %s (expected 0600 on the file and 0700 on ~/.ssh): %w",
			path, err)
	}

	if policy == domain.Strict {
		return nil, fmt.Errorf(
			"known_hosts not found at %s; create it, or pass --accept-new to trust the host on first connect: %w",
			path, ErrUnknownHost)
	}

	if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		return nil, fmt.Errorf("cannot create %s: %w", filepath.Dir(path), mkErr)
	}
	f, mkErr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if mkErr != nil {
		return nil, fmt.Errorf("cannot create %s: %w", path, mkErr)
	}
	f.Close()

	cb, err = knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read known_hosts at %s after creating it: %w", path, err)
	}
	return cb, nil
}

// tofuCallback records an unknown host, then accepts it. A mismatched key is never recorded.
func tofuCallback(base ssh.HostKeyCallback, knownHostsPath, addr string) (ssh.HostKeyCallback, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return nil, fmt.Errorf("--accept-new needs a host:port server address, got %q: %w", addr, err)
	}

	// Captured, not derived from the callback's arguments: a callback may serve several dials.
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := base(hostname, remote, key)
		if err == nil {
			return nil
		}

		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}

		// Want non-empty means the host is known with a different key; recording it is a downgrade.
		if len(keyErr.Want) > 0 {
			// Wrap the original so IsKeyMismatchError finds it; dropping it makes the
			// caller's MITM branch dead code.
			return fmt.Errorf(
				"REMOTE HOST IDENTIFICATION HAS CHANGED for %s: presented key does not match known_hosts. "+
					"This may be a machine-in-the-middle attack. Verify the server key out of band, then run: ssh-keygen -R %s: %w",
				hostname, hostname, err)
		}

		if appendErr := appendKnownHost(knownHostsPath, addr, key); appendErr != nil {
			return fmt.Errorf("host %s is unknown and could not be recorded in %s: %w",
				hostname, knownHostsPath, appendErr)
		}
		return nil
	}, nil
}

// appendKnownHost appends one entry: bare host for port 22, else [host]:port.
func appendKnownHost(path, addr string, key ssh.PublicKey) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}

	hostField := host
	if port != "22" {
		hostField = "[" + host + "]:" + port
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(knownhosts.Line([]string{hostField}, key) + "\n")
	return err
}

// IsUnknownHostError reports an unknown-host rejection, so callers can suggest --accept-new.
func IsUnknownHostError(err error) bool {
	var keyErr *knownhosts.KeyError
	return errors.As(err, &keyErr) && len(keyErr.Want) == 0
}

// IsKeyMismatchError reports a changed-key rejection.
func IsKeyMismatchError(err error) bool {
	var keyErr *knownhosts.KeyError
	return errors.As(err, &keyErr) && len(keyErr.Want) > 0
}

// NormalizeAddr formats a host/port pair as host:port.
func NormalizeAddr(host string, port int) string {
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
}
