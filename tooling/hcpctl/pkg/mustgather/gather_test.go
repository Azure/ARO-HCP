// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mustgather

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// MockQueryClient is a mock implementation of QueryClientInterface for testing
type MockQueryClient struct {
	mock.Mock
}

func (m *MockQueryClient) ConcurrentQueries(ctx context.Context, queries []*kusto.ConfigurableQuery, outputChannel chan<- kusto.TaggedRow) error {
	args := m.Called(ctx, queries, outputChannel)
	return args.Error(0)
}

func (m *MockQueryClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockQueryClient) ExecutePreconfiguredQuery(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- kusto.TaggedRow) (*kusto.QueryResult, error) {
	args := m.Called(ctx, query, outputChannel)
	result := args.Get(0)
	if result == nil {
		return nil, args.Error(1)
	}
	return result.(*kusto.QueryResult), args.Error(1)
}

func mockOutputFunc(ctx context.Context, logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
	for range logLineChan {
		// Consume all messages
	}
	return nil
}

func TestNewGatherer(t *testing.T) {
	mockQueryClient := &MockQueryClient{}
	opts := GathererOptions{
		QueryOptions: &QueryOptions{
			SubscriptionId:    "test-sub",
			ResourceGroupName: "test-rg",
		},
	}

	// Test CLI gatherer
	gatherer := NewCliGatherer(mockQueryClient, "/test/output", "services", "hcp", opts)
	assert.NotNil(t, gatherer)
	assert.Equal(t, mockQueryClient, gatherer.QueryClient)

	// Test custom gatherer
	customOutputFunc := func(ctx context.Context, logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
		for range logLineChan {
		}
		return nil
	}
	customOptions := RowOutputOptions{"outputPath": "/custom/path"}
	gatherer = NewGatherer(mockQueryClient, customOutputFunc, customOptions, opts)
	assert.NotNil(t, gatherer)
	assert.Equal(t, "/custom/path", gatherer.outputOptions["outputPath"])
}

