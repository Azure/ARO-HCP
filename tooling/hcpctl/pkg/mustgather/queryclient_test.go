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

// // MockKustoClient is a mock implementation of kusto.KustoClient for testing
// type MockKustoClient struct {
// 	mock.Mock
// }

// func (m *MockKustoClient) ExecutePreconfiguredQuery(ctx context.Context, query kusto.Query, outputChannel chan<- azkquery.Row) (*kusto.QueryResult, error) {
// 	args := m.Called(ctx, query, outputChannel)
// 	return args.Get(0).(*kusto.QueryResult), args.Error(1)
// }

// func (m *MockKustoClient) Close() error {
// 	args := m.Called()
// 	return args.Error(0)
// }

// // MockFileWriter is a mock implementation of FileWriter for testing
// type MockFileWriter struct {
// 	mock.Mock
// }

// type MockColumn struct {
// 	name  string
// 	ctype types.Column
// 	index int
// }

// func (m *MockColumn) Name() string {
// 	return m.name
// }

// func (m *MockColumn) Type() types.Column {
// 	return m.ctype
// }

// func (m *MockColumn) Index() int {
// 	return m.index
// }

// func (m *MockFileWriter) WriteFile(outputPath, fileName string, data any) error {
// 	args := m.Called(outputPath, fileName, data)
// 	return args.Error(0)
// }

// // mockQuery implements kusto.Query for testing, replacing the deleted ConfigurableQuery.
// type mockQuery struct {
// 	name     string
// 	database string
// }

// func (q *mockQuery) GetName() string        { return q.name }
// func (q *mockQuery) GetDatabase() string     { return q.database }
// func (q *mockQuery) GetQuery() *kql.Builder  { return kql.New("") }
// func (q *mockQuery) IsUnlimited() bool       { return false }

// func TestNewQueryClient(t *testing.T) {
// 	mockClient := &MockKustoClient{}
// 	timeout := 30 * time.Second
// 	outputPath := "/test/output"

// 	queryClient := NewQueryClient(mockClient, timeout, outputPath)

// 	assert.NotNil(t, queryClient)
// 	assert.Equal(t, mockClient, queryClient.Client)
// 	assert.Equal(t, timeout, queryClient.QueryTimeout)
// 	assert.Equal(t, outputPath, queryClient.OutputPath)
// 	assert.IsType(t, &JsonEncoderWriter{}, queryClient.FileWriter)
// }

// func TestQueryClient_Close(t *testing.T) {
// 	t.Run("successful close", func(t *testing.T) {
// 		mockClient := &MockKustoClient{}
// 		mockClient.On("Close").Return(nil)

// 		queryClient := &QueryClient{Client: mockClient}
// 		err := queryClient.Close()

// 		assert.NoError(t, err)
// 		mockClient.AssertExpectations(t)
// 	})

// 	t.Run("close with error", func(t *testing.T) {
// 		expectedError := errors.New("close error")
// 		mockClient := &MockKustoClient{}
// 		mockClient.On("Close").Return(expectedError)

// 		queryClient := &QueryClient{Client: mockClient}
// 		err := queryClient.Close()

// 		assert.Error(t, err)
// 		assert.Equal(t, expectedError, err)
// 		mockClient.AssertExpectations(t)
// 	})
// }

// func TestQueryClient_ConcurrentQueries_Success(t *testing.T) {
// 	mockClient := &MockKustoClient{}
// 	mockFileWriter := &MockFileWriter{}

// 	ctx := context.Background()
// 	outputChannel := make(chan azkquery.Row, 10)
// 	defer close(outputChannel)

// 	query1 := &mockQuery{name: "query1"}
// 	query2 := &mockQuery{name: "query2"}
// 	queries := []kusto.Query{query1, query2}

// 	result1 := &kusto.QueryResult{
// 		Columns:    azkquery.Columns{&MockColumn{name: "col1", ctype: "string", index: 0}},
// 		QueryStats: kusto.QueryStats{ExecutionTime: 100 * time.Millisecond, TotalRows: 10, DataSize: 1024},
// 	}
// 	result2 := &kusto.QueryResult{
// 		Columns:    azkquery.Columns{&MockColumn{name: "col2", ctype: "string", index: 0}},
// 		QueryStats: kusto.QueryStats{ExecutionTime: 200 * time.Millisecond, TotalRows: 20, DataSize: 2048},
// 	}

// 	mockClient.On("ExecutePreconfiguredQuery", mock.Anything, query1, mock.Anything).Return(result1, nil)
// 	mockClient.On("ExecutePreconfiguredQuery", mock.Anything, query2, mock.Anything).Return(result2, nil)

// 	mockFileWriter.On("WriteFile", "/test/output", "query1.json", result1).Return(nil)
// 	mockFileWriter.On("WriteFile", "/test/output", "query2.json", result2).Return(nil)

// 	queryClient := &QueryClient{
// 		Client:     mockClient,
// 		OutputPath: "/test/output",
// 		FileWriter: mockFileWriter,
// 	}

// 	err := queryClient.ConcurrentQueries(ctx, queries, outputChannel)

// 	assert.NoError(t, err)
// 	mockClient.AssertExpectations(t)
// 	mockFileWriter.AssertExpectations(t)
// }

// func TestQueryClient_ConcurrentQueries_QueryExecutionError(t *testing.T) {
// 	mockClient := &MockKustoClient{}
// 	mockFileWriter := &MockFileWriter{}

// 	ctx := context.Background()
// 	outputChannel := make(chan azkquery.Row, 10)
// 	defer close(outputChannel)

