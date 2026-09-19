package cmd

import (
	"fmt"
	"strings"

	"github.com/E-Timileyin/sail/internal/logger"
	"github.com/E-Timileyin/sail/internal/remote"
	"github.com/spf13/cobra"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage apps on a server",
}

var (
	appNewServer     string
	appNewConfig     string
	appNewRoot       string
	appNewAcceptNew  bool
	appNewKnownHosts string
	appNewImage      string
	appNewPort       int
	appNewDryRun     bool
)

var appNewCmd = &cobra.Command{
	Use:   "new <app>",
	Short: "Scaffold an app on the server",
	Long: `Create an app's directory and compose file on the server.

Creates, under <remote-root>/<app>/:

  compose.yaml   image reference, ports, healthcheck
  .env           empty, chmod 600, and NEVER overwritten if it exists
  .image         the image repository, so deploy does not need --image each time

Sail does not write secrets. An existing .env is left exactly as it is; put the app's
secrets in it yourself. The healthcheck in the generated compose file is what
` + "`up -d --wait`" + ` gates on, so rollback depends on it being correct.`,
	Args: cobra.ExactArgs(1),
	RunE: runAppNew,
}

var (
	appRmServer     string
	appRmConfig     string
	appRmRoot       string
	appRmAcceptNew  bool
	appRmKnownHosts string
	appRmYes        bool
)

var appRmCmd = &cobra.Command{
	Use:   "rm <app>",
	Short: "Remove an app from the server",
	Long: `Stop an app's containers and delete its directory.

This deletes the app's .env, so any secrets in it are gone. Requires --yes.`,
	Args: cobra.ExactArgs(1),
	RunE: runAppRm,
}

func init() {
	rootCmd.AddCommand(appCmd)
	appCmd.AddCommand(appNewCmd)
	appCmd.AddCommand(appRmCmd)

	addServerFlags(appNewCmd, &appNewConfig, &appNewServer, &appNewRoot, &appNewAcceptNew, &appNewKnownHosts)
	appNewCmd.Flags().StringVar(&appNewImage, "image", "", "Image repository to record (e.g. ghcr.io/you/my-api)")
	appNewCmd.Flags().IntVar(&appNewPort, "port", 8080, "Container port the app listens on")
	appNewCmd.Flags().BoolVar(&appNewDryRun, "dry-run", false, "Print the files that would be written")

	addServerFlags(appRmCmd, &appRmConfig, &appRmServer, &appRmRoot, &appRmAcceptNew, &appRmKnownHosts)
	appRmCmd.Flags().BoolVar(&appRmYes, "yes", false, "Confirm removal (required)")
}

func runAppNew(cmd *cobra.Command, args []string) error {
	app := args[0]

	layout, err := remote.NewLayout(appNewRoot, app)
	if err != nil {
		return err
	}

	if appNewDryRun {
		logger.Log.Infof("[dry-run] would create %s on server %s", layout.Dir(), orAny(appNewServer, "(single configured server)"))
		logger.Log.Infof("[dry-run] %s:\n%s", layout.ComposePath(), remote.ComposeTemplate(app, appNewPort))
		logger.Log.Infof("[dry-run] %s: created empty and chmod 600 (existing files are never overwritten)", layout.EnvPath())
		if appNewImage != "" {
			logger.Log.Infof("[dry-run] %s: %s", layout.ImageRefPath(), appNewImage)
		}
		logger.Log.Info("[dry-run] no connection made, nothing changed")
		return nil
	}

	runner, layout, err := connect(appNewConfig, appNewServer, appNewRoot, app,
		appNewAcceptNew, appNewKnownHosts)
	if err != nil {
		return err
	}
	defer runner.Close()

	// Refused: .env may hold secrets, and the compose file may hold healthcheck tweaks.
	existsRes := runner.Run(remote.AppExistsScript(layout))
	if existsRes.Err != nil {
		return fmt.Errorf("cannot check whether app %q exists: %w", app, existsRes.Err)
	}
	if strings.Contains(existsRes.Stdout, "exists") {
		return fmt.Errorf(
			"app %q already exists at %s on %s. Refusing to overwrite its compose file and .env.\n"+
				"  To redeploy it, use: sail deploy %s --tag <sha>\n"+
				"  To recreate it, remove it first: sail app rm %s --yes",
			app, layout.Dir(), runner.Server().Name, app, app)
	}

	if err := runner.MustRun(remote.MkdirScript(layout.Dir())); err != nil {
		return fmt.Errorf("cannot create %s: %w", layout.Dir(), err)
	}

	if err := runner.WriteFile(layout.ComposePath(), remote.ComposeTemplate(app, appNewPort)); err != nil {
		return err
	}
	logger.Log.Infof("wrote %s", layout.ComposePath())

	if err := runner.MustRun(remote.EnsureEnvScript(layout.EnvPath())); err != nil {
		return fmt.Errorf("cannot create %s: %w", layout.EnvPath(), err)
	}
	logger.Log.Infof("ensured %s (chmod 600, never overwritten)", layout.EnvPath())

	if appNewImage != "" {
		if err := remote.SetImageRef(runner, layout, appNewImage); err != nil {
			return fmt.Errorf("cannot record image: %w", err)
		}
		logger.Log.Infof("recorded image %s", appNewImage)
	} else {
		logger.Log.Warn("no --image recorded: pass --image on the next run, or set APP_IMAGE in .env before deploying")
	}

	logger.Log.Infof("app %q scaffolded at %s on %s", app, layout.Dir(), runner.Server().Name)
	logger.Log.Infof("next: put secrets in %s, then run: sail deploy %s --tag <sha>", layout.EnvPath(), app)
	return nil
}

func runAppRm(cmd *cobra.Command, args []string) error {
	app := args[0]

	layout, err := remote.NewLayout(appRmRoot, app)
	if err != nil {
		return err
	}

	if !appRmYes {
		// A refusal, not a dry run: removal deletes .env, which may hold secrets that
		// exist nowhere else.
		return fmt.Errorf(
			"refusing to remove app %q without --yes. This deletes %s, including any secrets in it",
			app, layout.Dir())
	}

	runner, layout, err := connect(appRmConfig, appRmServer, appRmRoot, app,
		appRmAcceptNew, appRmKnownHosts)
	if err != nil {
		return err
	}
	defer runner.Close()

	if err := runner.MustRun(remote.RemoveAppScript(layout)); err != nil {
		return fmt.Errorf("cannot remove app %q: %w", app, err)
	}

	logger.Log.Infof("removed app %q from %s", app, runner.Server().Name)
	return nil
}

func orAny(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
