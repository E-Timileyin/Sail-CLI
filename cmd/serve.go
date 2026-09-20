package cmd

import (
	"fmt"

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
		// serve is a stub: it never bound a socket, but used to log success and
		// return nil (#19). Fail before --deploy so a supervisor cannot treat
		// exit 0 as readiness, and so a deploy is not chained onto a lie.
		return fmt.Errorf("sail serve is not implemented: no listener is bound on port %s", serverPort)
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().BoolVar(&autoDeploy, "deploy", false, "Deploy before starting the server")
	serveCmd.Flags().StringVar(&configFile, "config", "config.yaml", "Path to deployment config file (required if --deploy is set)")
	serveCmd.Flags().StringVarP(&serverPort, "port", "p", "8080", "Port to run the server on")
}
