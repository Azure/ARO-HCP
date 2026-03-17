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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	kustoerrors "github.com/Azure/azure-kusto-go/azkustodata/errors"
	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"
	"github.com/Azure/azure-kusto-go/azkustodata/types"
	"github.com/Azure/azure-kusto-go/azkustodata/value"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// MockQueryClient is a mock implementation of QueryClientInterface for testing
type MockQueryClient struct {
	mock.Mock
}

func (m *MockQueryClient) ConcurrentQueries(ctx context.Context, queries []kusto.Query, outputChannel chan<- kusto.TaggedRow) error {
	args := m.Called(ctx, queries, outputChannel)
	return args.Error(0)
}

func (m *MockQueryClient) ExecutePreconfiguredQuery(ctx context.Context, query kusto.Query, outputChannel chan<- kusto.TaggedRow) (*kusto.QueryResult, error) {
	args := m.Called(ctx, query, outputChannel)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*kusto.QueryResult), args.Error(1)
}

func (m *MockQueryClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

func mockOutputFunc(ctx context.Context, logLineChan chan *NormalizedLogLine, options RowOutputOptions) error {
	for range logLineChan {
		// Consume all messages
	}
	return nil
}

// makeLogMap is a helper to create *map[string]any for NormalizedLogLine.Log
func makeLogMap(pairs ...any) map[string]any {
	m := make(map[string]any)
	for i := 0; i < len(pairs)-1; i += 2 {
		m[pairs[i].(string)] = pairs[i+1]
	}
	return m
}

// makeTestRow creates a kusto.TaggedRow wrapping a real azkquery.Row for testing convertRows.
func makeTestRow(t *testing.T, colDefs []struct {
	name string
	typ  types.Column
}, vals value.Values) kusto.TaggedRow {
	t.Helper()
	columns := make([]azkquery.Column, len(colDefs))
	for i, cd := range colDefs {
		columns[i] = azkquery.NewColumn(i, cd.name, cd.typ)
	}
	ds := azkquery.NewBaseDataset(context.Background(), kustoerrors.OpUnknown, "PrimaryResult")
	bt := azkquery.NewBaseTable(ds, 0, "test-id", "PrimaryResult", "PrimaryResult", columns)
	return kusto.TaggedRow{Row: azkquery.NewRow(bt, 0, vals), QueryName: "test"}
}

func TestNewGatherer(t *testing.T) {
	mockQueryClient := &MockQueryClient{}
	opts := GathererOptions{
		QueryOptions: &kusto.QueryOptions{
			SubscriptionId:    "test-sub",
			ResourceGroupName: "test-rg",
		},
	}

	// Test CLI gatherer
	gatherer := NewCliGatherer(mockQueryClient, "/test/output", "services", "hcp", "custom", opts, false)
	assert.NotNil(t, gatherer)
	assert.Equal(t, mockQueryClient, gatherer.QueryClient)

	// Test custom gatherer
	customOutputFunc := func(ctx context.Context, logLineChan chan *NormalizedLogLine, options RowOutputOptions) error {
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
			QueryOptions: &kusto.QueryOptions{
				SubscriptionId:    "test-sub",
				ResourceGroupName: "test-rg",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
	}

	// Success case: cluster ID query + cluster names query + services + HCP + custom queries
	mockQueryClient.On("ExecutePreconfiguredQuery", mock.Anything, mock.Anything, mock.Anything).Return(&kusto.QueryResult{}, nil)
	// ConcurrentQueries: services + HCP + custom
	mockQueryClient.On("ConcurrentQueries", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	err := gatherer.GatherLogs(t.Context())
	assert.NoError(t, err)
}

func TestGatherer_GatherLogs_WithKubernetesEventsAndSystemdLogs(t *testing.T) {
	mockQueryClient := &MockQueryClient{}
	gatherer := &Gatherer{
		QueryClient: mockQueryClient,
		opts: GathererOptions{
			SkipKubernetesEventsLogs: false,
			CollectSystemdLogs:       true,
			QueryOptions: &kusto.QueryOptions{
				SubscriptionId:    "test-sub",
				ResourceGroupName: "test-rg",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
	}

	// ExecutePreconfiguredQuery for cluster IDs + cluster names
	mockQueryClient.On("ExecutePreconfiguredQuery", mock.Anything, mock.Anything, mock.Anything).Return(&kusto.QueryResult{}, nil)
	// ConcurrentQueries for services + HCP + kubernetes events + systemd logs
	mockQueryClient.On("ConcurrentQueries", mock.Anything, mock.Anything, mock.Anything).Return(nil)

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
			QueryOptions: &kusto.QueryOptions{
				SubscriptionId:    "test-sub",
				ResourceGroupName: "test-rg",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
	}

	// Cluster ID + cluster name queries
	mockQueryClient.On("ExecutePreconfiguredQuery", mock.Anything, mock.Anything, mock.Anything).Return(&kusto.QueryResult{}, nil)
	// Services + HCP + kubernetes events (no systemd)
	mockQueryClient.On("ConcurrentQueries", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	err := gatherer.GatherLogs(t.Context())
	assert.NoError(t, err)

	// Verify ConcurrentQueries was called 4 times (services + HCP + custom + kubernetes events, no systemd)
	mockQueryClient.AssertNumberOfCalls(t, "ConcurrentQueries", 4)
}

func TestGatherer_GatherInfraLogs(t *testing.T) {
	mockQueryClient := &MockQueryClient{}
	gatherer := &Gatherer{
		QueryClient: mockQueryClient,
		opts: GathererOptions{
			GatherInfraLogs: true,
			QueryOptions: &kusto.QueryOptions{
				InfraClusterName: "test-infra-cluster",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
		infraLogsOnly: true,
	}

	// 3x ConcurrentQueries: kubernetes events + systemd logs + services
	mockQueryClient.On("ConcurrentQueries", mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(3)

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
			QueryOptions: &kusto.QueryOptions{
				InfraClusterName: "test-infra-cluster",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
		infraLogsOnly: true,
	}

	// First ConcurrentQueries (kubernetes events) fails
	mockQueryClient.On("ConcurrentQueries", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("query failed")).Once()

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
			QueryOptions: &kusto.QueryOptions{
				SubscriptionId:    "test-sub",
				ResourceGroupName: "test-rg",
			},
		},
		outputFunc:    mockOutputFunc,
		outputOptions: RowOutputOptions{"outputPath": "/test"},
	}

	// Cancel context before the query can complete
	mockQueryClient.On("ExecutePreconfiguredQuery", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
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
		"outputPath":                    tempDir,
		string(kusto.QueryTypeServices): "services",
	}

	logLineChan <- &NormalizedLogLine{
		Log:           makeLogMap("message", "test log"),
		Cluster:       "cluster1",
		Namespace:     "default",
		ContainerName: "container1",
		Timestamp:     time.Now(),
		QueryType:     kusto.QueryTypeServices,
	}
	close(logLineChan)

	err = cliOutputFunc(t.Context(), logLineChan, options)
	assert.NoError(t, err)

	// Verify file was created and contains log
	expectedFile := filepath.Join(tempDir, "services", "cluster1-default-container1.jsonl")
	assert.FileExists(t, expectedFile)
	content, err := os.ReadFile(expectedFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "test log")

	// Error case - invalid path
	logLineChan = make(chan *NormalizedLogLine, 1)
	badOptions := RowOutputOptions{
		"outputPath":                    "/nonexistent/path",
		string(kusto.QueryTypeServices): "services",
	}

	logLineChan <- &NormalizedLogLine{
		Log:           makeLogMap("message", "test log"),
		Cluster:       "cluster1",
		Namespace:     "default",
		ContainerName: "container1",
		Timestamp:     time.Now(),
		QueryType:     kusto.QueryTypeServices,
	}
	close(logLineChan)

	err = cliOutputFunc(t.Context(), logLineChan, badOptions)
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
			Log:       makeLogMap("message", "event log line"),
			Cluster:   "test-cluster",
			Timestamp: time.Now(),
			QueryType: kusto.QueryTypeKubernetesEvents,
		}
		close(logLineChan)
	}()

	err = cliOutputFunc(t.Context(), logLineChan, options)
	assert.NoError(t, err)

	// Kubernetes events use cluster-querytype.jsonl naming
	expectedFile := filepath.Join(tempDir, "cluster", "test-cluster-kubernetes-events.jsonl")
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
			Log:       makeLogMap("message", "systemd log line"),
			Cluster:   "test-cluster",
			Timestamp: time.Now(),
			QueryType: kusto.QueryTypeSystemdLogs,
		}
		close(logLineChan)
	}()

	err = cliOutputFunc(t.Context(), logLineChan, options)
	assert.NoError(t, err)

	expectedFile := filepath.Join(tempDir, "cluster", "test-cluster-systemd-logs.jsonl")
	assert.FileExists(t, expectedFile)
	content, err := os.ReadFile(expectedFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "systemd log line")
}

func TestConvertRows_StringColumns(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"extra_field", types.String},
	}, value.Values{
		value.NewString("test-cluster"),
		value.NewString("kube-system"),
		value.NewString("apiserver"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		value.NewString("extra-value"),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	assert.Equal(t, "test-cluster", result.Cluster)
	assert.Equal(t, "kube-system", result.Namespace)
	assert.Equal(t, "apiserver", result.ContainerName)
	assert.Equal(t, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), result.Timestamp)
	require.NotNil(t, result.Log)
	assert.Equal(t, "extra-value", result.Log["extra_field"])
}

func TestConvertRows_DynamicLogColumn(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	logJSON := `{"level":"info","msg":"hello world","ts":"2025-01-01T00:00:00Z"}`
	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"log", types.Dynamic},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)),
		value.NewDynamic([]byte(logJSON)),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)
	logMap := result.Log["log"]
	require.NotNil(t, logMap)
	// Dynamic JSON log is unmarshalled to map[string]any
	asMap, ok := logMap.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "info", asMap["level"])
	assert.Equal(t, "hello world", asMap["msg"])
}

