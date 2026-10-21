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

package verifiers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPollUntilReady_ZeroTimeout(t *testing.T) {
	err := pollUntilReady(
		context.Background(),
		"test-verifier",
		0,
		DefaultPollInterval,
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error { return nil },
	)
	if err == nil {
		t.Fatal("expected error for zero timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timeout must be > 0") {
		t.Fatalf("unexpected error message: %s", err.Error())
	}
}

func TestPollUntilReady_NegativeTimeout(t *testing.T) {
	err := pollUntilReady(
		context.Background(),
		"test-verifier",
		-1*time.Second,
		DefaultPollInterval,
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error { return nil },
	)
	if err == nil {
		t.Fatal("expected error for negative timeout, got nil")
	}
}

func TestPollUntilReady_ParentContextDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := pollUntilReady(
		ctx,
		"test-verifier",
		10*time.Second,
		10*time.Millisecond,
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error { return fmt.Errorf("not ready") },
	)
	if err == nil {
		t.Fatal("expected error when parent context deadline exceeded, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected 'cancelled' in error, got: %s", err.Error())
	}
}

func TestPollUntilReady_PositiveTimeout(t *testing.T) {
	err := pollUntilReady(
		context.Background(),
		"test-verifier",
		5*time.Second,
		100*time.Millisecond,
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error { return nil },
	)
	if err != nil {
		t.Fatalf("expected success for positive timeout with passing check, got: %v", err)
	}
}
