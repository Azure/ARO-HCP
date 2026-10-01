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
	"fmt"
	"maps"
	"slices"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// OperationStatusCalculator observes one named source of an operation's status.
// Input is the resource data shared by the checks for one kind of operation.
// Implementations receive their listers, clients, clocks and other dependencies
// at construction, so the evaluator does not need a union of unrelated clients.
// Inputs and returned states must be treated as read-only by the evaluator.
type OperationStatusCalculator[Input any] interface {
	GetSourceName() string
	CalculateOperationStatus(context.Context, *coreapi.Operation, Input) (*OperationState, error)
}

// OperationStatusCalculators indexes calculators by GetSourceName().
type OperationStatusCalculators[Input any] map[string]OperationStatusCalculator[Input]

// NewOperationStatusCalculators registers checks without silently overwriting
// duplicate sources. The map can also be used to select or replace named checks.
func NewOperationStatusCalculators[Input any](calculators ...OperationStatusCalculator[Input]) (OperationStatusCalculators[Input], error) {
	ret := make(OperationStatusCalculators[Input], len(calculators))
	for _, calculator := range calculators {
		if calculator == nil {
			return nil, errors.New("nil operation status calculator")
		}
		source := calculator.GetSourceName()
		if source == "" {
			return nil, errors.New("empty operation status source name")
		}
		if _, exists := ret[source]; exists {
			return nil, fmt.Errorf("duplicate operation status source %q", source)
		}
		ret[source] = calculator
	}
	return ret, nil
}

// CalculateOperationStatus evaluates every source, joining evaluation errors
// instead of returning a partial state. It preserves the existing provisioning
// state/message precedence and uses source-name order to break exact ties, so
// map iteration cannot change messages, error codes or error detail ordering.
func (calculators OperationStatusCalculators[Input]) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, input Input) (*OperationState, error) {
	if len(calculators) == 0 {
		return nil, errors.New("no operation states")
	}

	states := make([]*OperationState, 0, len(calculators))
	var errs []error
	for _, source := range slices.Sorted(maps.Keys(calculators)) {
		calculator := calculators[source]
		if calculator == nil || source == "" || calculator.GetSourceName() != source {
			errs = append(errs, fmt.Errorf("invalid operation status calculator for source %q", source))
			continue
		}
		state, err := calculator.CalculateOperationStatus(ctx, operation, input)
		if err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("operation status source %q: %w", source, err)))
			continue
		}
		if state == nil {
			errs = append(errs, fmt.Errorf("operation status source %q: nil operation state", source))
			continue
		}
		if state.ProvisioningState == "" {
			errs = append(errs, fmt.Errorf("operation status source %q: empty provisioning state", source))
			continue
		}
		// A calculator may return a shared state. Annotate a copy so evaluating
		// it through another registration never changes the original source.
		annotated := *state
		annotated.Source = source
		states = append(states, &annotated)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	slices.SortStableFunc(states, CompareOperationState)
	logger := utils.LoggerFromContext(ctx)
	logger.Info("determined operation status", "operationStates", states)
	picked, err := PickWorstOperationState(states)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("picked operation status", "provisioningState", picked.ProvisioningState, "message", picked.Message)
	return picked, nil
}
