package cmd

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/E-Timileyin/sail/internal/config"
	"github.com/E-Timileyin/sail/internal/domain"
	"github.com/E-Timileyin/sail/internal/logger"
	"github.com/E-Timileyin/sail/internal/sshx"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

var deployCmd = &cobra.Command{
	Use:   "deploy <config-file>",
	Short: "Deploy containers to remote servers",
	Long: `Deploy your application to one or more remote servers over SSH.

Host keys are verified against known_hosts. Use --accept-new to trust a host on
first connect, the same way OpenSSH's StrictHostKeyChecking=accept-new does.`,
	Args: cobra.ExactArgs(1),
	RunE: runDeploy,
}

var (
	dryRun         bool
	acceptNewHost  bool
	knownHostsPath string
)

func init() {
	rootCmd.AddCommand(deployCmd)

	deployCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show the plan without changing anything")
	deployCmd.Flags().BoolVar(&acceptNewHost, "accept-new", false,
		"Accept and record an unknown host key on first connect (like StrictHostKeyChecking=accept-new)")
	deployCmd.Flags().StringVar(&knownHostsPath, "known-hosts", "",
		"Path to known_hosts (default ~/.ssh/known_hosts)")
}

// runDeploy reports failure honestly.
//
// This previously returned nil unconditionally while logging per-server errors, so a
// deploy that failed on every server exited 0 and CI marked it green. Failures are now
// aggregated and returned, and cmd.Execute turns a non-nil error into exit code 1.
func runDeploy(cmd *cobra.Command, args []string) error {
	configFile := args[0]
	logger.Log.Infof("Starting deployment using config: %s", configFile)

	cfg, err := config.Load(configFile)
	if err != nil {
		return err
	}
	servers := cfg.Servers

	if err := config.EnsureKeyFilesReadable(servers); err != nil {
		return err
	}

	policy := domain.Strict
	if acceptNewHost {
		policy = domain.AcceptNew
		logger.Log.Warn("--accept-new is set: unknown host keys will be recorded in known_hosts")
	}
	for i := range servers {
		servers[i].TrustPolicy = policy
		servers[i].KnownHostsPath = knownHostsPath
	}

	var failed []string
	for i := range servers {
		server := &servers[i]

		if dryRun {
			// Short-circuit before any connection or mutation. Previously --dry-run was
			// registered but never read, so passing it performed a real deploy.
			logger.Log.Infof("[dry-run] would deploy to %s (%s) using key %s",
				server.Name, server.Address(), server.KeyPath)
			continue
		}

		logger.Log.Infof("Deploying to server: %s (%s)", server.Name, server.Host)

		if err := deployToServer(cmd, server); err != nil {
			logger.Log.Errorf("Deployment failed on %s: %v", server.Name, err)
			failed = append(failed, server.Name)
			continue
		}
		logger.Log.Infof("Successfully deployed to %s", server.Name)
	}

	if dryRun {
		logger.Log.Infof("[dry-run] no changes made")
		return nil
	}

	if len(failed) > 0 {
		return fmt.Errorf("deploy failed on %d/%d servers: %s",
			len(failed), len(servers), strings.Join(failed, ", "))
	}
	return nil
}

// deployToServer runs the deploy for one server. Split out so the loop can count
// failures without nested error handling.
func deployToServer(cmd *cobra.Command, server *domain.Server) error {
	sshConfig, err := sshx.ClientConfig(server)
	if err != nil {
		return err
	}

	client, err := dial(cmd, server, sshConfig)
	if err != nil {
		return err
	}
	defer client.Close()

	logger.Log.Debugf("Connected to %s", server.Address())

	return executeDeployment(client, server)
}

// dial opens the SSH connection, turning the two trust failures into advice.
func dial(_ *cobra.Command, server *domain.Server, sshConfig *ssh.ClientConfig) (*ssh.Client, error) {
	client, err := ssh.Dial("tcp", server.Address(), sshConfig)
	if err != nil {
		switch {
		case sshx.IsKeyMismatchError(err):
			return nil, fmt.Errorf(
				"host key for %s does not match known_hosts — possible machine-in-the-middle; "+
					"verify out of band, then run: ssh-keygen -R %s", server.Name, server.Host)
		case sshx.IsUnknownHostError(err):
			return nil, fmt.Errorf(
				"host %s is not in known_hosts; pass --accept-new to trust it on first connect", server.Host)
		}
		return nil, fmt.Errorf("cannot connect to %s (%s): %w", server.Name, server.Address(), err)
	}
	return client, nil
}

// executeDeployment runs the deployment commands on the remote server.
func executeDeployment(client *ssh.Client, _ *domain.Server) error {
	for _, check := range []struct{ name, cmd string }{
		{"docker", "docker --version"},
		{"docker compose", "docker compose version || docker-compose --version"},
	} {
		if err := runCommand(client, check.cmd); err != nil {
			return fmt.Errorf("%s is not available on the server: %w", check.name, err)
		}
	}

	commands := []struct {
		cmd         string
		ignoreError bool
	}{
		{cmd: "docker compose pull", ignoreError: false},
		{cmd: "docker compose down", ignoreError: true},
		// --wait blocks on the container's HEALTHCHECK and exits non-zero if it never
		// becomes healthy, so Sail does not need its own probe. See fixes.md.
		{cmd: "docker compose up -d --wait", ignoreError: false},
	}

	for _, c := range commands {
		logger.Log.Debugf("Executing: %s", c.cmd)
		if err := runCommand(client, c.cmd); err != nil && !c.ignoreError {
			return fmt.Errorf("command failed: %s: %w", c.cmd, err)
		}
	}

	return nil
}

// runCommand executes one command over SSH, returning stderr in the error so failures
// are diagnosable without a second round trip.
func runCommand(client *ssh.Client, command string) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("cannot create session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(command); err != nil {
		return fmt.Errorf("%w\nSTDOUT: %s\nSTDERR: %s", err, stdout.String(), stderr.String())
	}

	logger.Log.Debugf("Command output:\n%s", stdout.String())
	if stderr.Len() > 0 {
		logger.Log.Warnf("Command stderr:\n%s", stderr.String())
	}
	return nil
}
