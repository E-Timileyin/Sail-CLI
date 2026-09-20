package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the application server",
	Long:  `Start the HTTP/HTTPS server to serve the application.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// serve is a stub: it never bound a socket, but used to log success and
		// return nil (#19). Flags for --deploy/--port/--config are not registered
		// while the command cannot honor them (a silent no-op is the same class
		// of bug as a false success log). Fail before any deploy side effect.
		return fmt.Errorf("sail serve is not implemented: no listener is bound")
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
