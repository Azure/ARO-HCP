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

package operationutils

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// stubOperationStatusCalculator records inputs without wrapping a function.
type stubOperationStatusCalculator[Input any] struct {
	source    string
	state     *OperationState
	err       error
	calls     *[]string
	called    bool
	ctx       context.Context
	operation *coreapi.Operation
	input     Input
}

func (c *stubOperationStatusCalculator[Input]) GetSourceName() string { return c.source }

func (c *stubOperationStatusCalculator[Input]) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, input Input) (*OperationState, error) {
	c.called = true
	c.ctx, c.operation, c.input = ctx, operation, input
	if c.calls != nil {
		*c.calls = append(*c.calls, c.source)
	}
	return c.state, c.err
}

func TestOperationStatusCalculatorsUnion(t *testing.T) {
	t.Parallel()
	operation := &coreapi.Operation{}
	input := &coreapi.Cluster{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Equal state, message and code priority must select the same code regardless
	// of map iteration. A successful source's code must not influence the result.
	states := map[string]*OperationState{
		"z-validation": NewOperationState(coreapi.ProvisioningStateProvisioning, "waiting").WithCloudErrorCode(coreapi.CloudErrorCodeInvalidParameter),
		"a-validation": NewOperationState(coreapi.ProvisioningStateProvisioning, "waiting").WithCloudErrorCode(coreapi.CloudErrorCodeInvalidResource),
		"internal":     NewOperationState(coreapi.ProvisioningStateProvisioning, "still installing"),
		"success":      NewOperationState(coreapi.ProvisioningStateSucceeded, "ready").WithCloudErrorCode("InvalidSuccessfulSource"),
	}
	var calls []string
	calculators := OperationStatusCalculators[*coreapi.Cluster]{}
	for source, state := range states {
		state.Source = "original"
		calculators[source] = &stubOperationStatusCalculator[*coreapi.Cluster]{source: source, state: state, calls: &calls}
	}

	for range 50 {
		calls = nil
		got, err := calculators.CalculateOperationStatus(ctx, operation, input)
		require.NoError(t, err)
		assert.Equal(t, []string{"a-validation", "internal", "success", "z-validation"}, calls)
		assert.Equal(t, coreapi.ProvisioningStateProvisioning, got.ProvisioningState)
		assert.Equal(t, coreapi.CloudErrorCodeInvalidResource, got.CloudErrorCode)
		assert.Equal(t, "[internal] still installing; [a-validation] waiting; [z-validation] waiting", got.Message)
		assert.Nil(t, got.Error)
		for _, calculator := range calculators {
			stub := calculator.(*stubOperationStatusCalculator[*coreapi.Cluster])
			assert.Equal(t, ctx, stub.ctx)
			assert.Same(t, operation, stub.operation)
			assert.Same(t, input, stub.input)
		}
	}
	for _, state := range states {
		assert.Equal(t, "original", state.Source, "evaluating a source must not mutate the calculator's state")
	}
}

func TestOperationStatusCalculatorsPreserveCustomerErrors(t *testing.T) {
	t.Parallel()
	customerError := &coreapi.CloudErrorBody{
		Code:    coreapi.CloudErrorCodeInternalServerError,
		Message: "customer-safe failure",
		Details: []coreapi.CloudErrorBody{{Code: coreapi.CloudErrorCodeInvalidParameter, Message: "invalid subnet"}},
	}
	originalError := customerError.DeepCopy()
	calculators, err := NewOperationStatusCalculators[struct{}](
		&stubOperationStatusCalculator[struct{}]{source: "service", state: NewFailedOperationState(coreapi.CloudErrorCodeInternalServerError, "internal diagnostic", customerError)},
		&stubOperationStatusCalculator[struct{}]{source: "validation", state: NewFailedOperationState(coreapi.CloudErrorCodeInvalidResource, "validation failed", nil)},
	)
	require.NoError(t, err)

	got, err := calculators.CalculateOperationStatus(context.Background(), &coreapi.Operation{}, struct{}{})
	require.NoError(t, err)
	assert.Equal(t, coreapi.ProvisioningStateFailed, got.ProvisioningState)
	assert.Equal(t, coreapi.CloudErrorCodeInvalidResource, got.CloudErrorCode)
	require.NotNil(t, got.Error)
	assert.Equal(t, coreapi.CloudErrorCodeInvalidResource, got.Error.Code)
	assert.Equal(t, originalError.Message, got.Error.Message)
	assert.Equal(t, originalError.Details, got.Error.Details)
	assert.Equal(t, originalError, customerError, "the union must not mutate the source error")
}

func TestOperationStatusCalculatorsJoinErrors(t *testing.T) {
	t.Parallel()
	listerError := errors.New("lister failed")
	clientError := errors.New("cluster service failed")
	var calls []string
	calculators := OperationStatusCalculators[struct{}]{}
	for source, sourceError := range map[string]error{"a-lister": listerError, "b-client": clientError, "c-ready": nil} {
		calculators[source] = &stubOperationStatusCalculator[struct{}]{source: source, state: NewOperationState(coreapi.ProvisioningStateSucceeded, ""), err: sourceError, calls: &calls}
	}
	got, err := calculators.CalculateOperationStatus(context.Background(), &coreapi.Operation{}, struct{}{})
	assert.Nil(t, got, "source errors must prevent returning a partial union")
	require.Error(t, err)
	assert.ErrorIs(t, err, listerError)
	assert.ErrorIs(t, err, clientError)
	assert.Contains(t, err.Error(), `source "a-lister"`)
	assert.Contains(t, err.Error(), `source "b-client"`)
	assert.Equal(t, []string{"a-lister", "b-client", "c-ready"}, calls, "all sources must run despite earlier errors")
}

func TestOperationStatusCalculatorsRejectInvalidStates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		state     *OperationState
		wantError string
	}{
		{name: "nil", wantError: "nil operation state"},
		{name: "empty provisioning state", state: &OperationState{}, wantError: "empty provisioning state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ready := &stubOperationStatusCalculator[struct{}]{source: "z-ready", state: NewOperationState(coreapi.ProvisioningStateSucceeded, "")}
			calculators, err := NewOperationStatusCalculators[struct{}](
				&stubOperationStatusCalculator[struct{}]{source: "a-invalid", state: tc.state},
				ready,
			)
			require.NoError(t, err)
			got, err := calculators.CalculateOperationStatus(context.Background(), &coreapi.Operation{}, struct{}{})
			assert.Nil(t, got)
			require.ErrorContains(t, err, tc.wantError)
			assert.Contains(t, err.Error(), `source "a-invalid"`)
			assert.True(t, ready.called, "invalid results must not stop other observations")
		})
	}
}

