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
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// mockKustoClient is a mock implementation of the KustoClient interface
type mockKustoClient struct {
	executeQueryFunc func(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- any, rowType any, timeout time.Duration) (*kusto.QueryResult, error)
	closeFunc        func() error
}

func (m *mockKustoClient) ExecutePreconfiguredQuery(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- any, rowType any, timeout time.Duration) (*kusto.QueryResult, error) {
	if m.executeQueryFunc != nil {
		return m.executeQueryFunc(ctx, query, outputChannel, rowType, timeout)
	}
	return &kusto.QueryResult{}, nil
}

func (m *mockKustoClient) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// Test helpers
func createTestOptions(tempDir string) *MustGatherOptions {
	return &MustGatherOptions{
		ValidatedMustGatherOptions: &ValidatedMustGatherOptions{
			RawMustGatherOptions: &RawMustGatherOptions{
				OutputPath:   tempDir,
				QueryTimeout: 30 * time.Second,
			},
		},
	}
}

func createTestQueries(names ...string) []*kusto.ConfigurableQuery {
	queries := make([]*kusto.ConfigurableQuery, len(names))
	for i, name := range names {
		queries[i] = &kusto.ConfigurableQuery{
			Name:     name,
			Database: "test-db",
		}
	}
	return queries
}

func createMockClient(executeFunc func(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- any, rowType any, timeout time.Duration) (*kusto.QueryResult, error)) *mockKustoClient {
	return &mockKustoClient{executeQueryFunc: executeFunc}
}

func createTestLogRow(log string) *ContainerLogsRow {
	return &ContainerLogsRow{
		Log:           []byte(log),
		Cluster:       "test-cluster",
		Namespace:     "test-namespace",
		ContainerName: "test-container",
		Timestamp:     time.Now(),
	}
}

func createQueryResult(executionTime time.Duration, totalRows int, dataSize int) *kusto.QueryResult {
	return &kusto.QueryResult{
		Columns: []kusto.Column{
			{Name: "log", Type: "string"},
			{Name: "cluster", Type: "string"},
			{Name: "namespace", Type: "string"},
			{Name: "containerName", Type: "string"},
			{Name: "timestamp", Type: "datetime"},
		},
		QueryStats: kusto.QueryStats{
			ExecutionTime: executionTime,
			TotalRows:     totalRows,
			DataSize:      int64(dataSize),
		},
	}
}

func TestExecuteContainerLogsQueries(t *testing.T) {
	tempDir := t.TempDir()
	opts := createTestOptions(tempDir)

	tests := []struct {
		name        string
		queries     []*kusto.ConfigurableQuery
		setupClient func() *mockKustoClient
		expectError bool
		rowCount    int
		errorMsg    string
		checkFiles  []string
	}{
		{
			name:    "successful execution with multiple queries",
			queries: createTestQueries("test-query-1", "test-query-2"),
			setupClient: func() *mockKustoClient {
				return createMockClient(func(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- any, rowType any, timeout time.Duration) (*kusto.QueryResult, error) {
					outputChannel <- createTestLogRow("test log entry")
					return createQueryResult(100*time.Millisecond, 1, 100), nil
				})
			},
			rowCount:   2,
			checkFiles: []string{"test-query-1.json", "test-query-2.json"},
		},
		{
			name:    "handles query execution errors",
			queries: createTestQueries("failing-query", "successful-query"),
			setupClient: func() *mockKustoClient {
				return createMockClient(func(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- any, rowType any, timeout time.Duration) (*kusto.QueryResult, error) {
					if query.Name == "failing-query" {
						return nil, fmt.Errorf("query execution failed")
					}
					outputChannel <- createTestLogRow("success log entry")
					return createQueryResult(50*time.Millisecond, 1, 50), nil
				})
			},
			expectError: true,
			errorMsg:    "failed to execute queries: [failed to execute query: query execution failed]",
		},
		{
			name:    "handles file writing errors",
			queries: createTestQueries("test-query"),
			setupClient: func() *mockKustoClient {
				return createMockClient(func(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- any, rowType any, timeout time.Duration) (*kusto.QueryResult, error) {
					outputChannel <- createTestLogRow("test log")
					return createQueryResult(50*time.Millisecond, 1, 50), nil
				})
			},
			expectError: true,
			errorMsg:    "failed to execute queries: [failed to write query result to file: failed to create output file: open /invalid/path/that/does/not/exist/test-query.json: no such file or directory]",
		},
		{
			name:        "handles empty queries list",
			queries:     []*kusto.ConfigurableQuery{},
			setupClient: func() *mockKustoClient { return &mockKustoClient{} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputChannel := make(chan any, 10)

			mockClient := tt.setupClient()
			ctx := context.Background()

			// Use invalid path for file writing error test
			testOpts := opts
			if tt.name == "handles file writing errors" {
				testOpts = &MustGatherOptions{
					ValidatedMustGatherOptions: &ValidatedMustGatherOptions{
						RawMustGatherOptions: &RawMustGatherOptions{
							OutputPath:   "/invalid/path/that/does/not/exist",
							QueryTimeout: 30 * time.Second,
						},
					},
				}
			}

			err := executeContainerLogsQueries(ctx, mockClient, testOpts, tt.queries, outputChannel)
			close(outputChannel)

			if tt.expectError {
				assert.Error(t, err, tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}

			// Check output files for successful tests
			if !tt.expectError && len(tt.checkFiles) > 0 {
				for _, filename := range tt.checkFiles {
					filePath := filepath.Join(tempDir, filename)
					if _, err := os.Stat(filePath); os.IsNotExist(err) {
						assert.NoError(t, err)
					}
				}
				var actualOutput []any
				for row := range outputChannel {
					actualOutput = append(actualOutput, row)
				}
				assert.Len(t, actualOutput, tt.rowCount)
			}
		})
	}
}
