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

package base

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ControllerMutationFunc mutates a controller document in place. It should only
// perform short calls, not long lookups. It must not fail.
type ControllerMutationFunc func(controller *coreapi.Controller)

// ReportSyncError returns a mutation that sets the Degraded condition based on
// whether a sync error occurred.
func ReportSyncError(syncErr error) ControllerMutationFunc {
	return func(controller *coreapi.Controller) {
		if syncErr == nil {
			apimeta.SetStatusCondition(&controller.Status.Conditions, metav1.Condition{
				Type:    "Degraded",
				Status:  metav1.ConditionFalse,
				Reason:  "NoErrors",
				Message: "As expected.",
			})
			return
		}

		apimeta.SetStatusCondition(&controller.Status.Conditions, metav1.Condition{
			Type:    "Degraded",
			Status:  metav1.ConditionTrue,
			Reason:  "Failed",
			Message: fmt.Sprintf("Had an error while syncing: %s", syncErr.Error()),
		})
	}
}

// InitialControllerFunc builds a new coreapi.Controller for the given logical
// controller name.
type InitialControllerFunc func(controllerName string) *coreapi.Controller

// DegradedControllerPanicHandler returns a panic handler that writes a
// Degraded condition to the controller document with the panic stack trace.
func DegradedControllerPanicHandler(ctx context.Context, controllerCRUD cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller], controllerName string, initialControllerFn InitialControllerFunc) func(interface{}) {
	return func(panicVal interface{}) {
		stack := debug.Stack()
		err := WriteController(ctx, controllerCRUD, controllerName, initialControllerFn, ReportSyncError(fmt.Errorf("panic caught:\n%v\n\n%s", panicVal, stack)))
		if err != nil {
			logger := utils.LoggerFromContext(ctx)
			logger.Error(err, "failed to write controller after panic")
		}
	}
}

// WriteController reads the existing controller document (creating it if
// missing), applies mutations in order, and writes back the result. It only
// tries once — on conflict the control loop will re-run.
func WriteController(ctx context.Context, controllerCRUD cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller], controllerName string, initialControllerFn InitialControllerFunc, mutationFns ...ControllerMutationFunc) error {
	logger := utils.LoggerFromContext(ctx)

	existingController, err := getOrCreateControllerDocument(ctx, controllerCRUD, controllerName, initialControllerFn)
	if err != nil {
		return err
	}

	desiredController := existingController.DeepCopy()
	for _, mutationFn := range mutationFns {
		mutationFn(desiredController)
	}

	if equality.Semantic.DeepEqual(existingController, desiredController) {
		return nil
	}

	_, replaceErr := controllerCRUD.Replace(ctx, desiredController, nil)
	if replaceErr != nil {
		logger.Error(replaceErr, "failed to replace")
		return fmt.Errorf("failed to replace existing controller state: %w", replaceErr)
	}
	return nil
}

func getOrCreateControllerDocument(
	ctx context.Context,
	controllerCRUD cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller],
	controllerName string,
	initialControllerFn InitialControllerFunc,
) (*coreapi.Controller, error) {
	return getOrCreateControllerDocumentAttempt(ctx, controllerCRUD, controllerName, initialControllerFn, false)
}

func getOrCreateControllerDocumentAttempt(
	ctx context.Context,
	controllerCRUD cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller],
	controllerName string,
	initialControllerFn InitialControllerFunc,
	secondAttempt bool,
) (*coreapi.Controller, error) {
	if initialControllerFn == nil {
		return nil, fmt.Errorf("initialControllerFn is required")
	}

	existingController, err := controllerCRUD.Get(ctx, controllerName)
	switch {
	case err == nil:
		return existingController, nil
	case cosmosstorageutils.IsNotFoundError(err):
		// fall through
	default:
		return nil, utils.TrackError(err)
	}

	existingController, err = controllerCRUD.Create(ctx, initialControllerFn(controllerName), nil)
	switch {
	case err == nil:
		return existingController, nil
	case cosmosstorageutils.IsConflictError(err):
		// fall through
	default:
		return nil, utils.TrackError(err)
	}

	existingController, err = controllerCRUD.Get(ctx, controllerName)
	switch {
	case err == nil:
		return existingController, nil
	case cosmosstorageutils.IsNotFoundError(err):
		if secondAttempt {
			return nil, utils.TrackError(fmt.Errorf("second NotFound, Conflict, NotFound error: %w", err))
		}
		timer := time.NewTimer((cosmosstorageutils.SoftDeleteTTLSeconds + 1) * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, utils.TrackError(ctx.Err())
		case <-timer.C:
			return getOrCreateControllerDocumentAttempt(ctx, controllerCRUD, controllerName, initialControllerFn, true)
		}
	default:
		return nil, utils.TrackError(err)
	}
}