func TestConvertRows_DynamicLogColumn_NonJSON(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	// Dynamic column with non-JSON content falls back to String()
	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"log", types.Dynamic},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)),
		value.NewDynamic([]byte("not valid json")),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)
	// Falls back to String() representation
	logVal := result.Log["log"]
	require.NotNil(t, logVal)
	_, isString := logVal.(string)
	assert.True(t, isString, "non-JSON dynamic should fall back to string")
}

func TestConvertRows_IntColumn(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"status_code", types.Int},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		value.NewInt(200),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)
	// Int columns are stored via String() since they're not "log" dynamic columns
	assert.NotEmpty(t, result.Log["status_code"])
}

func TestConvertRows_LongColumn(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"bytes_received", types.Long},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		value.NewLong(1234567890),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)
	assert.Contains(t, result.Log["bytes_received"], "1234567890")
}

func TestConvertRows_RealColumn(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"cpu_usage", types.Real},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		value.NewReal(3.14159),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)
	assert.Contains(t, result.Log["cpu_usage"], "3.14159")
}

func TestConvertRows_BoolColumn(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"is_healthy", types.Bool},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		value.NewBool(true),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)
	assert.Contains(t, result.Log["is_healthy"], "true")
}

func TestConvertRows_TimespanColumn(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"duration", types.Timespan},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		value.NewTimespan(5 * time.Minute),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)
	assert.NotEmpty(t, result.Log["duration"])
}

