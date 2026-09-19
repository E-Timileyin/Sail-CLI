package cmd

import (
	"fmt"
	"strings"

	"github.com/E-Timileyin/sail/internal/config"
	"github.com/E-Timileyin/sail/internal/domain"
	"github.com/E-Timileyin/sail/internal/logger"
	"github.com/E-Timileyin/sail/internal/remote"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy <app> --tag <sha>",
	Short: "Deploy an app to a server",
	Long: `Deploy an app by pinning its image tag on the server and running

    docker compose up -d --wait

--wait blocks on the container's own HEALTHCHECK, so a container that starts but never
becomes healthy fails the deploy and Sail rolls back to the previously deployed tag.

The server needs the app scaffolded first (sail app new); Sail never writes secrets.`,
	Args: cobra.ExactArgs(1),
	RunE: runDeploy,
}

var (
	deployTag        string
	deployServer     string
	deployConfig     string
	deployRef        string
	deployRoot       string
	deployDryRun     bool
	deployAcceptNew  bool
	deployKnownHosts string
)

func init() {
	rootCmd.AddCommand(deployCmd)

	deployCmd.Flags().StringVar(&deployTag, "tag", "", "Image tag to deploy (required; pass the git SHA)")
	deployCmd.Flags().StringVar(&deployServer, "server", "", "Server name from config (required unless the config has exactly one)")
	deployCmd.Flags().StringVar(&deployConfig, "config", "config.yaml", "Path to the config file")
	deployCmd.Flags().StringVar(&deployRef, "image", "", "Image repository, overriding the app's recorded image")
	deployCmd.Flags().StringVar(&deployRoot, "remote-root", "/srv/sail", "Base directory for apps on the server")
	deployCmd.Flags().BoolVar(&deployDryRun, "dry-run", false, "Print what would run, without changing anything")
	deployCmd.Flags().BoolVar(&deployAcceptNew, "accept-new", false,
		"Accept and record an unknown host key on first connect (like StrictHostKeyChecking=accept-new)")
	deployCmd.Flags().StringVar(&deployKnownHosts, "known-hosts", "", "Path to known_hosts (default ~/.ssh/known_hosts)")

	_ = deployCmd.MarkFlagRequired("tag")
}

func runDeploy(cmd *cobra.Command, args []string) error {
	app := args[0]

	if err := remote.ValidateAppName(app); err != nil {
		return err
	}

	// Before config or dialing: no point connecting to reject "latest".
	if deployTag == "" {
		return fmt.Errorf("--tag is required")
	}
	if deployTag == "latest" {
		return fmt.Errorf(
			"--tag latest is not allowed: rollback needs a target that identifies a specific artifact. Pass the git SHA")
	}

	layout, err := remote.NewLayout(deployRoot, app)
	if err != nil {
		return err
	}

	// Stops before config and key checks: a dry run is most wanted on a machine that has
	// neither.
	if deployDryRun {
		logger.Log.Infof("[dry-run] app:  %s", app)
		logger.Log.Infof("[dry-run] tag:  %s", deployTag)
		logger.Log.Infof("[dry-run] root: %s", layout.Dir())
		if deployRef != "" {
			logger.Log.Infof("[dry-run] image: %s", deployRef)
		}
		logger.Log.Infof("[dry-run] would run:\n  %s",
			strings.ReplaceAll(remote.DeployScript(layout, deployTag), " && ", "\n  && "))
		logger.Log.Info("[dry-run] no connection made, nothing changed")
		return nil
	}

	cfg, err := config.Load(deployConfig)
	if err != nil {
		return err
	}
	if err := config.EnsureKeyFilesReadable(cfg.Servers); err != nil {
		return err
	}

	server, err := selectServer(cfg.Servers, deployServer)
	if err != nil {
		return err
	}
	applyTrustFlags(server, deployAcceptNew, deployKnownHosts)

	runner, err := remote.Dial(server)
	if err != nil {
		return err
	}
	defer runner.Close()

	if err := ensureAppScaffolded(runner, layout, app); err != nil {
		return err
	}
	if deployRef != "" {
		if err := remote.SetImageRef(runner, layout, deployRef); err != nil {
			return fmt.Errorf("cannot record image %q on the server: %w", deployRef, err)
		}
	}

	logger.Log.Infof("Deploying %s:%s to %s", app, deployTag, server.Name)

	deployer := remote.NewDeployer(runner, layout)
	out, err := deployer.Deploy(deployTag)
	if err != nil {
		reportDeployFailure(out, server.Name, app)
		return err
	}

	logger.Log.Infof("Deployed %s tag %s to %s (previous: %s)",
		app, out.Tag, server.Name, orNone(out.PreviousTag))
	return nil
}

func reportDeployFailure(out *remote.DeployOutcome, serverName, app string) {
	if out == nil {
		return
	}
	if out.FailedStage != "" {
		logger.Log.Errorf("deploy failed at stage %q on %s", out.FailedStage, serverName)
	}
	switch {
	case out.RolledBack:
		logger.Log.Warnf("%s is back on the previous tag %s", app, out.PreviousTag)
	case out.RollbackError != nil:
		logger.Log.Errorf("ROLLBACK FAILED on %s: %s may be running no working version", serverName, app)
	case out.Attempted:
		logger.Log.Errorf("%s was left as it was; no rollback target existed", app)
	}
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func selectServer(servers []domain.Server, name string) (*domain.Server, error) {
	if name == "" {
		if len(servers) == 1 {
			return &servers[0], nil
		}
		return nil, fmt.Errorf(
			"--server is required when the config defines %d servers: %s",
			len(servers), config.ServerNames(servers))
	}
	for i := range servers {
		if servers[i].Name == name {
			return &servers[i], nil
		}
	}
	return nil, fmt.Errorf("server %q is not in the config; known servers: %s",
		name, config.ServerNames(servers))
}

func applyTrustFlags(s *domain.Server, acceptNew bool, knownHosts string) {
	if acceptNew {
		s.TrustPolicy = domain.AcceptNew
	}
	s.KnownHostsPath = knownHosts
}

// ensureAppScaffolded fails early: compose in a directory with no compose file gives a
// confusing error from the remote shell.
func ensureAppScaffolded(runner *remote.Runner, layout remote.Layout, app string) error {
	res := runner.Run(remote.ReadStateScript(layout))
	if res.Err != nil {
		return fmt.Errorf("cannot read state for app %q: %w", app, res.Err)
	}
	state := remote.ParseState(res.Stdout)
	if err := state.ValidateForDeploy(); err != nil {
		return fmt.Errorf("app %q is not ready on the server: %w", app, err)
	}
	return nil
}
