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

	"github.com/Azure/azure-kusto-go/kusto/data/table"

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

func mockOutputFunc(logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
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
	customOutputFunc := func(logLineChan chan *NormalizedLogLine, queryType QueryType, options RowOutputOptions) error {
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
			QueryOptions: &QueryOptions{
				SubscriptionId:    "test-sub",
				ResourceGroupName: "test-rg",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
	}

	ctx := context.Background()

	// Success case
	mockQueryClient.On("ExecutePreconfiguredQuery", ctx, mock.AnythingOfType("*kusto.ConfigurableQuery"), mock.AnythingOfType("chan *table.Row")).Return(&kusto.QueryResult{}, nil).Once()
	mockQueryClient.On("ConcurrentQueries", ctx, mock.AnythingOfType("[]*kusto.ConfigurableQuery"), mock.AnythingOfType("chan *table.Row")).Return(nil).Twice()

	err := gatherer.GatherLogs(ctx)
	assert.NoError(t, err)

	// Error case
	mockQueryClient.On("ExecutePreconfiguredQuery", ctx, mock.AnythingOfType("*kusto.ConfigurableQuery"), mock.AnythingOfType("chan *table.Row")).Return(nil, errors.New("query failed"))

	err = gatherer.GatherLogs(ctx)
	assert.Error(t, err)

	mockQueryClient.AssertExpectations(t)
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

	go func() {
		logLineChan <- &NormalizedLogLine{
			Log:           []byte("test log"),
			Cluster:       "cluster1",
			Namespace:     "default",
			ContainerName: "container1",
			Timestamp:     time.Now(),
		}
		close(logLineChan)
	}()

	err = cliOutputFunc(logLineChan, QueryTypeServices, options)
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

	go func() {
		logLineChan <- &NormalizedLogLine{
			Log:           []byte("test log"),
			Cluster:       "cluster1",
			Namespace:     "default",
			ContainerName: "container1",
			Timestamp:     time.Now(),
		}
		close(logLineChan)
	}()

	err = cliOutputFunc(logLineChan, QueryTypeServices, badOptions)
	assert.Error(t, err)
}
