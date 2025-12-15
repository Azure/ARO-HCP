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

	"github.com/Azure/azure-kusto-go/kusto/data/table"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// MockQueryClient is a mock implementation of QueryClientInterface for testing
type MockQueryClient struct {
	mock.Mock
}

func (m *MockQueryClient) ConcurrentQueries(ctx context.Context, queries []*kusto.ConfigurableQuery, outputChannel chan *table.Row) error {
	args := m.Called(ctx, queries, outputChannel)
	return args.Error(0)
}

func (m *MockQueryClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockQueryClient) ExecutePreconfiguredQuery(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan *table.Row) (*kusto.QueryResult, error) {
	args := m.Called(ctx, query, outputChannel)
	result := args.Get(0)
	if result == nil {
		return nil, args.Error(1)
	}
	return result.(*kusto.QueryResult), args.Error(1)
}

// MockOutputFunc is a mock output function for testing
func mockOutputFunc(logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
	for range logLineChan {
		// Consume all messages
	}
	return nil
}

func mockOutputFuncWithError(logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
	for range logLineChan {
		// Consume all messages
	}
	return errors.New("output function error")
}

func TestNewCliGatherer(t *testing.T) {
	mockQueryClient := &MockQueryClient{}
	outputPath := "/test/output"
	serviceLogsDir := "services"
	hcpLogsDir := "hcp"
	opts := GathererOptions{
		SubscriptionID: "test-sub",
		ResourceGroup:  "test-rg",
		Limit:          100,
	}

	gatherer := NewCliGatherer(mockQueryClient, outputPath, serviceLogsDir, hcpLogsDir, opts)

	assert.NotNil(t, gatherer)
	assert.Equal(t, mockQueryClient, gatherer.QueryClient)
	assert.Equal(t, opts, gatherer.opts)
	assert.NotNil(t, gatherer.outputFunc)
	assert.NotNil(t, gatherer.outputOptions)
	assert.Equal(t, outputPath, gatherer.outputOptions["outputPath"])
	assert.Equal(t, serviceLogsDir, gatherer.outputOptions[string(QueryTypeServices)])
	assert.Equal(t, hcpLogsDir, gatherer.outputOptions[string(QueryTypeHostedControlPlane)])
}

func TestGathererOptions_Defaults(t *testing.T) {
	opts := GathererOptions{}

	assert.Empty(t, opts.ClusterIds)
	assert.Empty(t, opts.SubscriptionID)
	assert.Empty(t, opts.ResourceGroup)
	assert.False(t, opts.SkipHostedControlPlaneLogs)
	assert.True(t, opts.TimestampMin.IsZero())
	assert.True(t, opts.TimestampMax.IsZero())
	assert.Equal(t, 0, opts.Limit)
}

func TestGatherer_executeClusterIdQuery(t *testing.T) {
	t.Run("successful execution", func(t *testing.T) {
		mockQueryClient := &MockQueryClient{}
		gatherer := &Gatherer{
			QueryClient: mockQueryClient,
		}

		ctx := context.Background()
		query := &kusto.ConfigurableQuery{Name: "test-cluster-id-query"}

		// Mock the ExecutePreconfiguredQuery to return cluster IDs
		mockQueryClient.On("ExecutePreconfiguredQuery", ctx, query, mock.AnythingOfType("chan *table.Row")).Run(func(args mock.Arguments) {
			// Simulate returning cluster IDs
			// Note: In a real test, you'd create actual table.Row objects
			// The real implementation will close the channel, so we don't close it here
		}).Return(&kusto.QueryResult{}, nil)

		clusterIds, err := gatherer.executeClusterIdQuery(ctx, query)

		assert.NoError(t, err)
		assert.NotNil(t, clusterIds)
		mockQueryClient.AssertExpectations(t)
	})

	t.Run("query execution error", func(t *testing.T) {
		mockQueryClient := &MockQueryClient{}
		gatherer := &Gatherer{
			QueryClient: mockQueryClient,
		}

		ctx := context.Background()
		query := &kusto.ConfigurableQuery{Name: "failing-query"}
		expectedError := errors.New("query execution failed")

		mockQueryClient.On("ExecutePreconfiguredQuery", ctx, query, mock.AnythingOfType("chan *table.Row")).Return((*kusto.QueryResult)(nil), expectedError)

		clusterIds, err := gatherer.executeClusterIdQuery(ctx, query)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute query")
		assert.Nil(t, clusterIds)
		mockQueryClient.AssertExpectations(t)
	})
}

