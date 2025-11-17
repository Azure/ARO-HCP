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

package bicep

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"

	"github.com/go-logr/logr"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// DetermineCLIPath tries to parse `az bicep version` output to find the path to the `bicep` CLI path, and falls back
// to looking at the system $PATH. This roughly mirrors what the `az` CLI does as well, without reading & parsing the
// `az` config files:
// https://github.com/Azure/azure-cli/blob/a55543015da9e2f554a6a09816794b5315e3ce8b/src/azure-cli/azure/cli/command_modules/resource/_bicep.py#L68-L80
func DetermineCLIPath(ctx context.Context) (string, error) {
	fromAzCLI, azErr := parseAzCliOutputForPath(ctx)
	if azErr != nil {
		fromSystem, systemErr := exec.LookPath("bicep")
		if systemErr != nil {
			return "", fmt.Errorf("failed to find bicep binary, system lookup failed with %w, `az` CLI parsing failed with %w", systemErr, azErr)
		}
		return fromSystem, nil
	}
	return fromAzCLI, nil
}

var bicepPathPattern = regexp.MustCompile(`cli\.azure\.cli\.command_modules\.resource\._bicep: Bicep CLI installation path: (.+)`)

// parseAzCliOutputForPath uses an ugly hack to parse the debug output of `az bicep version` to find the path to the bicep CLI.
func parseAzCliOutputForPath(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "az", "bicep", "version", "--debug").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to determine bicep CLI path: %s %w", string(output), err)
	}
	matches := bicepPathPattern.FindSubmatch(output)
	if len(matches) != 2 {
		return "", fmt.Errorf("failed to determine bicep CLI path: unexpected output: %s", string(output))
	}
	return string(matches[1]), nil
}

// StartJSONRPCServer starts a bicep JSON-RPC server on a free port and returns the client connected to it.
// The server will be automatically stopped when the context is cancelled.
func StartJSONRPCServer(ctx context.Context, logger logr.Logger, debug bool) (*LSPClient, error) {
	bicepCLIPath, err := DetermineCLIPath(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to determine bicep CLI path: %w", err)
	}
	logger.V(4).Info("got bicep CLI path", "path", bicepCLIPath)

	cmd := exec.CommandContext(ctx, bicepCLIPath, "jsonrpc", "--stdio")

	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()

	var input io.Writer = inputWriter
	var output io.Reader = outputReader
	if debug {
		input = io.MultiWriter(inputWriter, os.Stdout)
		output = io.TeeReader(outputReader, os.Stdout)
	}

	cmd.Stdin = inputReader
	cmd.Stdout = outputWriter
	rwc := &stdioReadWriteCloser{
		Reader: output,
		Writer: input,
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start bicep JSON-RPC server: %w", err)
	}
	logger.V(4).Info("started bicep JSON-RPC server", "pid", cmd.Process.Pid)

	go func() {
		if err := cmd.Wait(); err != nil {
			logger.Error(err, "bicep JSON-RPC server exited with error")
		}
	}()
	return NewLSPClient(ctx, rwc, debug), nil
}

type stdioReadWriteCloser struct {
	io.Reader
	io.Writer
}

func (s *stdioReadWriteCloser) Read(p []byte) (n int, err error) {
	return s.Reader.Read(p)
}

func (s *stdioReadWriteCloser) Write(p []byte) (n int, err error) {
	return s.Writer.Write(p)
}

func (s *stdioReadWriteCloser) Close() error {
	return nil
}

var _ io.ReadWriteCloser = (*stdioReadWriteCloser)(nil)

func NewLSPClient(ctx context.Context, rwc io.ReadWriteCloser, debug bool) *LSPClient {
	stream := jsonrpc2.NewStream(rwc)
	if debug {
		stream = protocol.LoggingStream(stream, os.Stdout)
	}
	client := &LSPClient{
		conn: jsonrpc2.NewConn(stream),
	}
	client.conn.Go(ctx, nil)
	return client
}

type LSPClient struct {
	conn jsonrpc2.Conn
}

type buildParamsParams struct {
	Path               string         `json:"path"`
	ParameterOverrides map[string]any `json:"parameterOverrides"`
}

type buildParamsResult struct {
	Success     bool                    `json:"success"`
	Template    string                  `json:"template"`
	Parameters  string                  `json:"parameters"`
	Diagnostics []buildParamsDiagnostic `json:"diagnostics"`
}

type buildParamsDiagnostic struct {
	Source  string `json:"source"`
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// BuildParams builds a .bicepparam file at `path` into an ARM template and parameters content.
func (c *LSPClient) BuildParams(ctx context.Context, path string) (string, string, error) {
	result := &buildParamsResult{}
	if err := protocol.Call(ctx, c.conn, "bicep/compileParams", buildParamsParams{Path: path, ParameterOverrides: make(map[string]any)}, result); err != nil {
		return "", "", fmt.Errorf("failed to call bicep/buildParams: %w", err)
	}
	if !result.Success {
		err := fmt.Errorf("failed to build params")
		for _, diagnostic := range result.Diagnostics {
			err = errors.Join(err, fmt.Errorf("source: '%s', level: '%s', code: '%s', message: '%s'", diagnostic.Source, diagnostic.Level, diagnostic.Code, diagnostic.Message))
		}
		return "", "", err
	}
	return result.Template, result.Parameters, nil
}
