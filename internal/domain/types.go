// Package domain holds Sail's pure types and ports.
//
// It imports nothing outside the standard library. In particular it must not import
// golang.org/x/crypto/ssh or the Docker SDK: connection and trust policy live in
// adapters (sshx, docker), and a domain type that can open a socket is a domain type
// that cannot be tested without one.
package domain

import "fmt"

// Server is a deployment target.
type Server struct {
	Name     string            `yaml:"name"`
	Host     string            `yaml:"host"`
	Port     int               `yaml:"port"`
	User     string            `yaml:"user"`
	KeyPath  string            `yaml:"key_path"`
	Env      map[string]string `yaml:"env,omitempty"`
	Identity string            `yaml:"identity,omitempty"`
}

// Address returns host:port, defaulting to port 22.
func (s *Server) Address() string {
	port := s.Port
	if port == 0 {
		port = 22
	}
	return fmt.Sprintf("%s:%d", s.Host, port)
}

// Validate reports configuration that would fail later with a worse error.
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

// Deployment is what Sail deploys. Per ADR 0001 the compose file on the server carries
// healthchecks, limits and networks, so these are references, not a full container spec.
type Deployment struct {
	App        string `yaml:"app"`
	Image      string `yaml:"image"`
	Tag        string `yaml:"tag"`
	Server     string `yaml:"server"`
	Domain     string `yaml:"domain,omitempty"`
	Port       int    `yaml:"port,omitempty"`
	ComposeDir string `yaml:"compose_dir,omitempty"`
}

// Validate rejects a deployment that cannot be actioned.
func (d *Deployment) Validate() error {
	if d.App == "" {
		return fmt.Errorf("app is required")
	}
	if d.Image == "" {
		return fmt.Errorf("image is required")
	}
	// fixes.md: tag:latest must not be a default. An implicit tag makes rollback
	// impossible to reason about, because the previous artifact is unidentifiable.
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