func TestGatherer_queryAndWriteToFile(t *testing.T) {
	t.Run("successful execution", func(t *testing.T) {
		mockQueryClient := &MockQueryClient{}
		gatherer := &Gatherer{
			QueryClient:   mockQueryClient,
			outputFunc:    mockOutputFunc,
			outputOptions: RowOutputOptions{"outputPath": "/test"},
		}

		ctx := context.Background()
		queryType := QueryTypeServices
		queries := []*kusto.ConfigurableQuery{
			{Name: "test-query-1"},
			{Name: "test-query-2"},
		}

		// Mock ConcurrentQueries to simulate proper channel behavior
		mockQueryClient.On("ConcurrentQueries", ctx, queries, mock.AnythingOfType("chan *table.Row")).Run(func(args mock.Arguments) {
			// The ConcurrentQueries method should not close the channel - that's done by the caller
			// We just simulate that it completes successfully without sending data
		}).Return(nil)

		err := gatherer.queryAndWriteToFile(ctx, queryType, queries)

		assert.NoError(t, err)
		mockQueryClient.AssertExpectations(t)
	})

	t.Run("query execution error", func(t *testing.T) {
		mockQueryClient := &MockQueryClient{}
		gatherer := &Gatherer{
			QueryClient:   mockQueryClient,
			outputFunc:    mockOutputFunc,
			outputOptions: RowOutputOptions{"outputPath": "/test"},
		}

		ctx := context.Background()
		queryType := QueryTypeServices
		queries := []*kusto.ConfigurableQuery{{Name: "failing-query"}}
		expectedError := errors.New("query execution failed")

		mockQueryClient.On("ConcurrentQueries", ctx, queries, mock.AnythingOfType("chan *table.Row")).Return(expectedError)

		err := gatherer.queryAndWriteToFile(ctx, queryType, queries)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error during query execution")
		mockQueryClient.AssertExpectations(t)
	})

	t.Run("output function error", func(t *testing.T) {
		mockQueryClient := &MockQueryClient{}
		gatherer := &Gatherer{
			QueryClient:   mockQueryClient,
			outputFunc:    mockOutputFuncWithError,
			outputOptions: RowOutputOptions{"outputPath": "/test"},
		}

		ctx := context.Background()
		queryType := QueryTypeServices
		queries := []*kusto.ConfigurableQuery{{Name: "test-query"}}

		mockQueryClient.On("ConcurrentQueries", ctx, queries, mock.AnythingOfType("chan *table.Row")).Return(nil)

		err := gatherer.queryAndWriteToFile(ctx, queryType, queries)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to output data")
		mockQueryClient.AssertExpectations(t)
	})
}

func TestGatherer_convertRowsAndOutput(t *testing.T) {
	t.Run("successful conversion", func(t *testing.T) {
		gatherer := &Gatherer{
			outputFunc:    mockOutputFunc,
			outputOptions: RowOutputOptions{"outputPath": "/test"},
		}

		outputChannel := make(chan *table.Row)
		queryType := QueryTypeServices

		go func() {
			// Simulate sending table rows (in reality these would be actual table.Row objects)
			close(outputChannel) // For this test, we need to close the channel to avoid hanging
		}()

		err := gatherer.convertRowsAndOutput(outputChannel, queryType)

		assert.NoError(t, err)
	})

	t.Run("output function error", func(t *testing.T) {
		gatherer := &Gatherer{
			outputFunc:    mockOutputFuncWithError,
			outputOptions: RowOutputOptions{"outputPath": "/test"},
		}

		outputChannel := make(chan *table.Row)
		queryType := QueryTypeServices

		go func() {
			// Close the channel to simulate end of data
			close(outputChannel)
		}()

		err := gatherer.convertRowsAndOutput(outputChannel, queryType)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to output data")
	})
}

