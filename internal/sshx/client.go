package sshx

import (
	"fmt"
	"os"
	"time"

	"github.com/E-Timileyin/sail/internal/domain"
	"golang.org/x/crypto/ssh"
)

// ClientConfig builds an ssh.ClientConfig for a server.
//
// This lives here rather than on the domain type because it reads a private key from
// disk and performs host-key verification — I/O and crypto, which domain must not do.
// It is the only constructor of ssh.ClientConfig in the codebase, which is what keeps
// trust policy from being duplicated or quietly overridden at a call site.
func ClientConfig(s *domain.Server) (*ssh.ClientConfig, error) {
	authMethods, err := authMethods(s)
	if err != nil {
		return nil, err
	}

	cb, err := HostKeyCallback(s.KnownHostsPath, s.TrustPolicy, s.Address())
	if err != nil {
		return nil, err
	}

	return &ssh.ClientConfig{
		User:            s.User,
		Auth:            authMethods,
		HostKeyCallback: cb,
		Timeout:         30 * time.Second,
	}, nil
}

func authMethods(s *domain.Server) ([]ssh.AuthMethod, error) {
	if s.KeyPath == "" {
		if s.Password != "" {
			return nil, fmt.Errorf(
				"password authentication is no longer supported (server %q): remove the password field and set key_path",
				s.Name)
		}
		return nil, fmt.Errorf("no authentication method provided for server %q: set key_path", s.Name)
	}

	key, err := os.ReadFile(s.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read private key %s: %w", s.KeyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("unable to parse private key %s (is it passphrase-protected?): %w", s.KeyPath, err)
	}

	// Password is deliberately not offered as an additional method: accepting it would
	// let a config file reinstate a credential this project is removing from disk.
	return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
}