func TestGatherer_GatherLogs(t *testing.T) {
	mockQueryClient := &MockQueryClient{}
	gatherer := &Gatherer{
		QueryClient: mockQueryClient,
		opts: GathererOptions{
			SkipKubernetesEventsLogs: true,
			QueryOptions: &QueryOptions{
				SubscriptionId:    "test-sub",
				ResourceGroupName: "test-rg",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
	}

	// Success case: cluster ID query + services + HCP queries
	mockQueryClient.On("ExecutePreconfiguredQuery", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("*kusto.ConfigurableQuery"), mock.Anything).Return(&kusto.QueryResult{}, nil).Once()
	mockQueryClient.On("ConcurrentQueries", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("[]*kusto.ConfigurableQuery"), mock.Anything).Return(nil).Twice()

	err := gatherer.GatherLogs(t.Context())
	assert.NoError(t, err)

	// Error case: cluster ID query fails
	mockQueryClient.On("ExecutePreconfiguredQuery", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("*kusto.ConfigurableQuery"), mock.Anything).Return(nil, errors.New("query failed"))

	err = gatherer.GatherLogs(t.Context())
	assert.Error(t, err)

	mockQueryClient.AssertExpectations(t)
}

func TestGatherer_GatherLogs_WithKubernetesEventsAndSystemdLogs(t *testing.T) {
	mockQueryClient := &MockQueryClient{}
	gatherer := &Gatherer{
		QueryClient: mockQueryClient,
		opts: GathererOptions{
			SkipKubernetesEventsLogs: false,
			CollectSystemdLogs:       true,
			QueryOptions: &QueryOptions{
				SubscriptionId:    "test-sub",
				ResourceGroupName: "test-rg",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
	}

	// 1x ExecutePreconfiguredQuery for cluster IDs
	mockQueryClient.On("ExecutePreconfiguredQuery", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("*kusto.ConfigurableQuery"), mock.Anything).Return(&kusto.QueryResult{}, nil)
	// 2x ConcurrentQueries for services + HCP,
	// 2x ConcurrentQueries for kubernetes events + systemd logs (empty queries since no cluster names)
	mockQueryClient.On("ConcurrentQueries", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("[]*kusto.ConfigurableQuery"), mock.Anything).Return(nil)

	err := gatherer.GatherLogs(t.Context())
	assert.NoError(t, err)

	mockQueryClient.AssertExpectations(t)
}

func TestGatherer_GatherLogs_SkipOnlySystemdLogs(t *testing.T) {
	mockQueryClient := &MockQueryClient{}
	gatherer := &Gatherer{
		QueryClient: mockQueryClient,
		opts: GathererOptions{
			SkipKubernetesEventsLogs: false,
			QueryOptions: &QueryOptions{
				SubscriptionId:    "test-sub",
				ResourceGroupName: "test-rg",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
	}

	// Cluster ID + cluster name queries
	mockQueryClient.On("ExecutePreconfiguredQuery", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("*kusto.ConfigurableQuery"), mock.Anything).Return(&kusto.QueryResult{}, nil)
	// Services + HCP + kubernetes events (no systemd)
	mockQueryClient.On("ConcurrentQueries", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("[]*kusto.ConfigurableQuery"), mock.Anything).Return(nil)

	err := gatherer.GatherLogs(t.Context())
	assert.NoError(t, err)

	// Verify ConcurrentQueries was called 3 times (services + HCP + kubernetes events, no systemd)
	mockQueryClient.AssertNumberOfCalls(t, "ConcurrentQueries", 3)
}

func TestGatherer_GatherInfraLogs(t *testing.T) {
	mockQueryClient := &MockQueryClient{}
	gatherer := &Gatherer{
		QueryClient: mockQueryClient,
		opts: GathererOptions{
			GatherInfraLogs: true,
			QueryOptions: &QueryOptions{
				InfraClusterName: "test-infra-cluster",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
		infraLogsOnly: true,
	}

	// 3x ConcurrentQueries: kubernetes events + systemd logs + services
	mockQueryClient.On("ConcurrentQueries", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("[]*kusto.ConfigurableQuery"), mock.Anything).Return(nil).Times(3)

	err := gatherer.GatherLogs(t.Context())
	assert.NoError(t, err)

	mockQueryClient.AssertExpectations(t)
}

func TestGatherer_GatherInfraLogs_Error(t *testing.T) {
	mockQueryClient := &MockQueryClient{}
	gatherer := &Gatherer{
		QueryClient: mockQueryClient,
		opts: GathererOptions{
			GatherInfraLogs: true,
			QueryOptions: &QueryOptions{
				InfraClusterName: "test-infra-cluster",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
		infraLogsOnly: true,
	}

	// First ConcurrentQueries (kubernetes events) fails
	mockQueryClient.On("ConcurrentQueries", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("[]*kusto.ConfigurableQuery"), mock.Anything).Return(errors.New("query failed")).Once()

	err := gatherer.GatherLogs(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "kubernetes events")

	mockQueryClient.AssertExpectations(t)
}

func TestGatherer_GatherLogs_ContextCancellation(t *testing.T) {
	mockQueryClient := &MockQueryClient{}
	ctx, cancel := context.WithCancel(t.Context())

	gatherer := &Gatherer{
		QueryClient: mockQueryClient,
		opts: GathererOptions{
			SkipKubernetesEventsLogs: true,
			QueryOptions: &QueryOptions{
				SubscriptionId:    "test-sub",
				ResourceGroupName: "test-rg",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
	}

	// Cancel context before the query can complete
	mockQueryClient.On("ExecutePreconfiguredQuery", mock.Anything, mock.AnythingOfType("*kusto.ConfigurableQuery"), mock.Anything).Run(func(args mock.Arguments) {
		cancel()
	}).Return(nil, context.Canceled).Once()

	err := gatherer.GatherLogs(ctx)
	assert.Error(t, err)
}

func TestCliOutputFunc(t *testing.T) {
	// Success case
	tempDir, err := os.MkdirTemp("", "test-gatherer-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	err = os.MkdirAll(filepath.Join(tempDir, "services"), 0755)
	require.NoError(t, err)

	logLineChan := make(chan *NormalizedLogLine, 1)
	options := RowOutputOptions{
		"outputPath":              tempDir,
		string(QueryTypeServices): "services",
	}

	logLineChan <- &NormalizedLogLine{
		Log:           []byte("test log"),
		Cluster:       "cluster1",
		Namespace:     "default",
		ContainerName: "container1",
		Timestamp:     time.Now(),
	}
	close(logLineChan)

	err = cliOutputFunc(t.Context(), logLineChan, QueryTypeServices, options)
	assert.NoError(t, err)

	// Verify file was created and contains log
	expectedFile := filepath.Join(tempDir, "services", "cluster1-default-container1.log")
	assert.FileExists(t, expectedFile)
	content, err := os.ReadFile(expectedFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "test log")

	// Error case - invalid path
	logLineChan = make(chan *NormalizedLogLine, 1)
	badOptions := RowOutputOptions{
		"outputPath":              "/nonexistent/path",
		string(QueryTypeServices): "services",
	}

	logLineChan <- &NormalizedLogLine{
		Log:           []byte("test log"),
		Cluster:       "cluster1",
		Namespace:     "default",
		ContainerName: "container1",
		Timestamp:     time.Now(),
	}
	close(logLineChan)

	err = cliOutputFunc(t.Context(), logLineChan, QueryTypeServices, badOptions)
	assert.Error(t, err)
}

func TestCliOutputFunc_KubernetesEvents(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-gatherer-events-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	err = os.MkdirAll(filepath.Join(tempDir, "cluster"), 0755)
	require.NoError(t, err)

	logLineChan := make(chan *NormalizedLogLine, 1)
	options := RowOutputOptions{
		"outputPath": tempDir,
	}

	go func() {
		logLineChan <- &NormalizedLogLine{
			Log:       []byte("event log line"),
			Cluster:   "test-cluster",
			Timestamp: time.Now(),
		}
		close(logLineChan)
	}()

	err = cliOutputFunc(t.Context(), logLineChan, QueryTypeKubernetesEvents, options)
	assert.NoError(t, err)

	// Kubernetes events use cluster-querytype.log naming
	expectedFile := filepath.Join(tempDir, "cluster", "test-cluster-kubernetes-events.log")
	assert.FileExists(t, expectedFile)
	content, err := os.ReadFile(expectedFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "event log line")
}

func TestCliOutputFunc_SystemdLogs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-gatherer-systemd-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	err = os.MkdirAll(filepath.Join(tempDir, "cluster"), 0755)
	require.NoError(t, err)

	logLineChan := make(chan *NormalizedLogLine, 1)
	options := RowOutputOptions{
		"outputPath": tempDir,
	}

	go func() {
		logLineChan <- &NormalizedLogLine{
			Log:       []byte("systemd log line"),
			Cluster:   "test-cluster",
			Timestamp: time.Now(),
		}
		close(logLineChan)
	}()

	err = cliOutputFunc(t.Context(), logLineChan, QueryTypeSystemdLogs, options)
	assert.NoError(t, err)

	expectedFile := filepath.Join(tempDir, "cluster", "test-cluster-systemd-logs.log")
	assert.FileExists(t, expectedFile)
	content, err := os.ReadFile(expectedFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "systemd log line")
}
