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

package datadumptogit

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type inputType int

const (
	inputTypeFile inputType = iota
	inputTypeDirectory
	inputTypeTarGz
	inputTypeCSV
)

type rawOptions struct {
	LogPath   string
	OutputDir string
}

func defaultOptions() *rawOptions {
	return &rawOptions{}
}

func (opts *rawOptions) Run(ctx context.Context) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	return completed.Run(ctx)
}

func bindOptions(opts *rawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.LogPath, "log", opts.LogPath, "Path to backend log file, must-gather directory, or must-gather.tar.gz")
	cmd.Flags().StringVar(&opts.OutputDir, "output", opts.OutputDir, "Path to output directory for git repo")

	if err := cmd.MarkFlagRequired("log"); err != nil {
		return fmt.Errorf("failed to mark flag %q as required: %w", "log", err)
	}
	if err := cmd.MarkFlagDirname("output"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a directory: %w", "output", err)
	}
	if err := cmd.MarkFlagRequired("output"); err != nil {
		return fmt.Errorf("failed to mark flag %q as required: %w", "output", err)
	}
	return nil
}

type validatedOptions struct {
	*rawOptions
	inputType inputType
}

type options struct {
	*validatedOptions
	logFiles []string // List of log files to process
	tempDir  string   // Temp directory for extracted files (if tar.gz)
}

func (opts *rawOptions) Validate(ctx context.Context) (*validatedOptions, error) {
	if opts.LogPath == "" {
		return nil, fmt.Errorf("log path is required")
	}
	if opts.OutputDir == "" {
		return nil, fmt.Errorf("output directory is required")
	}

	// Check that path exists
	info, err := os.Stat(opts.LogPath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("log path does not exist: %s", opts.LogPath)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to stat log path: %w", err)
	}

	// Determine input type
	var iType inputType
	lowerPath := strings.ToLower(opts.LogPath)
	if info.IsDir() {
		iType = inputTypeDirectory
	} else if strings.HasSuffix(lowerPath, ".tar.gz") || strings.HasSuffix(lowerPath, ".tgz") {
		iType = inputTypeTarGz
	} else if strings.HasSuffix(lowerPath, ".csv") {
		iType = inputTypeCSV
	} else {
		iType = inputTypeFile
	}

	return &validatedOptions{
		rawOptions: opts,
		inputType:  iType,
	}, nil
}

func (opts *validatedOptions) Complete(ctx context.Context) (*options, error) {
	// Convert output dir to absolute path
	absOutputDir, err := filepath.Abs(opts.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for output directory: %w", err)
	}
	opts.OutputDir = absOutputDir

	result := &options{
		validatedOptions: opts,
	}

	switch opts.inputType {
	case inputTypeFile, inputTypeCSV:
		// Single file - use as-is (CSV parsing handled in parseLogFile)
		result.logFiles = []string{opts.LogPath}

	case inputTypeDirectory:
		// Find backend log files in service/ directory
		logFiles, err := findBackendLogs(opts.LogPath)
		if err != nil {
			return nil, fmt.Errorf("failed to find backend logs: %w", err)
		}
		if len(logFiles) == 0 {
			return nil, fmt.Errorf("no backend log files found in %s", opts.LogPath)
		}
		result.logFiles = logFiles

	case inputTypeTarGz:
		// Extract tar.gz and find backend log files
		tempDir, err := os.MkdirTemp("", "datadump-to-git-*")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp directory: %w", err)
		}
		result.tempDir = tempDir

		if err := extractTarGz(opts.LogPath, tempDir); err != nil {
			os.RemoveAll(tempDir)
			return nil, fmt.Errorf("failed to extract tar.gz: %w", err)
		}

		logFiles, err := findBackendLogs(tempDir)
		if err != nil {
			os.RemoveAll(tempDir)
			return nil, fmt.Errorf("failed to find backend logs: %w", err)
		}
		if len(logFiles) == 0 {
			os.RemoveAll(tempDir)
			return nil, fmt.Errorf("no backend log files found in archive")
		}
		result.logFiles = logFiles
	}

	return result, nil
}

// findBackendLogs finds all backend log files in a directory
func findBackendLogs(dir string) ([]string, error) {
	var logFiles []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Look for files matching *backend*.log or *backend*.jsonl in service/ directories
		filename := filepath.Base(path)
		lowerFilename := strings.ToLower(filename)
		parentDir := filepath.Base(filepath.Dir(path))

		isBackendLog := parentDir == "service" &&
			strings.Contains(lowerFilename, "backend") &&
			(strings.HasSuffix(lowerFilename, ".log") || strings.HasSuffix(lowerFilename, ".jsonl"))
		if isBackendLog {
			logFiles = append(logFiles, path)
		}

		return nil
	})

	return logFiles, err
}

// extractTarGz extracts a tar.gz file to a directory
func extractTarGz(tarGzPath, destDir string) error {
	file, err := os.Open(tarGzPath)
	if err != nil {
		return fmt.Errorf("failed to open tar.gz: %w", err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		// Skip directories, we'll create them as needed
		if header.Typeflag == tar.TypeDir {
			continue
		}

		// Only extract files we care about (backend logs - .log or .jsonl)
		filename := filepath.Base(header.Name)
		lowerFilename := strings.ToLower(filename)
		isBackendLog := strings.Contains(lowerFilename, "backend") &&
			(strings.HasSuffix(lowerFilename, ".log") || strings.HasSuffix(lowerFilename, ".jsonl"))
		if !isBackendLog {
			continue
		}

		// Create target path
		targetPath := filepath.Join(destDir, header.Name)

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}

		// Create file
		outFile, err := os.Create(targetPath)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}

		if _, err := io.Copy(outFile, tarReader); err != nil {
			outFile.Close()
			return fmt.Errorf("failed to extract file: %w", err)
		}
		outFile.Close()
	}

	return nil
}
