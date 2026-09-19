// Package domain holds Sail's types. Standard library imports only: connection and trust
// policy live in adapters, so a domain type cannot open a socket.
package domain

import (
	"fmt"
	"strconv"
)

// Server is one deployment target.
//
// Both tag sets are required: Viper's Unmarshal reads `mapstructure`, not `yaml`. With
// yaml-only tags multi-word keys silently unmarshal as empty — v0.1.0 shipped that, so
// key_path never loaded and only password auth could succeed.
type Server struct {
	Name    string            `yaml:"name" mapstructure:"name"`
	Host    string            `yaml:"host" mapstructure:"host"`
	Port    int               `yaml:"port" mapstructure:"port"`
	User    string            `yaml:"user" mapstructure:"user"`
	KeyPath string            `yaml:"key_path" mapstructure:"key_path"`
	Env     map[string]string `yaml:"env,omitempty" mapstructure:"env"`

	// Present only so the loader can detect and reject it; never used to authenticate.
	Password string `yaml:"password,omitempty" mapstructure:"password"`

	// Zero value is Strict.
	TrustPolicy TrustPolicy `yaml:"-" mapstructure:"-"`
	// KnownHostsPath overrides ~/.ssh/known_hosts.
	KnownHostsPath string `yaml:"-" mapstructure:"-"`
}

// Address returns host:port, defaulting to port 22.
func (s *Server) Address() string {
	port := s.Port
	if port == 0 {
		port = 22
	}
	return fmt.Sprintf("%s:%d", s.Host, port)
}

// PortString returns the port as a string, for callers that need it.
func (s *Server) PortString() string {
	port := s.Port
	if port == 0 {
		port = 22
	}
	return strconv.Itoa(port)
}

// Validate reports configuration that would otherwise fail later with a worse error.
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

// Deployment is what Sail deploys: an image reference and where it runs.
//
// Per ADR 0001 the compose file on the server carries healthchecks, limits and networks.
type Deployment struct {
	App        string `yaml:"app" mapstructure:"app"`
	Image      string `yaml:"image" mapstructure:"image"`
	Tag        string `yaml:"tag" mapstructure:"tag"`
	Server     string `yaml:"server" mapstructure:"server"`
	Domain     string `yaml:"domain,omitempty" mapstructure:"domain"`
	Port       int    `yaml:"port,omitempty" mapstructure:"port"`
	ComposeDir string `yaml:"compose_dir,omitempty" mapstructure:"compose_dir"`
}

// Validate rejects a deployment that cannot be actioned.
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

// ImageRef returns image:tag.
func (d *Deployment) ImageRef() string {
	return fmt.Sprintf("%s:%s", d.Image, d.Tag)
}

// TrustPolicy selects how an unknown host key is treated.
//
// Here rather than in sshx: sshx imports domain, so the adapter owning it would cycle.
type TrustPolicy int

const (
	// Strict rejects unknown hosts, matching OpenSSH's StrictHostKeyChecking=yes.
	Strict TrustPolicy = iota
	// AcceptNew records an unknown host's key on first use (OpenSSH accept-new). Opt-in.
	AcceptNew
)
