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

package framework

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

func TestIsRetryableBootDiagnosticsError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "non-Azure error",
			err:      fmt.Errorf("some random error"),
			expected: false,
		},
		{
			name: "409 OperationNotAllowed is retryable",
			err: &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "OperationNotAllowed",
			},
			expected: true,
		},
		{
			name: "409 with different error code is not retryable",
			err: &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "ResourceGroupNotFound",
			},
			expected: false,
		},
		{
			name: "404 is not retryable",
			err: &azcore.ResponseError{
				StatusCode: http.StatusNotFound,
			},
			expected: false,
		},
		{
			name: "500 is not retryable",
			err: &azcore.ResponseError{
				StatusCode: http.StatusInternalServerError,
			},
			expected: false,
		},
		{
			name: "wrapped 409 OperationNotAllowed is retryable",
			err: fmt.Errorf("outer: %w", &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "OperationNotAllowed",
			}),
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isRetryableBootDiagnosticsError(tc.err)
			if result != tc.expected {
				t.Errorf("isRetryableBootDiagnosticsError(%v) = %v, want %v", tc.err, result, tc.expected)
			}
		})
	}
}

func TestOutputFromRunCommandInstanceView(t *testing.T) {
	const (
		runCommandName = "e2e-runcommand-1"
		vmName         = "test-vm"
	)

	tests := []struct {
		name         string
		instanceView *armcompute.VirtualMachineRunCommandInstanceView
		wantOutput   string
		wantErr      string
	}{
		{
			name: "returns trimmed stdout on success",
			instanceView: &armcompute.VirtualMachineRunCommandInstanceView{
				Output:         to.Ptr("  hello world  \n"),
				ExitCode:       to.Ptr(int32(0)),
				ExecutionState: to.Ptr(armcompute.ExecutionStateSucceeded),
			},
			wantOutput: "hello world",
		},
		{
			name:         "returns empty stdout when fields are unset",
			instanceView: &armcompute.VirtualMachineRunCommandInstanceView{},
			wantOutput:   "",
		},
		{
			name: "whitespace-only stderr is not treated as failure",
			instanceView: &armcompute.VirtualMachineRunCommandInstanceView{
				Output: to.Ptr("ok"),
				Error:  to.Ptr("   \n"),
			},
			wantOutput: "ok",
		},
		{
			name: "stderr takes precedence over exit code and execution state",
			instanceView: &armcompute.VirtualMachineRunCommandInstanceView{
				Output:         to.Ptr("stdout"),
				Error:          to.Ptr(" command failed \n"),
				ExitCode:       to.Ptr(int32(1)),
				ExecutionState: to.Ptr(armcompute.ExecutionStateFailed),
			},
			wantErr: "command failed",
		},
		{
			name: "non-zero exit code is an error",
			instanceView: &armcompute.VirtualMachineRunCommandInstanceView{
				Output:         to.Ptr("partial output"),
				ExitCode:       to.Ptr(int32(2)),
				ExecutionState: to.Ptr(armcompute.ExecutionStateSucceeded),
			},
			wantErr: `run command "e2e-runcommand-1" failed with exit code 2 on VM "test-vm" (output: "partial output")`,
		},
		{
			name: "unexpected execution state is an error",
			instanceView: &armcompute.VirtualMachineRunCommandInstanceView{
				Output:         to.Ptr("still running"),
				ExitCode:       to.Ptr(int32(0)),
				ExecutionState: to.Ptr(armcompute.ExecutionStateRunning),
			},
			wantErr: `run command "e2e-runcommand-1" finished in unexpected state "Running" on VM "test-vm" (output: "still running")`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := outputFromRunCommandInstanceView(tc.instanceView, runCommandName, vmName)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("outputFromRunCommandInstanceView() unexpected error: %v", err)
				}
				if got != tc.wantOutput {
					t.Errorf("outputFromRunCommandInstanceView() output = %q, want %q", got, tc.wantOutput)
				}
				return
			}
			if err == nil {
				t.Fatalf("outputFromRunCommandInstanceView() error = nil, want %q", tc.wantErr)
			}
			if got != "" {
				t.Errorf("outputFromRunCommandInstanceView() output = %q, want empty on error", got)
			}
			if err.Error() != tc.wantErr && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("outputFromRunCommandInstanceView() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}
