package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/sirupsen/logrus"
)

type Manager struct {
	client *client.Client
	logger *logrus.Logger
}

// NewManager creates a new Docker manager instance
func NewManager(logger *logrus.Logger) (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	return &Manager{
		client: cli,
		logger: logger,
	}, nil
}

// CheckDocker verifies Docker is running and accessible
func (m *Manager) CheckDocker(ctx context.Context) error {
	_, err := m.client.Ping(ctx)
	if err != nil {
		return fmt.Errorf("Docker daemon is not available: %w", err)
	}
	m.logger.Info("Successfully connected to Docker daemon")
	return nil
}

// ContainerStatus returns the status of a container by name
func (m *Manager) ContainerStatus(ctx context.Context, name string) (string, error) {
	containers, err := m.client.ContainerList(ctx, types.ContainerListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	for _, c := range containers {
		for _, n := range c.Names {
			if n == "/"+name || n == name {
				return c.State, nil
			}
		}
	}

	return "not found", nil
}

// PullImage pulls the latest version of a Docker image
func (m *Manager) PullImage(ctx context.Context, imageRef string) error {
	m.logger.Infof("Pulling image: %s", imageRef)

	// Check if image already exists locally
	_, _, err := m.client.ImageInspectWithRaw(ctx, imageRef)
	if err == nil {
		m.logger.Infof("Image %s already exists locally, skipping pull", imageRef)
		return nil
	}
	if !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to check for existing image: %w", err)
	}

	// Pull the image if it doesn't exist locally
	out, err := m.client.ImagePull(ctx, imageRef, types.ImagePullOptions{
		All: false,
	})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", imageRef, err)
	}
	defer out.Close()

	// Read the output to ensure the pull completes
	_, err = io.Copy(io.Discard, out)
	if err != nil && err != io.EOF {
		return fmt.Errorf("error reading pull output: %w", err)
	}

	m.logger.Infof("Successfully pulled image: %s", imageRef)
	return nil
}

// CreateContainer creates a new container with the specified configuration
func (m *Manager) CreateContainer(ctx context.Context, name, image string, config *container.Config, hostConfig *container.HostConfig) (string, error) {
	m.logger.Infof("Creating container: %s", name)

	// Remove container if it already exists
	_ = m.StopContainer(ctx, name)
	_ = m.RemoveContainer(ctx, name)

	resp, err := m.client.ContainerCreate(ctx, config, hostConfig, nil, nil, name)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	m.logger.Infof("Created container %s with ID: %s", name, resp.ID[:12])
	return resp.ID, nil
}

// StartContainer starts a container by name
func (m *Manager) StartContainer(ctx context.Context, name string) error {
	m.logger.Infof("Starting container: %s", name)
	if err := m.client.ContainerStart(ctx, name, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("failed to start container %s: %w", name, err)
	}
	m.logger.Infof("Successfully started container: %s", name)
	return nil
}

// StopContainer stops a running container
func (m *Manager) StopContainer(ctx context.Context, name string) error {
	timeout := 10 // seconds
	m.logger.Infof("Stopping container: %s", name)

	// Create a context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// Create stop options with the timeout
	stopOptions := container.StopOptions{
		Signal:  "SIGTERM",
		Timeout: &timeout,
	}

	if err := m.client.ContainerStop(timeoutCtx, name, stopOptions); err != nil {
		// If container is already stopped, we can continue
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to stop container %s: %w", name, err)
		}
	}
	m.logger.Infof("Successfully stopped container: %s", name)
	return nil
}

// RenameContainer renames a container
func (m *Manager) RenameContainer(ctx context.Context, oldName, newName string) error {
	m.logger.Infof("Renaming container %s to %s", oldName, newName)
	if err := m.client.ContainerRename(ctx, oldName, newName); err != nil {
		// Ignore if the container doesn't exist, as it might have been removed already
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to rename container %s: %w", oldName, err)
		}
	}
	return nil
}

// RemoveContainer removes a container
func (m *Manager) RemoveContainer(ctx context.Context, name string) error {
	m.logger.Infof("Removing container: %s", name)
	if err := m.client.ContainerRemove(ctx, name, types.ContainerRemoveOptions{Force: true}); err != nil {
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to remove container %s: %w", name, err)
		}
	}
	m.logger.Infof("Successfully removed container: %s", name)
	return nil
}

// GetContainerLogs retrieves the logs for a container
func (m *Manager) GetContainerLogs(ctx context.Context, name string, tail int) (string, error) {
	options := types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
	}

	out, err := m.client.ContainerLogs(ctx, name, options)
	if err != nil {
		return "", fmt.Errorf("failed to get logs for container %s: %w", name, err)
	}
	defer out.Close()

	buf := new(strings.Builder)
	if _, err := io.Copy(buf, out); err != nil {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}

	return buf.String(), nil
}