func TestConvertRows_DateTimeExtraColumn(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	extraTime := time.Date(2025, 3, 15, 10, 30, 0, 0, time.UTC)
	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"created_at", types.DateTime},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		value.NewDateTime(extraTime),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)
	// Extra datetime columns go through String()
	assert.NotEmpty(t, result.Log["created_at"])
}

func TestConvertRows_MultipleExtraColumns(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	logJSON := `{"level":"error","msg":"something failed"}`
	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"log", types.Dynamic},
		{"severity", types.String},
		{"count", types.Long},
		{"is_error", types.Bool},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		value.NewDynamic([]byte(logJSON)),
		value.NewString("error"),
		value.NewLong(42),
		value.NewBool(true),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)

	// log column is unmarshalled as map
	logMap, ok := result.Log["log"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "error", logMap["level"])

	// Other extra columns are strings
	assert.Equal(t, "error", result.Log["severity"])
	assert.Contains(t, result.Log["count"], "42")
	assert.Contains(t, result.Log["is_error"], "true")
}

func TestConvertRows_NoExtraColumns(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	// Only known columns, no extra
	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	assert.Equal(t, "cluster-1", result.Cluster)
	// No extra columns means Log map is nil (empty map is not set)
	assert.Nil(t, result.Log)
}

func TestConvertRows_MultipleRows(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 3)
	outChan := make(chan *NormalizedLogLine, 3)

	for i, name := range []string{"cluster-a", "cluster-b", "cluster-c"} {
		row := makeTestRow(t, []struct {
			name string
			typ  types.Column
		}{
			{"cluster", types.String},
			{"namespace_name", types.String},
			{"container_name", types.String},
			{"timestamp", types.DateTime},
			{"message", types.String},
		}, value.Values{
			value.NewString(name),
			value.NewString("ns"),
			value.NewString("ctr"),
			value.NewDateTime(time.Date(2025, 1, 1, i, 0, 0, 0, time.UTC)),
			value.NewString("log-" + name),
		})
		rowChan <- row
	}
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	var results []*NormalizedLogLine
	for r := range outChan {
		results = append(results, r)
	}
	assert.Len(t, results, 3)
	assert.Equal(t, "cluster-a", results[0].Cluster)
	assert.Equal(t, "cluster-b", results[1].Cluster)
	assert.Equal(t, "cluster-c", results[2].Cluster)
}

