package mustgather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/azure-kusto-go/kusto/data/table"
	"github.com/go-logr/logr"
)

// FileWriter provides an interface for writing files to support testing
type FileWriter interface {
	WriteFile(outputPath, fileName string, data any) error
}

// DefaultFileWriter implements FileWriter using the standard file system
type DefaultFileWriter struct{}

func (d *DefaultFileWriter) WriteFile(outputPath, fileName string, data any) error {
	file, err := os.Create(path.Join(outputPath, fileName))
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(data)
}

type QueryClient struct {
	Client       kusto.KustoClient
	QueryTimeout time.Duration
	OutputPath   string
	FileWriter   FileWriter
}

// NewQueryClient creates a new QueryClient with default dependencies
func NewQueryClient(client kusto.KustoClient, queryTimeout time.Duration, outputPath string) *QueryClient {
	return &QueryClient{
		Client:       client,
		QueryTimeout: queryTimeout,
		OutputPath:   outputPath,
		FileWriter:   &DefaultFileWriter{},
	}
}

func (q *QueryClient) ConcurrentQueries(ctx context.Context, queries []*kusto.ConfigurableQuery, outputChannel chan *table.Row) error {
	logger := logr.FromContextOrDiscard(ctx)
	wg := sync.WaitGroup{}
	wg.Add(len(queries))

	errorCh := make(chan error, len(queries))

	for i, query := range queries {
		go func(query *kusto.ConfigurableQuery, queryIndex int) {
			defer wg.Done()
			result, err := q.Client.ExecutePreconfiguredQuery(ctx, query, outputChannel)
			if err != nil {
				logger.Error(err, "Query failed", "name", query.Name)
				errorCh <- fmt.Errorf("failed to execute query: %w", err)
				return
			}
			err = q.FileWriter.WriteFile(q.OutputPath, fmt.Sprintf("%s.json", query.Name), result)
			if err != nil {
				errorCh <- fmt.Errorf("failed to write query result to file: %w", err)
			}
		}(query, i)
	}

	wg.Wait()
	close(errorCh)

	if allErrors := errors.Join(<-errorCh); allErrors != nil {
		return fmt.Errorf("failed to execute queries: %v", allErrors)
	}

	return nil
}

func (q *QueryClient) Close() error {
	return q.Client.Close()
}

