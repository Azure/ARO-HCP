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
	"errors"
	"net/http"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// scriptedControllerCRUD is a ResourceCRUD whose Get and Create return
// pre-scripted results in order, so getOrCreateControllerDocument's
// conflict/NotFound branches can be exercised deterministically. All other
// methods are unused by that code path and panic if called.
type scriptedControllerCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller]
	getResults    []scriptedResult
	createResults []scriptedResult
	getCalls      int
	createCalls   int
}

type scriptedResult struct {
	obj *coreapi.Controller
	err error
}

func (f *scriptedControllerCRUD) Get(_ context.Context, _ string) (*coreapi.Controller, error) {
	r := f.getResults[f.getCalls]
	f.getCalls++
	return r.obj, r.err
}

func (f *scriptedControllerCRUD) Create(_ context.Context, _ *coreapi.Controller, _ *azcosmos.ItemOptions) (*coreapi.Controller, error) {
	r := f.createResults[f.createCalls]
	f.createCalls++
	return r.obj, r.err
}

func notFoundErr() error { return &azcore.ResponseError{StatusCode: http.StatusNotFound} }
func conflictErr() error { return &azcore.ResponseError{StatusCode: http.StatusConflict} }

func TestGetOrCreateControllerDocument(t *testing.T) {
	const controllerName = "TestController"
	initialFn := func(name string) *coreapi.Controller { return &coreapi.Controller{} }

	t.Run("returns the existing document when Get succeeds", func(t *testing.T) {
		want := &coreapi.Controller{}
		crud := &scriptedControllerCRUD{getResults: []scriptedResult{{obj: want}}}

		got, err := getOrCreateControllerDocument(testContext(), crud, controllerName, initialFn)
		require.NoError(t, err, "getOrCreateControllerDocument")
		assert.Same(t, want, got, "expected the document returned by Get")
	})

	t.Run("propagates a non-NotFound Get error", func(t *testing.T) {
		crud := &scriptedControllerCRUD{getResults: []scriptedResult{{err: errors.New("boom")}}}

		_, err := getOrCreateControllerDocument(testContext(), crud, controllerName, initialFn)
		assert.Error(t, err, "expected the Get error to propagate")
	})

	t.Run("creates the document when Get returns NotFound", func(t *testing.T) {
		want := &coreapi.Controller{}
		crud := &scriptedControllerCRUD{
			getResults:    []scriptedResult{{err: notFoundErr()}},
			createResults: []scriptedResult{{obj: want}},
		}

		got, err := getOrCreateControllerDocument(testContext(), crud, controllerName, initialFn)
		require.NoError(t, err, "getOrCreateControllerDocument")
		assert.Same(t, want, got, "expected the created document")
	})

	t.Run("re-reads after a Create conflict (lost creation race)", func(t *testing.T) {
		want := &coreapi.Controller{}
		crud := &scriptedControllerCRUD{
			getResults:    []scriptedResult{{err: notFoundErr()}, {obj: want}},
			createResults: []scriptedResult{{err: conflictErr()}},
		}

		got, err := getOrCreateControllerDocument(testContext(), crud, controllerName, initialFn)
		require.NoError(t, err, "getOrCreateControllerDocument")
		assert.Same(t, want, got, "expected the document read after the create conflict")
	})

	t.Run("propagates a non-Conflict Create error", func(t *testing.T) {
		crud := &scriptedControllerCRUD{
			getResults:    []scriptedResult{{err: notFoundErr()}},
			createResults: []scriptedResult{{err: errors.New("boom")}},
		}

		_, err := getOrCreateControllerDocument(testContext(), crud, controllerName, initialFn)
		assert.Error(t, err, "expected the Create error to propagate")
	})

	t.Run("returns ctx error when cancelled during the soft-delete TTL wait", func(t *testing.T) {
		crud := &scriptedControllerCRUD{
			getResults:    []scriptedResult{{err: notFoundErr()}, {err: notFoundErr()}},
			createResults: []scriptedResult{{err: conflictErr()}},
		}
		ctx, cancel := context.WithCancel(testContext())
		cancel() // TTL-wait select must observe the cancelled context immediately

		_, err := getOrCreateControllerDocument(ctx, crud, controllerName, initialFn)
		assert.ErrorIs(t, err, context.Canceled, "expected the cancelled context error during the TTL wait")
	})

	t.Run("errors when initialControllerFn is nil", func(t *testing.T) {
		crud := &scriptedControllerCRUD{}
		_, err := getOrCreateControllerDocument(testContext(), crud, controllerName, nil)
		assert.Error(t, err, "expected an error when initialControllerFn is nil")
	})
}

