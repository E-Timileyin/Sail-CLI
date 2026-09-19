package service

import (
	"context"
	"testing"
	"time"

	"github.com/E-Timileyin/sail/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockDockerClient is a mock implementation of the DockerClient interface
type MockDockerClient struct {
	mock.Mock
}

func (m *MockDockerClient) List(ctx context.Context) ([]model.Container, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]model.Container), args.Error(1)
}

func (m *MockDockerClient) Create(ctx context.Context, config *model.ContainerConfig) (string, error) {
	args := m.Called(ctx, config)
	if args.Get(0) == nil {
		return "", args.Error(1)
	}
	return args.String(0), args.Error(1)
}

func (m *MockDockerClient) Start(ctx context.Context, containerID string) error {
	args := m.Called(ctx, containerID)
	return args.Error(0)
}

func (m *MockDockerClient) Stop(ctx context.Context, containerID string) error {
	args := m.Called(ctx, containerID)
	return args.Error(0)
}

func (m *MockDockerClient) Remove(ctx context.Context, containerID string) error {
	args := m.Called(ctx, containerID)
	return args.Error(0)
}

func (m *MockDockerClient) Inspect(ctx context.Context, containerID string) (*model.ContainerInfo, error) {
	args := m.Called(ctx, containerID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.ContainerInfo), args.Error(1)
}

const testContainerID = "container123"

var testInfo = &model.ContainerInfo{
	ID:      testContainerID,
	Image:   "nginx:latest",
	Status:  "running",
	Created: time.Now().Add(-time.Hour),
}

func TestContainerService_List(t *testing.T) {
	mockClient := new(MockDockerClient)
	testContainers := []model.Container{
		{ID: "1", Image: "nginx:latest", Status: "running", Names: []string{"test-container-1"}},
		{ID: "2", Image: "redis:alpine", Status: "exited", Names: []string{"test-container-2"}},
	}

	tests := []struct {
		name          string
		setupMock     func()
		expected      []model.Container
		expectedError bool
	}{
		{
			name: "successful list",
			setupMock: func() {
				mockClient.On("List", mock.Anything).Return(testContainers, nil)
			},
			expected:      testContainers,
			expectedError: false,
		},
		{
			name: "error listing containers",
			setupMock: func() {
				mockClient.On("List", mock.Anything).Return(nil, assert.AnError)
			},
			expected:      nil,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock expectations
			mockClient.ExpectedCalls = nil

			tt.setupMock()
			svc := NewContainerServiceWithClient(mockClient)

			result, err := svc.List(context.Background())

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestContainerService_Create(t *testing.T) {
	mockClient := new(MockDockerClient)
	testConfig := &model.ContainerConfig{
		Image: "nginx:latest",
		Name:  "test-container",
		Env:   []string{"ENV=test"},
	}

	tests := []struct {
		name          string
		setupMock     func()
		expectedID    string
		expectedError bool
	}{
		{
			name: "successful create",
			setupMock: func() {
				mockClient.On("Create", mock.Anything, testConfig).Return("container123", nil)
			},
			expectedID:    "container123",
			expectedError: false,
		},
		{
			name: "error creating container",
			setupMock: func() {
				mockClient.On("Create", mock.Anything, testConfig).Return("", assert.AnError)
			},
			expectedID:    "",
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock expectations
			mockClient.ExpectedCalls = nil

			tt.setupMock()
			svc := NewContainerServiceWithClient(mockClient)

			result, err := svc.Create(context.Background(), testConfig)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedID, result)
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestContainerService_Start(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(mockClient *MockDockerClient)
		expectedError bool
	}{
		{
			name: "successful start",
			setupMock: func(mockClient *MockDockerClient) {
				mockClient.On("Start", mock.Anything, testContainerID).Return(nil)
			},
			expectedError: false,
		},
		{
			name: "error starting container",
			setupMock: func(mockClient *MockDockerClient) {
				mockClient.On("Start", mock.Anything, testContainerID).Return(assert.AnError)
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockDockerClient)
			// Reset mock expectations
			mockClient.ExpectedCalls = nil

			tt.setupMock(mockClient)
			svc := NewContainerServiceWithClient(mockClient)

			err := svc.Start(context.Background(), testContainerID)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestContainerService_Stop(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(mockClient *MockDockerClient)
		expectedError bool
	}{
		{
			name: "successful stop",
			setupMock: func(mockClient *MockDockerClient) {
				mockClient.On("Stop", mock.Anything, testContainerID).Return(nil)
			},
			expectedError: false,
		},
		{
			name: "error stopping container",
			setupMock: func(mockClient *MockDockerClient) {
				mockClient.On("Stop", mock.Anything, testContainerID).Return(assert.AnError)
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockDockerClient)
			// Reset mock expectations
			mockClient.ExpectedCalls = nil

			tt.setupMock(mockClient)
			svc := NewContainerServiceWithClient(mockClient)

			err := svc.Stop(context.Background(), testContainerID)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestContainerService_Remove(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(mockClient *MockDockerClient)
		expectedError bool
	}{
		{
			name: "successful remove",
			setupMock: func(mockClient *MockDockerClient) {
				mockClient.On("Remove", mock.Anything, testContainerID).Return(nil)
			},
			expectedError: false,
		},
		{
			name: "error removing container",
			setupMock: func(mockClient *MockDockerClient) {
				mockClient.On("Remove", mock.Anything, testContainerID).Return(assert.AnError)
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockDockerClient)
			// Reset mock expectations
			mockClient.ExpectedCalls = nil

			tt.setupMock(mockClient)
			svc := NewContainerServiceWithClient(mockClient)

			err := svc.Remove(context.Background(), testContainerID)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestContainerService_Inspect(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(mockClient *MockDockerClient)
		expected      *model.ContainerInfo
		expectedError bool
	}{
		{
			name: "successful inspect",
			setupMock: func(mockClient *MockDockerClient) {
				mockClient.On("Inspect", mock.Anything, testContainerID).Return(testInfo, nil)
			},
			expected:      testInfo,
			expectedError: false,
		},
		{
			name: "error inspecting container",
			setupMock: func(mockClient *MockDockerClient) {
				mockClient.On("Inspect", mock.Anything, testContainerID).Return(nil, assert.AnError)
			},
			expected:      nil,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockDockerClient)
			// Reset mock expectations
			mockClient.ExpectedCalls = nil

			tt.setupMock(mockClient)
			svc := NewContainerServiceWithClient(mockClient)

			result, err := svc.Inspect(context.Background(), testContainerID)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
			mockClient.AssertExpectations(t)
		})
	}
}