func TestGatherer_GatherLogs(t *testing.T) {
	t.Run("successful log gathering", func(t *testing.T) {
		mockQueryClient := &MockQueryClient{}
		gatherer := &Gatherer{
			QueryClient: mockQueryClient,
			opts: GathererOptions{
				SubscriptionID:             "test-sub",
				ResourceGroup:              "test-rg",
				SkipHostedControlPlaneLogs: false,
			},
			outputFunc:    mockOutputFunc,
			outputOptions: RowOutputOptions{"outputPath": "/test"},
		}

		ctx := context.Background()

		// Mock the cluster ID query
		mockQueryClient.On("ExecutePreconfiguredQuery", ctx, mock.AnythingOfType("*kusto.ConfigurableQuery"), mock.AnythingOfType("chan *table.Row")).Return(&kusto.QueryResult{}, nil).Once()

		// Mock the services queries
		mockQueryClient.On("ConcurrentQueries", ctx, mock.AnythingOfType("[]*kusto.ConfigurableQuery"), mock.AnythingOfType("chan *table.Row")).Return(nil).Once()

		// Mock the hosted control plane queries
		mockQueryClient.On("ConcurrentQueries", ctx, mock.AnythingOfType("[]*kusto.ConfigurableQuery"), mock.AnythingOfType("chan *table.Row")).Return(nil).Once()

		err := gatherer.GatherLogs(ctx)

		assert.NoError(t, err)
		// Note: With our mock setup, no actual cluster IDs are returned, so ClusterIds will be empty
		// In a real scenario, the ExecutePreconfiguredQuery would populate the channel with actual data
		assert.NotNil(t, gatherer.opts.ClusterIds) // Should be initialized (even if empty)
		mockQueryClient.AssertExpectations(t)
	})

	t.Run("skip hosted control plane logs", func(t *testing.T) {
		mockQueryClient := &MockQueryClient{}
		gatherer := &Gatherer{
			QueryClient: mockQueryClient,
			opts: GathererOptions{
				SubscriptionID:             "test-sub",
				ResourceGroup:              "test-rg",
				SkipHostedControlPlaneLogs: true,
			},
			outputFunc:    mockOutputFunc,
			outputOptions: RowOutputOptions{"outputPath": "/test"},
		}

		ctx := context.Background()

		// Mock the cluster ID query
		mockQueryClient.On("ExecutePreconfiguredQuery", ctx, mock.AnythingOfType("*kusto.ConfigurableQuery"), mock.AnythingOfType("chan *table.Row")).Return(&kusto.QueryResult{}, nil).Once()

		// Mock the services queries
		mockQueryClient.On("ConcurrentQueries", ctx, mock.AnythingOfType("[]*kusto.ConfigurableQuery"), mock.AnythingOfType("chan *table.Row")).Return(nil).Once()

		// No hosted control plane queries should be called

		err := gatherer.GatherLogs(ctx)

		assert.NoError(t, err)
		mockQueryClient.AssertExpectations(t)
	})

	t.Run("cluster ID query error", func(t *testing.T) {
		mockQueryClient := &MockQueryClient{}
		gatherer := &Gatherer{
			QueryClient: mockQueryClient,
			opts: GathererOptions{
				SubscriptionID: "test-sub",
				ResourceGroup:  "test-rg",
			},
			outputFunc:    mockOutputFunc,
			outputOptions: RowOutputOptions{"outputPath": "/test"},
		}

		ctx := context.Background()
		expectedError := errors.New("cluster ID query failed")

		mockQueryClient.On("ExecutePreconfiguredQuery", ctx, mock.AnythingOfType("*kusto.ConfigurableQuery"), mock.AnythingOfType("chan *table.Row")).Return((*kusto.QueryResult)(nil), expectedError)

		err := gatherer.GatherLogs(ctx)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute cluster id query")
		mockQueryClient.AssertExpectations(t)
	})

	t.Run("services query error", func(t *testing.T) {
		mockQueryClient := &MockQueryClient{}
		gatherer := &Gatherer{
			QueryClient: mockQueryClient,
			opts: GathererOptions{
				SubscriptionID: "test-sub",
				ResourceGroup:  "test-rg",
			},
			outputFunc:    mockOutputFunc,
			outputOptions: RowOutputOptions{"outputPath": "/test"},
		}

		ctx := context.Background()

		// Mock successful cluster ID query
		mockQueryClient.On("ExecutePreconfiguredQuery", ctx, mock.AnythingOfType("*kusto.ConfigurableQuery"), mock.AnythingOfType("chan *table.Row")).Return(&kusto.QueryResult{}, nil).Once()

		// Mock failing services query
		expectedError := errors.New("services query failed")
		mockQueryClient.On("ConcurrentQueries", ctx, mock.AnythingOfType("[]*kusto.ConfigurableQuery"), mock.AnythingOfType("chan *table.Row")).Return(expectedError)

		err := gatherer.GatherLogs(ctx)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute services query")
		mockQueryClient.AssertExpectations(t)
	})
}

