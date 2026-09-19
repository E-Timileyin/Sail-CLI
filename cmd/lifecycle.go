package cmd

import (
	"fmt"
	"strconv"

	"github.com/E-Timileyin/sail/internal/logger"
	"github.com/E-Timileyin/sail/internal/remote"
	"github.com/spf13/cobra"
)

var (
	rollbackServer     string
	rollbackConfig     string
	rollbackRoot       string
	rollbackAcceptNew  bool
	rollbackKnownHosts string
	rollbackDryRun     bool
)

var rollbackCmd = &cobra.Command{
	Use:   "rollback <app>",
	Short: "Redeploy the previously deployed tag",
	Long: `Roll back an app to the tag it was running before the current one.

The previous tag is recorded on the server (apps/<app>/.tag.prev) and is only updated
after a deploy becomes healthy, so a rollback target always points at something that
passed its healthcheck at least once.

Rollback is repeatable: rolling back twice returns the app to where it started.`,
	Args: cobra.ExactArgs(1),
	RunE: runRollback,
}

func init() {
	rootCmd.AddCommand(rollbackCmd)
	addServerFlags(rollbackCmd, &rollbackConfig, &rollbackServer, &rollbackRoot, &rollbackAcceptNew, &rollbackKnownHosts)
	rollbackCmd.Flags().BoolVar(&rollbackDryRun, "dry-run", false, "Print what would run, without changing anything")
}

func runRollback(cmd *cobra.Command, args []string) error {
	app := args[0]
	if err := remote.ValidateAppName(app); err != nil {
		return err
	}

	if rollbackDryRun {
		// The previous tag lives on the server, so a dry run reports what it would do
		// rather than guessing a tag.
		logger.Log.Infof("[dry-run] would read %s on the server and redeploy the recorded previous tag",
			remote.Layout{Root: rollbackRoot, App: app}.PreviousTagPath())
		logger.Log.Info("[dry-run] no connection made, nothing changed")
		return nil
	}

	runner, layout, err := connect(rollbackConfig, rollbackServer, rollbackRoot, app,
		rollbackAcceptNew, rollbackKnownHosts)
	if err != nil {
		return err
	}
	defer runner.Close()

	deployer := remote.NewDeployer(runner, layout)
	out, err := deployer.Rollback()
	if err != nil {
		return err
	}

	logger.Log.Infof("Rolled %s back to %s (was %s)", app, out.Tag, out.PreviousTag)
	return nil
}

var (
	logsServer     string
	logsConfig     string
	logsRoot       string
	logsAcceptNew  bool
	logsKnownHosts string
	logsTail       int
	logsFollow     bool
)

var logsCmd = &cobra.Command{
	Use:   "logs <app>",
	Short: "Show an app's container logs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app := args[0]

		runner, layout, err := connect(logsConfig, logsServer, logsRoot, app, logsAcceptNew, logsKnownHosts)
		if err != nil {
			return err
		}
		defer runner.Close()

		res := runner.Run(remote.LogsScript(layout, logsTail, logsFollow))
		if res.Stdout != "" {
			fmt.Print(res.Stdout)
		}
		if res.Stderr != "" {
			fmt.Fprint(cmd.ErrOrStderr(), res.Stderr)
		}
		// Propagate the remote exit status so `sail logs` is usable in a pipeline.
		return res.Err
	},
}

func init() {
	rootCmd.AddCommand(logsCmd)
	addServerFlags(logsCmd, &logsConfig, &logsServer, &logsRoot, &logsAcceptNew, &logsKnownHosts)
	logsCmd.Flags().IntVar(&logsTail, "tail", 100, "Number of lines to show")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
}

var (
	historyServer     string
	historyConfig     string
	historyRoot       string
	historyAcceptNew  bool
	historyKnownHosts string
	historyLimit      int
)

var historyCmd = &cobra.Command{
	Use:   "history <app>",
	Short: "Show an app's deployment history",
	Long: `Show the append-only deploy log for an app, newest last.

Each line records a timestamp, the tag and the outcome. A "started" line with no matching
outcome means a deploy was interrupted — which is itself the signal that something went
wrong, so it is recorded rather than papered over.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app := args[0]

		runner, layout, err := connect(historyConfig, historyServer, historyRoot, app,
			historyAcceptNew, historyKnownHosts)
		if err != nil {
			return err
		}
		defer runner.Close()

		res := runner.Run(remote.ReadLogScript(layout, historyLimit))
		if res.Stdout != "" {
			fmt.Print(res.Stdout)
		}
		return res.Err
	},
}

func init() {
	rootCmd.AddCommand(historyCmd)
	addServerFlags(historyCmd, &historyConfig, &historyServer, &historyRoot, &historyAcceptNew, &historyKnownHosts)
	historyCmd.Flags().IntVar(&historyLimit, "limit", 20, "Number of entries to show")
}

var (
	statusServer     string
	statusConfig     string
	statusRoot       string
	statusAcceptNew  bool
	statusKnownHosts string
)

var statusCmd = &cobra.Command{
	Use:   "status <app>",
	Short: "Show an app's container status and recorded tag",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app := args[0]

		runner, layout, err := connect(statusConfig, statusServer, statusRoot, app,
			statusAcceptNew, statusKnownHosts)
		if err != nil {
			return err
		}
		defer runner.Close()

		stateRes := runner.Run(remote.ReadStateScript(layout))
		if stateRes.Err != nil {
			return fmt.Errorf("cannot read state for %q: %w", app, stateRes.Err)
		}
		state := remote.ParseState(stateRes.Stdout)

		fmt.Printf("app:      %s\n", app)
		fmt.Printf("server:   %s\n", runner.Server().Name)
		fmt.Printf("tag:      %s\n", orNone(state.CurrentTag))
		fmt.Printf("previous: %s\n", orNone(state.PreviousTag))
		fmt.Println()

		res := runner.Run(remote.StatusScript(layout))
		if res.Stdout != "" {
			fmt.Print(res.Stdout)
		}
		if res.Stderr != "" {
			fmt.Fprint(cmd.ErrOrStderr(), res.Stderr)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
	addServerFlags(statusCmd, &statusConfig, &statusServer, &statusRoot, &statusAcceptNew, &statusKnownHosts)
}

var _ = strconv.Itoa