func TestOperationStatusCalculatorRegistration(t *testing.T) {
	t.Parallel()
	check := &stubOperationStatusCalculator[struct{}]{source: "source", state: NewOperationState(coreapi.ProvisioningStateSucceeded, "")}
	for _, tc := range []struct {
		name        string
		calculators []OperationStatusCalculator[struct{}]
		wantError   string
	}{
		{name: "duplicate", calculators: []OperationStatusCalculator[struct{}]{check, check}, wantError: "duplicate operation status source"},
		{name: "nil", calculators: []OperationStatusCalculator[struct{}]{nil}, wantError: "nil operation status calculator"},
		{name: "unnamed", calculators: []OperationStatusCalculator[struct{}]{&stubOperationStatusCalculator[struct{}]{}}, wantError: "empty operation status source name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewOperationStatusCalculators(tc.calculators...)
			require.ErrorContains(t, err, tc.wantError)
		})
	}

	for _, tc := range []struct {
		name        string
		calculators OperationStatusCalculators[struct{}]
		wantError   string
	}{
		{name: "empty", wantError: "no operation states"},
		{name: "mismatched key", calculators: OperationStatusCalculators[struct{}]{"different-source": check}, wantError: "invalid operation status calculator"},
		{name: "nil entry", calculators: OperationStatusCalculators[struct{}]{"source": nil}, wantError: "invalid operation status calculator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.calculators.CalculateOperationStatus(context.Background(), &coreapi.Operation{}, struct{}{})
			assert.Nil(t, got)
			require.ErrorContains(t, err, tc.wantError)
		})
	}
}
