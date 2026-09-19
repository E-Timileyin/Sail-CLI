// Package sshx owns Sail's SSH trust policy.
//
// Nothing outside this package should construct an ssh.ClientConfig. Host-key
// verification is a property of the connection, not of a caller, so it must not be
// duplicated at call sites — that is how the previous InsecureIgnoreHostKey() calls
// survived in two places at once.
package sshx

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// TrustPolicy decides how to treat a host key that is not already in known_hosts.
type TrustPolicy int

const (
	// Strict rejects unknown hosts. This is the default, matching OpenSSH's
	// StrictHostKeyChecking=yes.
	Strict TrustPolicy = iota
	// AcceptNew records an unknown host's key on first use and accepts it, matching
	// OpenSSH's StrictHostKeyChecking=accept-new. It is opt-in.
	AcceptNew
)

// ErrUnknownHost is returned when a host's key is absent from known_hosts and the
// policy is Strict.
var ErrUnknownHost = errors.New("host key is not in known_hosts")

// HostKeyCallback returns a callback verifying host keys against knownHostsPath.
//
// addr is the host:port the connection targets, used only under AcceptNew when a key
// must be recorded. A key that does not match a recorded entry is always rejected,
// including under AcceptNew: trust-on-first-use may add a key, never replace one.
// Silently accepting a changed key would defeat the purpose of pinning, so the
// mismatch path returns a distinct, loud error.
func HostKeyCallback(knownHostsPath string, policy TrustPolicy, addr string) (ssh.HostKeyCallback, error) {
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

	if policy == Strict {
		return base, nil
	}
	return tofuCallback(base, knownHostsPath, addr)
}

// loadKnownHosts builds the base verifier.
//
// knownhosts.New fails if the file does not exist. Verified against x/crypto: it
// returns a nil callback and an error satisfying errors.Is(err, os.ErrNotExist).
// Under AcceptNew that is recoverable — create an empty file and retry, since an empty
// known_hosts is valid and rejects everything until a key is appended. Under Strict it
// is a hard error with an actionable message.
func loadKnownHosts(path string, policy TrustPolicy) (ssh.HostKeyCallback, error) {
	cb, err := knownhosts.New(path)
	if err == nil {
		return cb, nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf(
			"cannot read known_hosts at %s (expected 0600 on the file and 0700 on ~/.ssh): %w",
			path, err)
	}

	if policy == Strict {
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

// tofuCallback wraps base so an unknown host is appended to known_hosts and then
// accepted. A mismatched key is never appended.
func tofuCallback(base ssh.HostKeyCallback, knownHostsPath, addr string) (ssh.HostKeyCallback, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return nil, fmt.Errorf("--accept-new needs a host:port server address, got %q: %w", addr, err)
	}

	// One callback may serve several dial attempts, so addr is captured here rather
	// than derived from the callback's arguments.
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := base(hostname, remote, key)
		if err == nil {
			return nil
		}

		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}

		// Want is non-empty when the host is known but presented a different key.
		// Recording it would be a trust downgrade.
		if len(keyErr.Want) > 0 {
			// Wrap the original error so IsKeyMismatchError still finds it via
			// errors.As. Dropping it would make the caller's mismatch branch dead
			// code — silently losing the MITM warning on the one path that needs it.
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

// appendKnownHost appends one entry in ssh-keyscan's format: a bare host for port 22,
// otherwise the bracketed [host]:port form. knownhosts.Line handles key serialisation.
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

// IsUnknownHostError reports an unknown-host rejection, so callers can suggest
// --accept-new without parsing error strings.
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
