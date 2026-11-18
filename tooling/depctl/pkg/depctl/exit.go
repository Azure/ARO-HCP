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

// Package depctl holds the public exit contract of the depctl binary.
//
// The lifecycle engine itself lives under internal/orchestration and is
// composed only by the commands in this module. No adapters are exported: a
// caller cannot construct write authority, and the engine surface is not a
// promised Go API until the record contract is frozen (decision D10).
package depctl

import "fmt"

// Exit codes. Callers must inspect outcome, phase and complete rather than exit
// zero alone.
const (
	ExitOK         = 0
	ExitInvalid    = 2
	ExitIncomplete = 3
	ExitValidation = 4
	ExitStale      = 5
	ExitInternal   = 6
)

// ExitError carries a process exit code and a sanitized message.
type ExitError struct {
	Code    int
	Message string
}

// Error implements error.
func (e *ExitError) Error() string { return e.Message }

// Errorf builds an exit error.
func Errorf(code int, format string, args ...any) *ExitError {
	return &ExitError{Code: code, Message: fmt.Sprintf(format, args...)}
}
