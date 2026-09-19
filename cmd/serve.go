package cmd

import (
	"fmt"

	"github.com/E-Timileyin/sail/internal/logger"
	"github.com/spf13/cobra"
)

var (
	autoDeploy bool
	configFile string
	serverPort string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the application server",
	Long:  `Start the HTTP/HTTPS server to serve the application.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if autoDeploy {
			if err := runDeploy(cmd, []string{configFile}); err != nil {
				return fmt.Errorf("deployment failed: %v", err)
			}
		}

		logger.Log.Infof("Starting server on port %s...", serverPort)
		// TODO: not implemented. serve is a stub; --deploy chains a deploy, then this
		// prints and exits without listening.

		logger.Log.Info("Server started successfully")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().BoolVar(&autoDeploy, "deploy", false, "Deploy before starting the server")
	serveCmd.Flags().StringVar(&configFile, "config", "config.yaml", "Path to deployment config file (required if --deploy is set)")
	serveCmd.Flags().StringVarP(&serverPort, "port", "p", "8080", "Port to run the server on")
}
