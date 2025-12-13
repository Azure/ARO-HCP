// // Copyright 2025 Microsoft Corporation
// //
// // Licensed under the Apache License, Version 2.0 (the "License");
// // you may not use this file except in compliance with the License.
// // You may obtain a copy of the License at
// //
// //     http://www.apache.org/licenses/LICENSE-2.0
// //
// // Unless required by applicable law or agreed to in writing, software
// // distributed under the License is distributed on an "AS IS" BASIS,
// // WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// // See the License for the specific language governing permissions and
// // limitations under the License.

package mustgather

// import (
// 	"context"
// 	"fmt"
// 	"os"
// 	"path/filepath"
// 	"reflect"
// 	"testing"
// 	"time"

// 	"github.com/stretchr/testify/assert"

// 	"github.com/Azure/azure-kusto-go/kusto/data/table"
// 	"github.com/Azure/azure-kusto-go/kusto/data/value"

// 	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
// )

// // mockKustoClient is a mock implementation of the KustoClient interface
// type mockKustoClient struct {
// 	executeQueryFunc func(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- *table.Row, timeout time.Duration) (*kusto.QueryResult, error)
// 	closeFunc        func() error
// }

// func (m *mockKustoClient) ExecutePreconfiguredQuery(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- *table.Row, timeout time.Duration) (*kusto.QueryResult, error) {
// 	if m.executeQueryFunc != nil {
// 		return m.executeQueryFunc(ctx, query, outputChannel, timeout)
// 	}
// 	return &kusto.QueryResult{}, nil
// }

// func (m *mockKustoClient) Close() error {
// 	if m.closeFunc != nil {
// 		return m.closeFunc()
// 	}
// 	return nil
// }

// // Test helpers

// func createTestQueries(names ...string) []*kusto.ConfigurableQuery {
// 	queries := make([]*kusto.ConfigurableQuery, len(names))
// 	for i, name := range names {
// 		queries[i] = &kusto.ConfigurableQuery{
// 			Name:     name,
// 			Database: "test-db",
// 		}
// 	}
// 	return queries
// }

// func createMockClient(tmpDir string, executeFunc func(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- *table.Row, timeout time.Duration) (*kusto.QueryResult, error)) *QueryClient {
// 	return &QueryClient{
// 		Client: &mockKustoClient{
// 			executeQueryFunc: executeFunc,
// 			closeFunc:        func() error { return nil },
// 		},
// 		OutputPath:   tmpDir,
// 		QueryTimeout: 0 * time.Second,
// 	}
// }

// type TestLogRow struct {
// 	value.Kusto
// 	val string
// }

// func (t *TestLogRow) String() string {
// 	return t.val
// }
// func (t *TestLogRow) Convert(v reflect.Value) error {
// 	v.SetString(t.val)
// 	return nil
// }

// func createTestLogRow(log string) *table.Row {
// 	return &table.Row{
// 		Values: value.Values{&TestLogRow{val: log}},
// 	}
// }

// func createQueryResult(executionTime time.Duration, totalRows int, dataSize int) *kusto.QueryResult {
// 	return &kusto.QueryResult{
// 		Columns: []kusto.Column{
// 			{Name: "log", Type: "string"},
// 			{Name: "cluster", Type: "string"},
// 			{Name: "namespace", Type: "string"},
// 			{Name: "containerName", Type: "string"},
// 			{Name: "timestamp", Type: "datetime"},
// 		},
// 		QueryStats: kusto.QueryStats{
// 			ExecutionTime: executionTime,
// 			TotalRows:     totalRows,
// 			DataSize:      int64(dataSize),
// 		},
// 	}
// }

// func TestExecuteContainerLogsQueries(t *testing.T) {
// 	tempDir := t.TempDir()

