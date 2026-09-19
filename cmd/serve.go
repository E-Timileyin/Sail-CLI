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
	RunE:  runServe,
}

func runServe(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("serve is not implemented yet; no server was started")
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().BoolVar(&autoDeploy, "deploy", false, "Deploy before starting the server")
	serveCmd.Flags().StringVar(&configFile, "config", "config.yaml", "Path to deployment config file (required if --deploy is set)")
	serveCmd.Flags().StringVarP(&serverPort, "port", "p", "8080", "Port to run the server on")
}
