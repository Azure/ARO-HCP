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

package controllerutils

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

// fakeControllerCRUD is a simple in-memory implementation of ResourceCRUD[api.Controller] for testing
type fakeControllerCRUD struct {
	controllers map[string]*api.Controller
}

func newFakeControllerCRUD() *fakeControllerCRUD {
	return &fakeControllerCRUD{
		controllers: make(map[string]*api.Controller),
	}
}

func (f *fakeControllerCRUD) GetByID(ctx context.Context, cosmosID string) (*api.Controller, error) {
	return nil, nil
}

func (f *fakeControllerCRUD) Get(ctx context.Context, resourceID string) (*api.Controller, error) {
	if c, ok := f.controllers[resourceID]; ok {
		return c, nil
	}
	return nil, database.NewNotFoundError()
}

func (f *fakeControllerCRUD) List(ctx context.Context, opts *database.DBClientListResourceDocsOptions) (database.DBClientIterator[api.Controller], error) {
	return nil, nil
}

func (f *fakeControllerCRUD) Create(ctx context.Context, newObj *api.Controller, options *azcosmos.ItemOptions) (*api.Controller, error) {
	f.controllers[newObj.ResourceID.Name] = newObj
	return newObj, nil
}

func (f *fakeControllerCRUD) Replace(ctx context.Context, newObj *api.Controller, options *azcosmos.ItemOptions) (*api.Controller, error) {
	f.controllers[newObj.ResourceID.Name] = newObj
	return newObj, nil
}

func (f *fakeControllerCRUD) Delete(ctx context.Context, resourceID string) error {
	delete(f.controllers, resourceID)
	return nil
}

func (f *fakeControllerCRUD) AddCreateToTransaction(ctx context.Context, transaction database.DBTransaction, newObj *api.Controller, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return "", nil
}

func (f *fakeControllerCRUD) AddReplaceToTransaction(ctx context.Context, transaction database.DBTransaction, newObj *api.Controller, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return "", nil
}

func TestDegradedControllerPanicHandler(t *testing.T) {
	controllerName := "test-controller"
	subscriptionID := "00000000-0000-0000-0000-000000000000"
	resourceGroup := "test-rg"
	clusterName := "test-cluster"

	initialController := func(name string) *api.Controller {
		resourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID + "/resourceGroups/" + resourceGroup +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
				"/" + api.ControllerResourceTypeName + "/" + name))
		clusterResourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + subscriptionID + "/resourceGroups/" + resourceGroup +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))
		return &api.Controller{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID: resourceID,
			},
			ExternalID: clusterResourceID,
			Status: api.ControllerStatus{
				Conditions: []metav1.Condition{},
			},
		}
	}

	t.Run("captures panic value and stack trace", func(t *testing.T) {
		fakeCRUD := newFakeControllerCRUD()
		ctx := context.Background()

		handler := DegradedControllerPanicHandler(ctx, fakeCRUD, controllerName, initialController)

		// Call the handler with a panic value
		handler("test panic message")

		// Verify the controller was created with the Degraded condition
		controller := fakeCRUD.controllers[controllerName]
		require.NotNil(t, controller, "controller should have been created")

		degradedCondition := meta.FindStatusCondition(controller.Status.Conditions, "Degraded")
		require.NotNil(t, degradedCondition, "Degraded condition should exist")
		require.Equal(t, metav1.ConditionTrue, degradedCondition.Status, "Degraded condition should be True")
		require.Equal(t, "Failed", degradedCondition.Reason, "Degraded condition reason should be Failed")

		// Verify the message contains the panic value
		require.Contains(t, degradedCondition.Message, "test panic message", "message should contain panic value")

		// Verify the message contains stack trace indicators
		require.Contains(t, degradedCondition.Message, "panic caught:", "message should indicate panic was caught")
		require.Contains(t, degradedCondition.Message, "goroutine", "message should contain stack trace with goroutine info")
		require.Contains(t, degradedCondition.Message, ".go:", "message should contain stack trace with file references")
	})

	t.Run("captures error type panic with stack trace", func(t *testing.T) {
		fakeCRUD := newFakeControllerCRUD()
		ctx := context.Background()

		handler := DegradedControllerPanicHandler(ctx, fakeCRUD, controllerName, initialController)

		// Call the handler with an error panic value
		handler(strings.NewReader("error-like panic"))

		controller := fakeCRUD.controllers[controllerName]
		require.NotNil(t, controller, "controller should have been created")

		degradedCondition := meta.FindStatusCondition(controller.Status.Conditions, "Degraded")
		require.NotNil(t, degradedCondition, "Degraded condition should exist")

		// Verify stack trace is present even for non-string panic values
		require.Contains(t, degradedCondition.Message, "goroutine", "message should contain stack trace")
	})
}