// 	tests := []struct {
// 		name        string
// 		queries     []*kusto.ConfigurableQuery
// 		setupClient func() *QueryClient
// 		expectError bool
// 		rowCount    int
// 		errorMsg    string
// 		checkFiles  []string
// 	}{
// 		{
// 			name:    "successful execution with multiple queries",
// 			queries: createTestQueries("test-query-1", "test-query-2"),
// 			setupClient: func() *QueryClient {
// 				return createMockClient(tempDir, func(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- *table.Row, timeout time.Duration) (*kusto.QueryResult, error) {
// 					outputChannel <- createTestLogRow("test log entry")
// 					return createQueryResult(100*time.Millisecond, 1, 100), nil
// 				})
// 			},
// 			rowCount:   2,
// 			checkFiles: []string{"test-query-1.json", "test-query-2.json"},
// 		},
// 		{
// 			name:    "handles query execution errors",
// 			queries: createTestQueries("failing-query", "successful-query"),
// 			setupClient: func() *QueryClient {
// 				return createMockClient(tempDir, func(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- *table.Row, timeout time.Duration) (*kusto.QueryResult, error) {
// 					if query.Name == "failing-query" {
// 						return nil, fmt.Errorf("query execution failed")
// 					}
// 					outputChannel <- createTestLogRow("success log entry")
// 					return createQueryResult(50*time.Millisecond, 1, 50), nil
// 				})
// 			},
// 			expectError: true,
// 			errorMsg:    "failed to execute queries: [failed to execute query: query execution failed]",
// 		},
// 		{
// 			name:    "handles file writing errors",
// 			queries: createTestQueries("test-query"),
// 			setupClient: func() *QueryClient {
// 				return createMockClient("/invalid/path/that/does/not/exist", func(ctx context.Context, query *kusto.ConfigurableQuery, outputChannel chan<- *table.Row, timeout time.Duration) (*kusto.QueryResult, error) {
// 					outputChannel <- createTestLogRow("test log")
// 					return createQueryResult(50*time.Millisecond, 1, 50), nil
// 				})
// 			},
// 			expectError: true,
// 			errorMsg:    "failed to execute queries: [failed to write query result to file: failed to create output file: open /invalid/path/that/does/not/exist/test-query.json: no such file or directory]",
// 		},
// 		{
// 			name:        "handles empty queries list",
// 			queries:     []*kusto.ConfigurableQuery{},
// 			setupClient: func() *QueryClient { return &QueryClient{Client: &mockKustoClient{}} },
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			outputChannel := make(chan *table.Row, 10)

// 			queryClient := tt.setupClient()
// 			ctx := context.Background()

// 			// Use invalid path for file writing error test

// 			err := queryClient.concurrentQueries(ctx, tt.queries, outputChannel)
// 			close(outputChannel)

// 			if tt.expectError {
// 				assert.Error(t, err, tt.errorMsg)
// 			} else {
// 				assert.NoError(t, err)
// 			}

// 			// Check output files for successful tests
// 			if !tt.expectError && len(tt.checkFiles) > 0 {
// 				for _, filename := range tt.checkFiles {
// 					filePath := filepath.Join(tempDir, filename)
// 					if _, err := os.Stat(filePath); os.IsNotExist(err) {
// 						assert.NoError(t, err)
// 					}
// 				}
// 				var actualOutput []any
// 				for row := range outputChannel {
// 					actualOutput = append(actualOutput, row)
// 				}
// 				assert.Len(t, actualOutput, tt.rowCount)
// 			}
// 		})
// 	}
// }

// func TestWriteNormalizedLogsToFile(t *testing.T) {
// 	tempDir := t.TempDir()

// 	err := os.MkdirAll(filepath.Join(tempDir, ServicesLogDirectory), 0755)
// 	assert.NoError(t, err)

// 	tests := []struct {
// 		name          string
// 		callCount     int
// 		castFunction  func(input *table.Row) (*NormalizedLogLine, error)
// 		expectedFiles []string
// 	}{
// 		{
// 			name: "successful",
// 			castFunction: func(input *table.Row) (*NormalizedLogLine, error) {
// 				return &NormalizedLogLine{
// 					Log:           []byte("test log entry"),
// 					Cluster:       "cluster",
// 					Namespace:     "namespace",
// 					ContainerName: "container",
// 					Timestamp:     time.Now(),
// 				}, nil
// 			},
// 			expectedFiles: []string{"cluster-namespace-container.log"},
// 		},
// 		{
// 			name: "successful with multiple logs",
// 			castFunction: func(input *table.Row) (*NormalizedLogLine, error) {
// 				namespace := "ns"
// 				if input.Values[0].String() == "other log entry" {
// 					namespace = "ns2"
// 				}
// 				return &NormalizedLogLine{
// 					Log:           []byte(input.Values[0].String()),
// 					Cluster:       "cluster",
// 					Namespace:     namespace,
// 					ContainerName: "container",
// 					Timestamp:     time.Now(),
// 				}, nil
// 			},
// 			expectedFiles: []string{"cluster-ns-container.log", "cluster-ns2-container.log"},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			outputChannel := make(chan *table.Row, 10)
// 			outputChannel <- createTestLogRow("test log entry")
// 			outputChannel <- createTestLogRow("other log entry")
// 			close(outputChannel)
// 			err := writeNormalizedLogsToFile(outputChannel, tt.castFunction, tempDir, ServicesLogDirectory)
// 			assert.NoError(t, err)
// 			for _, filename := range tt.expectedFiles {
// 				assert.FileExists(t, filepath.Join(tempDir, ServicesLogDirectory, filename))
// 			}
// 		})
// 	}
// }