// 	query := &mockQuery{name: "failing_query", database: "test"}
// 	queries := []kusto.Query{query}

// 	expectedError := errors.New("query execution failed")
// 	mockClient.On("ExecutePreconfiguredQuery", mock.Anything, query, mock.Anything).Return((*kusto.QueryResult)(nil), expectedError)

// 	queryClient := &QueryClient{
// 		Client:     mockClient,
// 		OutputPath: "/test/output",
// 		FileWriter: mockFileWriter,
// 	}

// 	err := queryClient.ConcurrentQueries(ctx, queries, outputChannel)

// 	assert.Error(t, err)
// 	assert.Contains(t, err.Error(), "failed to execute query")
// 	mockClient.AssertExpectations(t)
// 	mockFileWriter.AssertExpectations(t)
// }

// func TestQueryClient_ConcurrentQueries_FileWriteError(t *testing.T) {
// 	mockClient := &MockKustoClient{}
// 	mockFileWriter := &MockFileWriter{}

// 	ctx := context.Background()
// 	outputChannel := make(chan azkquery.Row, 10)
// 	defer close(outputChannel)

// 	query := &mockQuery{name: "query_with_write_error"}
// 	queries := []kusto.Query{query}

// 	result := &kusto.QueryResult{
// 		Columns:    azkquery.Columns{&MockColumn{name: "col1", ctype: "string", index: 0}},
// 		QueryStats: kusto.QueryStats{ExecutionTime: 100 * time.Millisecond, TotalRows: 10, DataSize: 1024},
// 	}

// 	expectedWriteError := errors.New("file write failed")
// 	mockClient.On("ExecutePreconfiguredQuery", mock.Anything, query, mock.Anything).Return(result, nil)
// 	mockFileWriter.On("WriteFile", "/test/output", "query_with_write_error.json", result).Return(expectedWriteError)

// 	queryClient := &QueryClient{
// 		Client:     mockClient,
// 		OutputPath: "/test/output",
// 		FileWriter: mockFileWriter,
// 	}

// 	err := queryClient.ConcurrentQueries(ctx, queries, outputChannel)

// 	assert.Error(t, err)
// 	assert.Contains(t, err.Error(), "failed to write query result to file")
// 	mockClient.AssertExpectations(t)
// 	mockFileWriter.AssertExpectations(t)
// }

// func TestQueryClient_ConcurrentQueries_EmptyQueries(t *testing.T) {
// 	mockClient := &MockKustoClient{}
// 	mockFileWriter := &MockFileWriter{}

// 	ctx := context.Background()
// 	outputChannel := make(chan azkquery.Row, 10)
// 	defer close(outputChannel)

// 	queries := []kusto.Query{}

// 	queryClient := &QueryClient{
// 		Client:     mockClient,
// 		OutputPath: "/test/output",
// 		FileWriter: mockFileWriter,
// 	}

// 	err := queryClient.ConcurrentQueries(ctx, queries, outputChannel)

// 	assert.NoError(t, err)
// 	mockClient.AssertExpectations(t)
// 	mockFileWriter.AssertExpectations(t)
// }

// func TestQueryClient_ConcurrentQueries_Concurrency(t *testing.T) {
// 	mockClient := &MockKustoClient{}
// 	mockFileWriter := &MockFileWriter{}

// 	ctx := context.Background()
// 	outputChannel := make(chan azkquery.Row, 10)
// 	defer close(outputChannel)

// 	numQueries := 5
// 	queries := make([]kusto.Query, numQueries)
// 	results := make([]*kusto.QueryResult, numQueries)

// 	for i := 0; i < numQueries; i++ {
// 		queries[i] = &mockQuery{name: fmt.Sprintf("query%d", i)}
// 		results[i] = &kusto.QueryResult{
// 			Columns:    azkquery.Columns{&MockColumn{name: fmt.Sprintf("col%d", i), ctype: "string", index: 0}},
// 			QueryStats: kusto.QueryStats{ExecutionTime: time.Duration(i*100) * time.Millisecond, TotalRows: i * 10, DataSize: int64(i * 1024)},
// 		}
// 	}

// 	var mu sync.Mutex
// 	executionTimes := make(map[string]time.Time)

// 	for i := 0; i < numQueries; i++ {
// 		query := queries[i]
// 		result := results[i]

// 		mockClient.On("ExecutePreconfiguredQuery", mock.Anything, query, mock.Anything).Run(func(args mock.Arguments) {
// 			mu.Lock()
// 			executionTimes[query.GetName()] = time.Now()
// 			mu.Unlock()
// 			time.Sleep(10 * time.Millisecond)
// 		}).Return(result, nil)

// 		mockFileWriter.On("WriteFile", "/test/output", fmt.Sprintf("%s.json", query.GetName()), result).Return(nil)
// 	}

// 	queryClient := &QueryClient{
// 		Client:     mockClient,
// 		OutputPath: "/test/output",
// 		FileWriter: mockFileWriter,
// 	}

// 	start := time.Now()
// 	err := queryClient.ConcurrentQueries(ctx, queries, outputChannel)
// 	duration := time.Since(start)

// 	assert.NoError(t, err)

// 	// Verify that execution was concurrent (should take much less time than sequential)
// 	// Sequential execution would take at least numQueries * 10ms = 50ms
// 	assert.Less(t, duration, 40*time.Millisecond, "Execution should be concurrent")

// 	assert.Len(t, executionTimes, numQueries)

// 	mockClient.AssertExpectations(t)
// 	mockFileWriter.AssertExpectations(t)
// }
