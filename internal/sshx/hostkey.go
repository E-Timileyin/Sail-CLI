// Package sshx owns SSH trust policy, the only place an ssh.ClientConfig is built:
// verification duplicated across call sites is how InsecureIgnoreHostKey survived.
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

func DefaultKnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home directory: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

var ErrUnknownHost = errors.New("host key is not in known_hosts")

// HostKeyCallback rejects a key that does not match a recorded entry, even under AcceptNew:
// trust-on-first-use may add a key, never replace one.
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

// loadKnownHosts: knownhosts.New fails on a missing file (nil callback, os.ErrNotExist).
// Empty known_hosts is valid and rejects everything, so AcceptNew creates it and retries.
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

// tofuCallback records an unknown host then accepts it. A mismatched key is never recorded.
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

func IsUnknownHostError(err error) bool {
	var keyErr *knownhosts.KeyError
	return errors.As(err, &keyErr) && len(keyErr.Want) == 0
}

func IsKeyMismatchError(err error) bool {
	var keyErr *knownhosts.KeyError
	return errors.As(err, &keyErr) && len(keyErr.Want) > 0
}

func NormalizeAddr(host string, port int) string {
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
}
