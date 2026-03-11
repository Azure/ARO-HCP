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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

func TestGetCondition(t *testing.T) {
	tests := []struct {
		name          string
		conditions    []api.Condition
		conditionType string
		wantFound     bool
		wantMessage   string
	}{
		{
			name:          "returns nil for nil slice",
			conditions:    nil,
			conditionType: ConditionTypeDegraded,
			wantFound:     false,
		},
		{
			name:          "returns nil when type not found",
			conditions:    []api.Condition{{Type: "Available", Status: api.ConditionTrue, Message: "Available"}},
			conditionType: ConditionTypeDegraded,
			wantFound:     false,
		},
		{
			name:          "returns first match when multiple have same type",
			conditions:    []api.Condition{{Type: ConditionTypeDegraded, Status: api.ConditionTrue, Message: "first"}, {Type: ConditionTypeDegraded, Status: api.ConditionFalse, Message: "second"}},
			conditionType: ConditionTypeDegraded,
			wantFound:     true,
			wantMessage:   "first",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := GetCondition(tt.conditions, tt.conditionType)
			require.Equal(t, tt.wantFound, res != nil)
			if tt.wantFound {
				require.Equal(t, tt.wantMessage, res.Message)
			}
			if len(tt.wantMessage) > 0 {
				require.Equal(t, tt.wantMessage, res.Message)
			}
		})
	}

	t.Run("the returned reference should never point to the original item in the list", func(t *testing.T) {
		conditions := []api.Condition{
			{Type: "Available", Status: api.ConditionTrue, Message: "Available"},
			{Type: ConditionTypeDegraded, Status: api.ConditionTrue, Reason: "NoErrors", Message: "As expected."},
		}
		unwantedCondition := &conditions[1]

		res := GetCondition(conditions, ConditionTypeDegraded)
		// We intentionally perform a pointer comparison to check that the returned condition is a reference to the found one in the list.
		if res == unwantedCondition {
			t.Errorf("returned condition is a reference to the found one in the list")
		}
	})
}

func TestSetCondition(t *testing.T) {
	tests := []struct {
		name                 string
		conditions           []api.Condition
		toSet                api.Condition
		wantLen              int
		wantConditionMessage string
	}{
		{
			name:                 "adds condition when slice is nil",
			conditions:           nil,
			toSet:                api.Condition{Type: ConditionTypeDegraded, Status: api.ConditionFalse, Reason: "NoErrors", Message: "As expected."},
			wantLen:              1,
			wantConditionMessage: "As expected.",
		},
		{
			name:                 "adds condition when type not found",
			conditions:           []api.Condition{{Type: "Available", Status: api.ConditionTrue}},
			toSet:                api.Condition{Type: ConditionTypeDegraded, Status: api.ConditionFalse, Reason: "NoErrors", Message: "As expected."},
			wantLen:              2,
			wantConditionMessage: "As expected.",
		},
		{
			name:                 "modifies existing condition when found",
			conditions:           []api.Condition{{Type: ConditionTypeDegraded, Status: api.ConditionTrue, Reason: "Failed", Message: "Had an error"}},
			toSet:                api.Condition{Type: ConditionTypeDegraded, Status: api.ConditionFalse, Reason: "NoErrors", Message: "As expected."},
			wantLen:              1,
			wantConditionMessage: "As expected.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conditions := tt.conditions
			SetCondition(&conditions, tt.toSet)
			require.Len(t, conditions, tt.wantLen)
			retrievedCondition := GetCondition(conditions, tt.toSet.Type)
			require.NotNil(t, retrievedCondition)
			require.Equal(t, tt.wantConditionMessage, retrievedCondition.Message)
		})
	}
}

func TestIsConditionTrue(t *testing.T) {
	tests := []struct {
		name          string
		conditions    []api.Condition
		conditionType string
		want          bool
	}{
		{"returns false for nil conditions", nil, ConditionTypeDegraded, false},
		{
			name:          "returns false when condition not found",
			conditions:    []api.Condition{{Type: "Available", Status: api.ConditionTrue}},
			conditionType: ConditionTypeDegraded,
			want:          false,
		},
		{
			name:          "returns false when condition found and its status is False",
			conditions:    []api.Condition{{Type: ConditionTypeDegraded, Status: api.ConditionFalse}},
			conditionType: ConditionTypeDegraded,
			want:          false,
		},
		{
			name:          "returns true when condition found and status is True",
			conditions:    []api.Condition{{Type: ConditionTypeDegraded, Status: api.ConditionTrue}},
			conditionType: ConditionTypeDegraded,
			want:          true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsConditionTrue(tt.conditions, tt.conditionType)
			if got != tt.want {
				t.Errorf("IsConditionTrue() = %v, want %v", got, tt.want)
			}
		})
	}
}

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
	return nil, nil
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
			ResourceID: resourceID,
			ExternalID: clusterResourceID,
			Status: api.ControllerStatus{
				Conditions: []api.Condition{},
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

		degradedCondition := GetCondition(controller.Status.Conditions, "Degraded")
		require.NotNil(t, degradedCondition, "Degraded condition should exist")
		require.Equal(t, api.ConditionTrue, degradedCondition.Status, "Degraded condition should be True")
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

		degradedCondition := GetCondition(controller.Status.Conditions, "Degraded")
		require.NotNil(t, degradedCondition, "Degraded condition should exist")

		// Verify stack trace is present even for non-string panic values
		require.Contains(t, degradedCondition.Message, "goroutine", "message should contain stack trace")
	})
}
