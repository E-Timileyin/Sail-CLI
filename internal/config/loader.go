package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/E-Timileyin/sail/internal/domain"
	"github.com/spf13/viper"
)

// CurrentVersion is the config schema Sail accepts.
//
// Clean break (no compatibility shims): a config that does not declare this exact
// version is rejected with an actionable message rather than silently reinterpreted.
// v0.1.0 shipped no binary artifacts and advertised a `go install` path that does not
// resolve, so there is no installed base whose config deserves lenient parsing — and a
// permissive loader is how a plaintext password field survives unnoticed.
const CurrentVersion = 2

// Config is the on-disk shape of config.yaml.
type Config struct {
	Version int             `yaml:"version" mapstructure:"version"`
	App     AppConfig       `yaml:"app" mapstructure:"app"`
	Servers []domain.Server `yaml:"servers" mapstructure:"servers"`
}

// AppConfig is non-secret application metadata.
type AppConfig struct {
	Name        string `yaml:"name" mapstructure:"name"`
	Environment string `yaml:"environment" mapstructure:"environment"`
}

// LoadConfig reads and validates the server configuration.
func LoadConfig(configFile string) ([]domain.Server, error) {
	cfg, err := Load(configFile)
	if err != nil {
		return nil, err
	}
	return cfg.Servers, nil
}

// Load reads, version-checks and validates a config file.
func Load(configFile string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath(".")
	}

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("cannot read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", err)
	}

	if err := validate(&cfg, v); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validate(cfg *Config, v *viper.Viper) error {
	if cfg.Version == 0 {
		return fmt.Errorf(
			"config has no `version` field; this release requires `version: %d`.\n"+
				"  This is a breaking change from v0.1.0: the plaintext `password:` field was removed\n"+
				"  (Sail verifies host keys and uses key auth only). Add `version: %d` at the top of\n"+
				"  the file and replace any `password:` with `key_path:`.",
			CurrentVersion, CurrentVersion)
	}
	if cfg.Version != CurrentVersion {
		return fmt.Errorf("config version %d is not supported (this build expects %d)", cfg.Version, CurrentVersion)
	}

	// Detect removed fields explicitly. Viper drops unknown keys silently, which would
	// otherwise leave a user's credential sitting in a file they believe Sail reads.
	if v.InConfig("deployment") {
		return fmt.Errorf(
			"the `deployment:` block was removed in this release; per ADR 0001 the compose file on\n" +
				"  the server carries image, ports, healthcheck and limits")
	}
	for i, s := range cfg.Servers {
		if v.InConfig(fmt.Sprintf("servers.%d.password", i)) {
			return fmt.Errorf(
				"server %q has a `password:` field, which is no longer supported.\n"+
					"  Remove it and set `key_path:` instead. Secrets belong in apps/<name>/.env on the\n"+
					"  server (chmod 600), never in a config file that lives in the repo",
				s.Name)
		}
	}

	if len(cfg.Servers) == 0 {
		return fmt.Errorf("no servers found in config")
	}

	seen := map[string]bool{}
	for i := range cfg.Servers {
		s := &cfg.Servers[i]
		if err := s.Validate(); err != nil {
			return err
		}
		if seen[s.Name] {
			return fmt.Errorf("server name %q is used more than once", s.Name)
		}
		seen[s.Name] = true
	}
	return nil
}

// ServerNames returns configured server names, sorted, for error messages.
func ServerNames(servers []domain.Server) string {
	names := make([]string, 0, len(servers))
	for _, s := range servers {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// EnsureKeyFilesReadable checks every configured key exists and is not group/world
// readable. A private key with loose permissions is an ssh client error anyway; failing
// here names the offending file instead of surfacing "permissions too open" from a
// dial attempt.
func EnsureKeyFilesReadable(servers []domain.Server) error {
	for _, s := range servers {
		if s.KeyPath == "" {
			continue
		}
		info, err := os.Stat(s.KeyPath)
		if err != nil {
			return fmt.Errorf("server %q: cannot read key_path %s: %w", s.Name, s.KeyPath, err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf(
				"server %q: key %s has permissions %04o; SSH requires 0600 (chmod 600 %s)",
				s.Name, s.KeyPath, info.Mode().Perm(), s.KeyPath)
		}
	}
	return nil
}
