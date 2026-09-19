package domain

import (
	"fmt"
	"strconv"
)

// Server is one deployment target.
// Both tag sets are required: Viper reads `mapstructure`, not `yaml`. With yaml-only tags
// multi-word keys unmarshal empty — v0.1.0 shipped that, so key_path never loaded.
type Server struct {
	Name    string            `yaml:"name" mapstructure:"name"`
	Host    string            `yaml:"host" mapstructure:"host"`
	Port    int               `yaml:"port" mapstructure:"port"`
	User    string            `yaml:"user" mapstructure:"user"`
	KeyPath string            `yaml:"key_path" mapstructure:"key_path"`
	Env     map[string]string `yaml:"env,omitempty" mapstructure:"env"`

	// Present only so the loader can reject it; never used to authenticate.
	Password string `yaml:"password,omitempty" mapstructure:"password"`

	// Zero value is Strict.
	TrustPolicy TrustPolicy `yaml:"-" mapstructure:"-"`
	// KnownHostsPath overrides ~/.ssh/known_hosts.
	KnownHostsPath string `yaml:"-" mapstructure:"-"`
}

func (s *Server) Address() string {
	port := s.Port
	if port == 0 {
		port = 22
	}
	return fmt.Sprintf("%s:%d", s.Host, port)
}

func (s *Server) PortString() string {
	port := s.Port
	if port == 0 {
		port = 22
	}
	return strconv.Itoa(port)
}

func (s *Server) Validate() error {
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

// Deployment is an image reference and where it runs. Per ADR 0001 the compose file on the
// server carries healthchecks, limits and networks.
type Deployment struct {
	App        string `yaml:"app" mapstructure:"app"`
	Image      string `yaml:"image" mapstructure:"image"`
	Tag        string `yaml:"tag" mapstructure:"tag"`
	Server     string `yaml:"server" mapstructure:"server"`
	Domain     string `yaml:"domain,omitempty" mapstructure:"domain"`
	Port       int    `yaml:"port,omitempty" mapstructure:"port"`
	ComposeDir string `yaml:"compose_dir,omitempty" mapstructure:"compose_dir"`
}

func (d *Deployment) Validate() error {
	if d.App == "" {
		return fmt.Errorf("app is required")
	}
	if d.Image == "" {
		return fmt.Errorf("image is required")
	}
	// No implicit tag: an unidentifiable previous artifact makes rollback meaningless.
	if d.Tag == "" {
		return fmt.Errorf("tag is required; refusing to deploy an implicit tag — pass the git SHA")
	}
	if d.Tag == "latest" {
		return fmt.Errorf("tag \"latest\" is not allowed; pass the git SHA so rollback has a target")
	}
	return nil
}

func (d *Deployment) ImageRef() string {
	return fmt.Sprintf("%s:%s", d.Image, d.Tag)
}

// TrustPolicy lives here rather than in sshx: sshx imports domain, so the adapter owning
// the type that configures it would cycle.
type TrustPolicy int

const (
	// OpenSSH StrictHostKeyChecking=yes.
	Strict TrustPolicy = iota
	// OpenSSH StrictHostKeyChecking=accept-new. Opt-in.
	AcceptNew
)