func TestCliOutputFunc(t *testing.T) {
	t.Run("successful file output", func(t *testing.T) {
		// Create temporary directory for testing
		tempDir, err := os.MkdirTemp("", "test-gatherer-*")
		require.NoError(t, err)
		defer os.RemoveAll(tempDir)

		serviceDir := "services"
		err = os.MkdirAll(filepath.Join(tempDir, serviceDir), 0755)
		require.NoError(t, err)

		logLineChan := make(chan *NormalizedLogLine, 2)
		queryType := QueryTypeServices
		options := RowOutputOptions{
			"outputPath":              tempDir,
			string(QueryTypeServices): serviceDir,
		}

		// Send test log lines
		go func() {
			logLineChan <- &NormalizedLogLine{
				Log:           []byte("test log message 1"),
				Cluster:       "cluster1",
				Namespace:     "default",
				ContainerName: "container1",
				Timestamp:     time.Now(),
			}
			logLineChan <- &NormalizedLogLine{
				Log:           []byte("test log message 2"),
				Cluster:       "cluster1",
				Namespace:     "default",
				ContainerName: "container1",
				Timestamp:     time.Now(),
			}
			close(logLineChan)
		}()

		err = cliOutputFunc(logLineChan, queryType, options)

		assert.NoError(t, err)

		// Verify file was created
		expectedFile := filepath.Join(tempDir, serviceDir, "cluster1-default-container1.log")
		assert.FileExists(t, expectedFile)

		// Verify file content
		content, err := os.ReadFile(expectedFile)
		require.NoError(t, err)
		assert.Contains(t, string(content), "test log message 1")
		assert.Contains(t, string(content), "test log message 2")
	})

	t.Run("file creation error", func(t *testing.T) {
		logLineChan := make(chan *NormalizedLogLine, 1)
		queryType := QueryTypeServices
		options := RowOutputOptions{
			"outputPath":              "/nonexistent/path",
			string(QueryTypeServices): "services",
		}

		// Send test log line
		go func() {
			logLineChan <- &NormalizedLogLine{
				Log:           []byte("test log message"),
				Cluster:       "cluster1",
				Namespace:     "default",
				ContainerName: "container1",
				Timestamp:     time.Now(),
			}
			close(logLineChan)
		}()

		err := cliOutputFunc(logLineChan, queryType, options)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create output file")
	})
}

func TestNormalizedLogLine(t *testing.T) {
	now := time.Now()
	logLine := &NormalizedLogLine{
		Log:           []byte("test log message"),
		Cluster:       "test-cluster",
		Namespace:     "test-namespace",
		ContainerName: "test-container",
		Timestamp:     now,
	}

	assert.Equal(t, []byte("test log message"), logLine.Log)
	assert.Equal(t, "test-cluster", logLine.Cluster)
	assert.Equal(t, "test-namespace", logLine.Namespace)
	assert.Equal(t, "test-container", logLine.ContainerName)
	assert.Equal(t, now, logLine.Timestamp)
}

func TestRowOutputOptions(t *testing.T) {
	options := RowOutputOptions{
		"key1": "value1",
		"key2": 42,
		"key3": true,
	}

	assert.Equal(t, "value1", options["key1"])
	assert.Equal(t, 42, options["key2"])
	assert.Equal(t, true, options["key3"])
}
