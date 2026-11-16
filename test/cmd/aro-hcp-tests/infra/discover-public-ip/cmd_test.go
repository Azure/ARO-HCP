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

package discoverpublicip

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestCommandCancellationHasNoStdout(t *testing.T) {
	var stdout bytes.Buffer
	command := NewCommand()
	command.SetOut(&stdout)
	command.SetErr(io.Discard)
	command.SetArgs([]string{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := command.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteContext = %v, want cancellation", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("cancellation wrote stdout: %q", stdout.String())
	}
}

func TestCommandRejectsArguments(t *testing.T) {
	var stdout bytes.Buffer
	command := NewCommand()
	command.SetOut(&stdout)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"unexpected"})
	if err := command.Execute(); err == nil {
		t.Fatal("command accepted a positional argument")
	}
	if stdout.Len() != 0 {
		t.Fatalf("argument rejection wrote stdout: %q", stdout.String())
	}
}