func TestConvertRows_ContextCancellation(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow)
	outChan := make(chan *NormalizedLogLine)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately

	err := g.convertRows(ctx, rowChan, outChan)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestConvertRows_NullDynamic(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"log", types.Dynamic},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		value.NewNullDynamic(),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)
	// Null dynamic falls through to String() representation
	_, exists := result.Log["log"]
	assert.True(t, exists)
}

func TestConvertRows_DynamicLogArray(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	// Dynamic column with JSON array (not object) falls back to String()
	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"log", types.Dynamic},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		value.NewDynamic([]byte(`["a","b","c"]`)),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)
	// JSON array can't unmarshal to map[string]any, falls back to String()
	logVal := result.Log["log"]
	require.NotNil(t, logVal)
	_, isString := logVal.(string)
	assert.True(t, isString, "JSON array should fall back to string representation")
}

func TestConvertRows_NonLogDynamicColumn(t *testing.T) {
	g := &Gatherer{}
	rowChan := make(chan kusto.TaggedRow, 1)
	outChan := make(chan *NormalizedLogLine, 1)

	// A dynamic column that is NOT named "log" should go through String() path
	row := makeTestRow(t, []struct {
		name string
		typ  types.Column
	}{
		{"cluster", types.String},
		{"namespace_name", types.String},
		{"container_name", types.String},
		{"timestamp", types.DateTime},
		{"metadata", types.Dynamic},
	}, value.Values{
		value.NewString("cluster-1"),
		value.NewString("ns-1"),
		value.NewString("container-1"),
		value.NewDateTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		value.NewDynamic([]byte(`{"key":"val"}`)),
	})

	rowChan <- row
	close(rowChan)

	err := g.convertRows(t.Context(), rowChan, outChan)
	require.NoError(t, err)
	close(outChan)

	result := <-outChan
	require.NotNil(t, result)
	require.NotNil(t, result.Log)
	// Non-"log" dynamic columns go through String(), not JSON unmarshal
	metaVal := result.Log["metadata"]
	require.NotNil(t, metaVal)
	_, isString := metaVal.(string)
	assert.True(t, isString, "non-log dynamic column should use String() representation")
}

func TestCliOutputFunc_JSONLFormat(t *testing.T) {
	tempDir := t.TempDir()
	err := os.MkdirAll(filepath.Join(tempDir, "services"), 0755)
	require.NoError(t, err)

	logLineChan := make(chan *NormalizedLogLine, 2)
	options := RowOutputOptions{
		"outputPath":                    tempDir,
		string(kusto.QueryTypeServices): "services",
	}

	// Send two log lines to verify JSONL format (one JSON object per line)
	logLineChan <- &NormalizedLogLine{
		Log:           makeLogMap("message", "first line", "level", "info"),
		Cluster:       "cluster1",
		Namespace:     "default",
		ContainerName: "container1",
		Timestamp:     time.Now(),
		QueryType:     kusto.QueryTypeServices,
	}
	logLineChan <- &NormalizedLogLine{
		Log:           makeLogMap("message", "second line", "level", "error"),
		Cluster:       "cluster1",
		Namespace:     "default",
		ContainerName: "container1",
		Timestamp:     time.Now(),
		QueryType:     kusto.QueryTypeServices,
	}
	close(logLineChan)

	err = cliOutputFunc(t.Context(), logLineChan, options)
	assert.NoError(t, err)

	expectedFile := filepath.Join(tempDir, "services", "cluster1-default-container1.jsonl")
	content, err := os.ReadFile(expectedFile)
	require.NoError(t, err)

	// Each line should be valid JSON
	lines := splitNonEmpty(string(content))
	require.Len(t, lines, 2)

	var line1, line2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &line1))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &line2))
	assert.Equal(t, "first line", line1["message"])
	assert.Equal(t, "second line", line2["message"])
}

// splitNonEmpty splits a string by newlines and returns non-empty lines.
func splitNonEmpty(s string) []string {
	var result []string
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}
