package model

import (
	"fmt"
	"os"
	"time"

	"github.com/E-Timileyin/sail/internal/sshx"
	"golang.org/x/crypto/ssh"
)

// ServerStruct is one deployment target.
//
// Both `yaml` and `mapstructure` tags are required. Viper's Unmarshal reads
// `mapstructure`, not `yaml`: with yaml-only tags, multi-word keys such as key_path
// silently unmarshal as empty (verified — single-word keys like name/host appear to work
// only because Viper fuzzy-matches them). v0.1.0 shipped yaml-only tags here, so
// key_path from config was never populated and only password auth could ever succeed.
type ServerStruct struct {
	Name    string            `yaml:"name" mapstructure:"name"`
	Host    string            `yaml:"host" mapstructure:"host"`
	Port    int               `yaml:"port" mapstructure:"port"`
	User    string            `yaml:"user" mapstructure:"user"`
	KeyPath string            `yaml:"key_path" mapstructure:"key_path"`
	Env     map[string]string `yaml:"env,omitempty" mapstructure:"env"`

	// Password is retained as a field the loader can detect and reject, so an old
	// config fails with an actionable message instead of a silent auth failure.
	// It is never used to authenticate. See config.LoadConfig.
	Password string `yaml:"password,omitempty" mapstructure:"password"`

	// TrustPolicy selects host-key handling. Zero value is Strict.
	TrustPolicy sshx.TrustPolicy `yaml:"-" mapstructure:"-"`
	// KnownHostsPath overrides ~/.ssh/known_hosts.
	KnownHostsPath string `yaml:"-" mapstructure:"-"`
}

// SSHConfig returns the SSH client configuration.
//
// Host keys are verified against known_hosts. There is deliberately no way to obtain a
// config that skips verification: the previous ssh.InsecureIgnoreHostKey() call here
// allowed a machine-in-the-middle to impersonate the target on every deploy.
func (s *ServerStruct) SSHConfig() (*ssh.ClientConfig, error) {
	authMethods, err := s.authMethods()
	if err != nil {
		return nil, err
	}

	cb, err := sshx.HostKeyCallback(s.KnownHostsPath, s.TrustPolicy, s.Address())
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

func (s *ServerStruct) authMethods() ([]ssh.AuthMethod, error) {
	var authMethods []ssh.AuthMethod

	if s.KeyPath != "" {
		key, err := os.ReadFile(s.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("unable to read private key %s: %w", s.KeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("unable to parse private key %s: %w", s.KeyPath, err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	// Password auth is intentionally not a fallback. Accepting it would let a config
	// file reinstate a credential this project is removing from disk.
	if len(authMethods) == 0 {
		if s.Password != "" {
			return nil, fmt.Errorf(
				"password authentication is no longer supported (server %q): remove the password field and set key_path",
				s.Name)
		}
		return nil, fmt.Errorf("no authentication method provided for server %q: set key_path", s.Name)
	}

	return authMethods, nil
}

// Address returns the server address in host:port format.
func (s *ServerStruct) Address() string {
	port := s.Port
	if port == 0 {
		port = 22
	}
	return fmt.Sprintf("%s:%d", s.Host, port)
}

// Validate reports configuration that would otherwise fail later with a worse error.
func (s *ServerStruct) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("server name is required")
	}
	if s.Host == "" {
		return fmt.Errorf("server %q: host is required", s.Name)
	}
	if s.User == "" {
		return fmt.Errorf("server %q: user is required", s.Name)
	}
	if s.KeyPath == "" {
		return fmt.Errorf(
			"server %q: key_path is required; password authentication is no longer supported",
			s.Name)
	}
	return nil
}
