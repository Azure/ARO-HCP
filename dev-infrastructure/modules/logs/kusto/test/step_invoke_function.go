// Copyright 2026 Microsoft Corporation
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

package kustotest

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// invokeFunctionStep calls a stored function and compares the result.
//
// Step directory contents:
//   - 00-key.json: {"database": "DatabaseName", "function": "FuncName", "args": ["arg1", "arg2"]}
//   - expected-result.json: array of expected row objects (compared column-by-column)
//   - expected-error.txt (optional): if present, the function invocation must fail containing this substring
//
// If neither expected-result.json nor expected-error.txt is present, the step
// only validates that the function invocation succeeds (smoke test).
type invokeFunctionStep struct {
	id            stepID
	database      string
	function      string
	args          []any
	expectedRows  []map[string]any
	hasExpected   bool
	expectedError string
}

type functionKey struct {
	Database string `json:"database"`
	Function string `json:"function"`
	Args     []any  `json:"args"`
}

func newInvokeFunctionStep(id stepID, stepDir fs.FS) (*invokeFunctionStep, error) {
	keyContent, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("missing 00-key.json: %w", err)
	}
	var key functionKey
	if err := json.Unmarshal(keyContent, &key); err != nil {
		return nil, fmt.Errorf("failed to parse 00-key.json: %w", err)
	}
	if len(key.Database) == 0 {
		return nil, fmt.Errorf("00-key.json must specify 'database'")
	}
	if len(key.Function) == 0 {
		return nil, fmt.Errorf("00-key.json must specify 'function'")
	}

	step := &invokeFunctionStep{
		id:       id,
		database: key.Database,
		function: key.Function,
		args:     key.Args,
	}

	if resultContent, err := fs.ReadFile(stepDir, "expected-result.json"); err == nil {
		if err := json.Unmarshal(resultContent, &step.expectedRows); err != nil {
			return nil, fmt.Errorf("failed to parse expected-result.json: %w", err)
		}
		step.hasExpected = true
	}

	if errContent, err := fs.ReadFile(stepDir, "expected-error.txt"); err == nil {
		step.expectedError = strings.TrimSpace(string(errContent))
	}

	return step, nil
}

func (s *invokeFunctionStep) StepID() stepID { return s.id }

func (s *invokeFunctionStep) Run(ctx context.Context, t *testing.T, clients map[string]*emulatorClient) {
	t.Helper()
	client, ok := clients[s.database]
	require.True(t, ok, "%s: database %q not found in client map", s.id, s.database)

	kqlExpr, err := buildFunctionCall(s.function, s.args)
	require.NoError(t, err, "failed to build function call for %q", s.function)
	debugf(t, "%s: [%s] %s", s.id, s.database, kqlExpr)

	ds, err := client.query(ctx, kqlExpr)
	if len(s.expectedError) != 0 {
		require.ErrorContains(t, err, s.expectedError,
			"expected function %q to fail with %q", s.function, s.expectedError)
		return
	}
	require.NoError(t, err, "function %q invocation failed", s.function)

	actualRows := datasetToRows(t, ds)

	if !s.hasExpected {
		debugf(t, "%s: returned %d rows (smoke test)", s.id, len(actualRows))
		return
	}

	compareRows(t, fmt.Sprintf("function %q", s.function), s.expectedRows, actualRows)

	debugf(t, "%s: %d rows matched", s.id, len(actualRows))
}

func buildFunctionCall(funcName string, args []any) (string, error) {
	if len(args) == 0 {
		return funcName + "()", nil
	}
	var parts []string
	for _, arg := range args {
		switch v := arg.(type) {
		case string:
			if isKQLExpression(v) {
				parts = append(parts, v)
			} else {
				s := strings.ReplaceAll(v, `\`, `\\`)
				s = strings.ReplaceAll(s, `"`, `\"`)
				parts = append(parts, fmt.Sprintf("\"%s\"", s))
			}
		case float64:
			if v == float64(int64(v)) {
				parts = append(parts, fmt.Sprintf("%d", int64(v)))
			} else {
				parts = append(parts, fmt.Sprintf("%v", v))
			}
		case bool:
			parts = append(parts, fmt.Sprintf("%v", v))
		default:
			return "", fmt.Errorf("unsupported argument type %T for function call", v)
		}
	}
	return fmt.Sprintf("%s(%s)", funcName, strings.Join(parts, ", ")), nil
}

func isKQLExpression(s string) bool {
	for _, prefix := range []string{
		"datetime(", "timespan(", "ago(", "now(", "dynamic(", "int(", "long(", "real(", "decimal(", "bool(", "guid(",
	} {
		if strings.HasPrefix(s, prefix) && strings.HasSuffix(s, ")") {
			return true
		}
	}
	return false
}