func testContext() context.Context {
	return utils.ContextWithLogger(context.Background(), logr.Discard())
}

func TestReportSyncError(t *testing.T) {
	tests := []struct {
		name       string
		syncErr    error
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{
			name:       "nil error sets Degraded=False",
			syncErr:    nil,
			wantStatus: metav1.ConditionFalse,
			wantReason: "NoErrors",
		},
		{
			name:       "non-nil error sets Degraded=True",
			syncErr:    errors.New("boom"),
			wantStatus: metav1.ConditionTrue,
			wantReason: "Failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &coreapi.Controller{}
			ReportSyncError(tt.syncErr)(controller)

			cond := apimeta.FindStatusCondition(controller.Status.Conditions, "Degraded")
			require.NotNil(t, cond, "expected Degraded condition to be set")
			assert.Equal(t, tt.wantStatus, cond.Status, "condition status")
			assert.Equal(t, tt.wantReason, cond.Reason, "condition reason")
		})
	}
}

func TestWriteController(t *testing.T) {
	const stampID = "s1"
	const controllerName = "TestController"

	t.Run("creates the controller document when missing", func(t *testing.T) {
		mockDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
		controllerCRUD := mockDB.Stamps().ManagementClusters(stampID).Controllers()
		key := ManagementClusterKey{StampIdentifier: stampID}

		err := WriteController(testContext(), controllerCRUD, controllerName, key.InitialController, ReportSyncError(nil))
		require.NoError(t, err, "WriteController")

		stored, err := controllerCRUD.Get(testContext(), controllerName)
		require.NoError(t, err, "Get after WriteController")

		cond := apimeta.FindStatusCondition(stored.Status.Conditions, "Degraded")
		require.NotNil(t, cond, "expected Degraded condition to be set")
		assert.Equal(t, metav1.ConditionFalse, cond.Status, "condition status")
	})

	t.Run("updates the controller document when a mutation changes it", func(t *testing.T) {
		mockDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
		controllerCRUD := mockDB.Stamps().ManagementClusters(stampID).Controllers()
		key := ManagementClusterKey{StampIdentifier: stampID}
		ctx := testContext()

		require.NoError(t, WriteController(ctx, controllerCRUD, controllerName, key.InitialController, ReportSyncError(nil)))
		require.NoError(t, WriteController(ctx, controllerCRUD, controllerName, key.InitialController, ReportSyncError(errors.New("boom"))))

		stored, err := controllerCRUD.Get(ctx, controllerName)
		require.NoError(t, err, "Get after second WriteController")

		cond := apimeta.FindStatusCondition(stored.Status.Conditions, "Degraded")
		require.NotNil(t, cond, "expected Degraded condition to be set")
		assert.Equal(t, metav1.ConditionTrue, cond.Status, "condition status")
		assert.Equal(t, "Failed", cond.Reason, "condition reason")
	})

	t.Run("is a no-op when the mutation produces no change", func(t *testing.T) {
		mockDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
		controllerCRUD := mockDB.Stamps().ManagementClusters(stampID).Controllers()
		key := ManagementClusterKey{StampIdentifier: stampID}
		ctx := testContext()

		require.NoError(t, WriteController(ctx, controllerCRUD, controllerName, key.InitialController, ReportSyncError(nil)))
		before, err := controllerCRUD.Get(ctx, controllerName)
		require.NoError(t, err, "Get before repeated WriteController")

		require.NoError(t, WriteController(ctx, controllerCRUD, controllerName, key.InitialController, ReportSyncError(nil)))
		after, err := controllerCRUD.Get(ctx, controllerName)
		require.NoError(t, err, "Get after repeated WriteController")

		assert.Equal(t, before.CosmosETag, after.CosmosETag, "no-op write should not replace the document")
	})

	t.Run("errors when initialControllerFn is nil", func(t *testing.T) {
		mockDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
		controllerCRUD := mockDB.Stamps().ManagementClusters(stampID).Controllers()

		err := WriteController(testContext(), controllerCRUD, controllerName, nil, ReportSyncError(nil))
		assert.Error(t, err, "expected error when initialControllerFn is nil")
	})
}
