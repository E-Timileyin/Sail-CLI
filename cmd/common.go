package cmd

import (
	"github.com/E-Timileyin/sail/internal/config"
	"github.com/E-Timileyin/sail/internal/remote"
	"github.com/spf13/cobra"
)

// connect is shared by every server-touching command so trust flags and server selection
// cannot drift between them.
func connect(configPath, serverName, remoteRoot, app string, acceptNew bool, knownHosts string) (*remote.Runner, remote.Layout, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, remote.Layout{}, err
	}
	if err := config.EnsureKeyFilesReadable(cfg.Servers); err != nil {
		return nil, remote.Layout{}, err
	}

	server, err := selectServer(cfg.Servers, serverName)
	if err != nil {
		return nil, remote.Layout{}, err
	}
	applyTrustFlags(server, acceptNew, knownHosts)

	layout, err := remote.NewLayout(remoteRoot, app)
	if err != nil {
		return nil, remote.Layout{}, err
	}

	runner, err := remote.Dial(server)
	if err != nil {
		return nil, remote.Layout{}, err
	}
	return runner, layout, nil
}

func addServerFlags(c *cobra.Command, configPath, serverName, remoteRoot *string, acceptNew *bool, knownHosts *string) {
	c.Flags().StringVar(configPath, "config", "config.yaml", "Path to the config file")
	c.Flags().StringVar(serverName, "server", "", "Server name from config (required unless the config has exactly one)")
	c.Flags().StringVar(remoteRoot, "remote-root", "/srv/sail", "Base directory for apps on the server")
	c.Flags().BoolVar(acceptNew, "accept-new", false,
		"Accept and record an unknown host key on first connect (like StrictHostKeyChecking=accept-new)")
	c.Flags().StringVar(knownHosts, "known-hosts", "", "Path to known_hosts (default ~/.ssh/known_hosts)")
}
