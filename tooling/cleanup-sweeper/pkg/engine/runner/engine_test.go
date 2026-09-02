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

package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

type fakeStep struct {
	name            string
	targets         []Target
	discoverErr     error
	deleteErrByID   map[string]error
	verifyErr       error
	verifyCalls     int
	deleteCalls     int
	retryLimit      int
	continueOnError bool
}

func (f *fakeStep) Name() string { return f.name }

func (f *fakeStep) Discover(_ context.Context) ([]Target, error) {
	if f.discoverErr != nil {
		return nil, f.discoverErr
	}
	return append([]Target(nil), f.targets...), nil
}

func (f *fakeStep) Delete(_ context.Context, target Target, _ bool) error {
	f.deleteCalls++
	if err, ok := f.deleteErrByID[target.ID]; ok {
		return err
	}
	return nil
}

func (f *fakeStep) Verify(_ context.Context) error {
	f.verifyCalls++
	return f.verifyErr
}

func (f *fakeStep) RetryLimit() int {
	if f.retryLimit < 1 {
		return 1
	}
	return f.retryLimit
}

func (f *fakeStep) ContinueOnError() bool { return f.continueOnError }

func TestEngineRun(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name            string
		step            *fakeStep
		parallelism     int
		dryRun          bool
		enablePostRunFn bool
		assertions      func(t *testing.T, err error, step *fakeStep, postRunCalled bool)
	}

	testCases := []testCase{
		{
			name: "dry-run skips delete and verify",
			step: &fakeStep{
				name:    "dry-run-step",
				targets: []Target{{ID: "a"}, {ID: "b"}},
			},
			parallelism: 1,
			dryRun:      true,
			assertions: func(t *testing.T, err error, step *fakeStep, _ bool) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if step.deleteCalls != 0 {
					t.Fatalf("expected no delete calls in dry-run, got %d", step.deleteCalls)
				}
				if step.verifyCalls != 0 {
					t.Fatalf("expected no verify calls in dry-run, got %d", step.verifyCalls)
				}
			},
		},
		{
			name: "delete error continues when configured",
			step: &fakeStep{
				name:            "continue-step",
				targets:         []Target{{ID: "x"}},
				deleteErrByID:   map[string]error{"x": errors.New("boom")},
				continueOnError: true,
			},
			parallelism: 1,
			assertions: func(t *testing.T, err error, _ *fakeStep, _ bool) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			},
		},
		{
			name: "delete error fails when not continuable",
			step: &fakeStep{
				name:            "fail-step",
				targets:         []Target{{ID: "x", Name: "res-x", Type: "example/type"}},
				deleteErrByID:   map[string]error{"x": errors.New("boom")},
				continueOnError: false,
			},
			parallelism: 1,
			assertions: func(t *testing.T, err error, _ *fakeStep, _ bool) {
				t.Helper()
				if err == nil {
					t.Fatalf("expected error")
				}
				if !strings.Contains(err.Error(), "failed deleting") {
					t.Fatalf("expected delete failure error, got %v", err)
				}
			},
		},
		{
			name: "target retained after revalidation is not retried or deleted",
			step: &fakeStep{
				name:          "retain-step",
				targets:       []Target{{ID: "x", Name: "res-x", Type: "example/type"}},
				deleteErrByID: map[string]error{"x": fmt.Errorf("%w: principal restored", ErrTargetRetained)},
				retryLimit:    3,
			},
			parallelism: 1,
			assertions: func(t *testing.T, err error, step *fakeStep, _ bool) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if step.deleteCalls != 1 {
					t.Fatalf("expected one revalidation call without retries, got %d", step.deleteCalls)
				}
			},
		},
		{
			name: "delete errors are joined when not continuable",
			step: &fakeStep{
				name: "fail-step",
				targets: []Target{
					{ID: "x", Name: "res-x", Type: "example/type"},
					{ID: "y", Name: "res-y", Type: "example/type"},
				},
				deleteErrByID: map[string]error{
					"x": errors.New("boom-x"),
					"y": errors.New("boom-y"),
				},
				continueOnError: false,
			},
			parallelism: 2,
			assertions: func(t *testing.T, err error, _ *fakeStep, _ bool) {
				t.Helper()
				if err == nil {
					t.Fatalf("expected error")
				}
				if !strings.Contains(err.Error(), "res-x") {
					t.Fatalf("expected joined error to contain res-x failure, got %v", err)
				}
				if !strings.Contains(err.Error(), "res-y") {
					t.Fatalf("expected joined error to contain res-y failure, got %v", err)
				}
			},
		},
		{
			name: "discover error fails even when continuable",
			step: &fakeStep{
				name:            "discover-fail-step",
				discoverErr:     errors.New("discover boom"),
				continueOnError: true,
			},
			parallelism: 1,
			assertions: func(t *testing.T, err error, _ *fakeStep, _ bool) {
				t.Helper()
				if err == nil {
					t.Fatalf("expected error")
				}
				if !strings.Contains(err.Error(), "discovery failed") {
					t.Fatalf("expected discovery failure error, got %v", err)
				}
			},
		},
		{
			name: "verify error is best-effort when configured",
			step: &fakeStep{
				name:            "verify-fail-step",
				targets:         []Target{{ID: "x"}},
				verifyErr:       errors.New("verify boom"),
				continueOnError: true,
			},
			parallelism: 1,
			assertions: func(t *testing.T, err error, _ *fakeStep, _ bool) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			},
		},
		{
			name: "verify error fails when not continuable",
			step: &fakeStep{
				name:            "verify-fail-step",
				targets:         []Target{{ID: "x"}},
				verifyErr:       errors.New("verify boom"),
				continueOnError: false,
			},
			parallelism: 1,
			assertions: func(t *testing.T, err error, _ *fakeStep, _ bool) {
				t.Helper()
				if err == nil {
					t.Fatalf("expected error")
				}
				if !strings.Contains(err.Error(), "verification failed") {
					t.Fatalf("expected verification failure error, got %v", err)
				}
			},
		},
		{
			name: "post-run callback is called on successful step execution",
			step: &fakeStep{
				name:    "post-run-success-step",
				targets: []Target{{ID: "a"}},
			},
			parallelism:     1,
			enablePostRunFn: true,
			assertions: func(t *testing.T, err error, _ *fakeStep, postRunCalled bool) {
				t.Helper()
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if !postRunCalled {
					t.Fatal("expected PostRunFn to be called")
				}
			},
		},
		{
			name: "post-run callback is not called on step failure",
			step: &fakeStep{
				name:          "post-run-fail-step",
				targets:       []Target{{ID: "x", Name: "x", Type: "t"}},
				deleteErrByID: map[string]error{"x": errors.New("boom")},
			},
			parallelism:     1,
			enablePostRunFn: true,
			assertions: func(t *testing.T, err error, _ *fakeStep, postRunCalled bool) {
				t.Helper()
				if err == nil {
					t.Fatal("expected error")
				}
				if postRunCalled {
					t.Fatal("PostRunFn should not be called when a step fails")
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			postRunCalled := false
			engine := &Engine{
				Steps:       []Step{tc.step},
				DryRun:      tc.dryRun,
				Parallelism: tc.parallelism,
			}
			if tc.enablePostRunFn {
				engine.PostRunFn = func(_ context.Context) error {
					postRunCalled = true
					return nil
				}
			}

			ctx := logr.NewContext(context.Background(), logr.Discard())
			err := engine.Run(ctx)
			tc.assertions(t, err, tc.step, postRunCalled)
		})
	}
}
