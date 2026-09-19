package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/E-Timileyin/sail/internal/config"
	"github.com/E-Timileyin/sail/internal/domain"
	"github.com/spf13/cobra"
)

var sshCmd = &cobra.Command{
	Use:   "ssh [server-name] [command]",
	Short: "SSH into a server defined in config",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serverName := args[0]

		servers, err := config.LoadConfig("config.yaml")
		if err != nil {
			return fmt.Errorf("failed to load config: %v", err)
		}

		var targetServer *domain.Server
		for i, s := range servers {
			if s.Name == serverName {
				targetServer = &servers[i]
				break
			}
		}

		if targetServer == nil {
			return fmt.Errorf("server '%s' not found in config", serverName)
		}

		sshArgs := []string{
			"-p", fmt.Sprintf("%d", targetServer.Port),
			fmt.Sprintf("%s@%s", targetServer.User, targetServer.Host),
		}

		if len(args) > 1 {
			sshArgs = append(sshArgs, args[1:]...)
		}

		sshCmd := exec.Command("ssh", sshArgs...)
		sshCmd.Stdin = os.Stdin
		sshCmd.Stdout = os.Stdout
		sshCmd.Stderr = os.Stderr

		return sshCmd.Run()
	},
}

func init() {
	rootCmd.AddCommand(sshCmd)
}
