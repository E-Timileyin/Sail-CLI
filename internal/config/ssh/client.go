package ssh

import (
	"bytes"
	"fmt"

	"os"
	"time"

	"github.com/E-Timileyin/sail/internal/domain"
	"github.com/E-Timileyin/sail/internal/sshx"
	"golang.org/x/crypto/ssh"
)

// CommandResult represents the result of an SSH command execution.
type CommandResult struct {
	Command string
	Status  string
	Message string
	Error   error
}

// ExecuteSSHCommand executes a command on a remote server via SSH.
func ExecuteSSHCommand(cfg domain.Server, command string) (*CommandResult, error) {
	result := &CommandResult{
		Command: command,
	}

	// 1. Read and parse private key
	keyBytes, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		result.Status = "fail"
		result.Error = fmt.Errorf("failed to read private key file: %w", err)
		return result, result.Error
	}

	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		result.Status = "fail"
		result.Error = fmt.Errorf("failed to parse private key: %w", err)
		return result, result.Error
	}

	// 2. Configure SSH client. Host keys are verified against known_hosts via sshx;
	// the previous ssh.InsecureIgnoreHostKey() call here meant this helper accepted
	// any key, so a MITM could impersonate the server for every command it ran.
	hostKeyCallback, err := sshx.HostKeyCallback(cfg.KnownHostsPath, cfg.TrustPolicy, cfg.Address())
	if err != nil {
		result.Status = "fail"
		result.Error = err
		return result, err
	}

	config := &ssh.ClientConfig{
		User: cfg.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}

	// 3. Connect to the server
	client, err := ssh.Dial("tcp", cfg.Address(), config)
	if err != nil {
		result.Status = "fail"
		result.Error = fmt.Errorf("failed to connect to %s: %w", cfg.Address(), err)
		return result, result.Error
	}
	defer client.Close()

	// 4. Create a session
	session, err := client.NewSession()
	if err != nil {
		result.Status = "fail"
		result.Error = fmt.Errorf("failed to create session: %w", err)
		return result, result.Error
	}
	defer session.Close()

	// 5. Capture output
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	// 6. Run the command
	if err := session.Run(command); err != nil {
		result.Status = "fail"
		result.Message = stderr.String()
		result.Error = fmt.Errorf("command failed: %w", err)
		return result, result.Error
	}

	// 7. Return success
	result.Status = "success"
	result.Message = stdout.String()
	return result, nil
}
