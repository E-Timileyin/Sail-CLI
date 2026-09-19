package cmd

import (
	"os"

	"github.com/E-Timileyin/sail/internal/logger"
	"github.com/spf13/cobra"
)

var (
	logLevel  string
	logFormat string
)

var rootCmd = &cobra.Command{
	Use:   "sail",
	Short: "Deploy Dockerized apps to a server over SSH, with rollback",
	Long: `Sail deploys an app by pinning its image tag on the server and running
docker compose up -d --wait, which blocks on the container's own HEALTHCHECK.

A deploy that never becomes healthy is rolled back to the previously deployed tag.`,
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		logger.Initialize(logger.Config{
			Level:  logger.Level(logLevel),
			Format: logFormat,
		})
		// Not logged here: "starting up" on every invocation is noise, and it corrupts
		// --version output in scripts.
		return nil
	},
}

// SetVersion records the build's version for --version and for bug reports.
func SetVersion(v string) {
	rootCmd.Version = v
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// The command has already reported the error; SilenceUsage keeps cobra from
		// printing the full flag list after every failure.
		if logger.Log != nil {
			logger.Log.Errorf("Command failed: %v", err)
		}
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "info", "Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "Log format (text or json)")

	// {{.Version}} is what SetVersion supplies. A hardcoded template would ignore it,
	// which silently printed the package default instead of the release tag.
	rootCmd.SetVersionTemplate("{{.Version}}\n")
}
