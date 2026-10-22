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

package operations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestExternalAuthOperationCompletionDeadline(t *testing.T) {
	now := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)
	expired := metav1.NewTime(now.Add(-time.Second))
	future := metav1.NewTime(now.Add(time.Second))
	equal := metav1.NewTime(now)
	csErr := errors.New("cluster service unavailable")
	writeErr := errors.New("status write failed")

	for _, request := range []cosmosstorageutils.OperationRequest{
		cosmosstorageutils.OperationRequestCreate,
		cosmosstorageutils.OperationRequestUpdate,
		cosmosstorageutils.OperationRequestDelete,
	} {
		t.Run(string(request), func(t *testing.T) {
			for _, tc := range []struct {
				name             string
				deadline         *metav1.Time
				noCSID           bool
				prerequisiteGate bool
				owner            string
				terminal         coreapi.ProvisioningState
				absent           bool
				staleResource    bool
				resourceFailed   bool
				csError          bool
				transactionError error
			}{
				{name: "nil deadline"},
				{name: "future deadline", deadline: &future},
				{name: "equal deadline", deadline: &equal},
				{name: "expired deadline", deadline: &expired},
				{name: "nil deadline without CS ID", noCSID: true},
				{name: "future deadline without CS ID", deadline: &future, noCSID: true},
				{name: "equal deadline without CS ID", deadline: &equal, noCSID: true},
				{name: "expired deadline without CS ID", deadline: &expired, noCSID: true},
				{name: "nil deadline with prerequisite gate", prerequisiteGate: true},
				{name: "expired deadline with prerequisite gate", deadline: &expired, prerequisiteGate: true},
				{name: "nil deadline with CS error", csError: true},
				{name: "future deadline with CS error", deadline: &future, csError: true},
				{name: "equal deadline with CS error", deadline: &equal, csError: true},
				{name: "expired deadline bypasses CS error", deadline: &expired, csError: true},
				{name: "expired deadline mismatched owner", deadline: &expired, owner: "newer-operation"},
				{name: "expired deadline empty owner", deadline: &expired, owner: "empty"},
				{name: "expired deadline succeeded operation", deadline: &expired, terminal: coreapi.ProvisioningStateSucceeded},
				{name: "expired deadline failed operation", deadline: &expired, terminal: coreapi.ProvisioningStateFailed},
				{name: "expired deadline canceled operation", deadline: &expired, terminal: coreapi.ProvisioningStateCanceled},
				{name: "absent document", deadline: &expired, absent: true},
				{name: "expired deadline stale cached ownership", deadline: &expired, staleResource: true},
				{name: "expired deadline clears owner of already failed resource", deadline: &expired, resourceFailed: true},
				{name: "expired deadline precondition failure", deadline: &expired, transactionError: corecosmosstoragetesting.NewPreconditionFailedError()},
				{name: "expired deadline write error", deadline: &expired, transactionError: writeErr},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
					fixture := operationtesting.NewExternalAuthTestFixture()
					ea := fixture.NewExternalAuth()
					// Other operation types' expired deadlines must not affect this attempt.
					ea.ServiceProviderProperties.CreateOperationCompletionDeadline = &expired
					ea.ServiceProviderProperties.UpdateOperationCompletionDeadline = &expired
					ea.ServiceProviderProperties.DeleteOperationCompletionDeadline = &expired
					switch request {
					case cosmosstorageutils.OperationRequestCreate:
						ea.ServiceProviderProperties.CreateOperationCompletionDeadline = tc.deadline
					case cosmosstorageutils.OperationRequestUpdate:
						ea.ServiceProviderProperties.UpdateOperationCompletionDeadline = tc.deadline
					case cosmosstorageutils.OperationRequestDelete:
						ea.ServiceProviderProperties.DeleteOperationCompletionDeadline = tc.deadline
						ea.ServiceProviderProperties.DeletionTimestamp = &equal
						ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &equal
					}
					if tc.noCSID {
						ea.ServiceProviderProperties.ClusterServiceID = nil
					}
					if tc.prerequisiteGate {
						if request == cosmosstorageutils.OperationRequestDelete {
							ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = nil
						} else {
							ea.ServiceProviderProperties.DeletionTimestamp = &equal
						}
					}
					if tc.owner != "" {
						ea.ServiceProviderProperties.ActiveOperationID = tc.owner
						if tc.owner == "empty" {
							ea.ServiceProviderProperties.ActiveOperationID = ""
						}
					}
					if tc.resourceFailed {
						ea.Properties.ProvisioningState = coreapi.ProvisioningStateFailed
					}
					cachedEA := ea.DeepCopy()
					if tc.staleResource {
						ea.ServiceProviderProperties.ActiveOperationID = "newer-operation"
					}
					operation := fixture.NewOperation(request)
					if tc.terminal != "" {
						operation.Status = tc.terminal
						if tc.terminal == coreapi.ProvisioningStateFailed {
							operation.Error = &coreapi.CloudErrorBody{Code: coreapi.CloudErrorCodeInternalServerError, Message: "Original failure"}
						}
					}
					var notifications atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						notifications.Add(1)
						assert.Equal(t, http.MethodPost, r.Method)
						w.WriteHeader(http.StatusOK)
					}))
					defer server.Close()
					operation.NotificationURI = server.URL
					resources := []any{fixture.NewCluster(), operation}
					if !tc.absent {
						resources = append(resources, ea)
					}
					db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
					require.NoError(t, err)
					var client corecosmosstorage.ResourcesDBClient = db
					if tc.transactionError != nil {
						client = &externalAuthTimeoutErrorDB{ResourcesDBClient: db, err: tc.transactionError}
					}
					var eaLister corelisters.ExternalAuthLister = &corelistertesting.DBExternalAuthLister{ResourcesDBClient: db}
					if tc.staleResource {
						eaLister = &corelistertesting.SliceExternalAuthLister{ExternalAuths: []*coreapi.ExternalAuth{cachedEA}}
					}
					operationLister := &corelistertesting.DBActiveOperationLister{ResourcesDBClient: db}
					clock := clocktesting.NewFakePassiveClock(now)
					cs := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
					expires := tc.deadline != nil && now.After(tc.deadline.Time)
					owned := tc.owner == "" && (!tc.staleResource || request != cosmosstorageutils.OperationRequestDelete)
					readsCS := (!expires || request != cosmosstorageutils.OperationRequestDelete) && owned && tc.terminal == "" && !tc.absent && !tc.noCSID && !tc.prerequisiteGate
					if readsCS {
						csEA, err := arohcpv1alpha1.NewExternalAuth().
							Status(arohcpv1alpha1.NewExternalAuthStatus().State(arohcpv1alpha1.NewExternalAuthState().Value("uninstalling"))).Build()
						require.NoError(t, err)
						var getErr error
						if tc.csError {
							getErr = csErr
						}
						cs.EXPECT().GetExternalAuth(gomock.Any(), fixture.ExternalAuthInternalID).Return(csEA, getErr)
					}
					var syncer interface {
						SynchronizeOperation(context.Context, controllerutils.OperationKey) error
					}
					switch request {
					case cosmosstorageutils.OperationRequestCreate:
						syncer = &operationExternalAuthCreate{
							clock:                  clock,
							resourcesDBClient:      client,
							activeOperationsLister: operationLister,
							externalAuthLister:     eaLister,
							clusterServiceClient:   cs,
							notificationClient:     server.Client(),
						}
					case cosmosstorageutils.OperationRequestUpdate:
						syncer = &operationExternalAuthUpdate{
							clock:                  clock,
							resourcesDBClient:      client,
							activeOperationsLister: operationLister,
							externalAuthLister:     eaLister,
							clusterServiceClient:   cs,
							readDesireLister:       &kubeapplierlistertesting.SliceReadDesireLister{},
							notificationClient:     server.Client(),
						}
					case cosmosstorageutils.OperationRequestDelete:
						syncer = &operationExternalAuthDelete{
							clock:                clock,
							resourcesDBClient:    client,
							clusterServiceClient: cs,
							notificationClient:   server.Client(),
						}
					}
					err = syncer.SynchronizeOperation(ctx, fixture.OperationKey())
					switch {
					case readsCS && tc.csError && !expires:
						require.ErrorIs(t, err, csErr)
					case tc.transactionError == writeErr:
						require.ErrorIs(t, err, writeErr)
					default:
						require.NoError(t, err)
					}

					wantStatus := operation.Status
					if tc.terminal == "" && owned && tc.transactionError == nil {
						switch {
						case tc.absent && request == cosmosstorageutils.OperationRequestDelete:
							wantStatus = coreapi.ProvisioningStateSucceeded
						case tc.prerequisiteGate && request != cosmosstorageutils.OperationRequestDelete:
						case readsCS && !tc.csError && request == cosmosstorageutils.OperationRequestCreate:
							wantStatus = coreapi.ProvisioningStateSucceeded
						case expires && !tc.absent:
							wantStatus = coreapi.ProvisioningStateFailed
						case tc.noCSID && request == cosmosstorageutils.OperationRequestUpdate:
							wantStatus = coreapi.ProvisioningStateUpdating
						case readsCS && !tc.csError:
							switch request {
							case cosmosstorageutils.OperationRequestCreate:
								wantStatus = coreapi.ProvisioningStateSucceeded
							case cosmosstorageutils.OperationRequestUpdate:
								wantStatus = coreapi.ProvisioningStateUpdating
							case cosmosstorageutils.OperationRequestDelete:
								wantStatus = coreapi.ProvisioningStateDeleting
							}
						}
					}
					got, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
					require.NoError(t, err)
					assert.Equal(t, wantStatus, got.Status)
					changed := got.Status != operation.Status
					if changed && got.Status == coreapi.ProvisioningStateFailed {
						require.NotNil(t, got.Error)
						assert.Equal(t, coreapi.CloudErrorCodeDeadlineExceeded, got.Error.Code)
						assert.Contains(t, got.Error.Message, "did not complete within the allowed time.")
					} else {
						assert.Equal(t, operation.Error, got.Error)
					}
					if changed {
						assert.True(t, now.Equal(got.LastTransitionTime), "transition time must use the controller clock")
					}
					if changed && got.Status.IsTerminal() {
						assert.EqualValues(t, 1, notifications.Load())
						assert.Empty(t, got.NotificationURI)
						// Reconciliation cannot rewrite or notify a terminal operation again.
						require.NoError(t, syncer.SynchronizeOperation(ctx, fixture.OperationKey()))
						assert.EqualValues(t, 1, notifications.Load())
					} else {
						assert.Zero(t, notifications.Load())
						assert.Equal(t, server.URL, got.NotificationURI)
					}
					if tc.absent {
						return
					}
					gotEA, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).ExternalAuth(operationtesting.TestClusterName).Get(ctx, operationtesting.TestExternalAuthName)
					require.NoError(t, err)
					wantResourceStatus := ea.Properties.ProvisioningState
					wantOwner := ea.ServiceProviderProperties.ActiveOperationID
					if changed && !tc.staleResource {
						wantResourceStatus = wantStatus
						if wantStatus.IsTerminal() {
							wantOwner = ""
						}
					}
					assert.Equal(t, wantResourceStatus, gotEA.Properties.ProvisioningState)
					assert.Equal(t, wantOwner, gotEA.ServiceProviderProperties.ActiveOperationID)
					// Timeout leaves deletion progress intact for the independent cleanup controllers.
					assert.True(t, ea.ServiceProviderProperties.DeletionTimestamp.Equal(gotEA.ServiceProviderProperties.DeletionTimestamp), "deletion timestamp must remain unchanged")
					assert.True(t, ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Equal(gotEA.ServiceProviderProperties.ClusterServiceDeletionTimestamp), "CS deletion timestamp must remain unchanged")
					assert.Equal(t, ea.ServiceProviderProperties.ClusterServiceID, gotEA.ServiceProviderProperties.ClusterServiceID)
				})
			}
		})
	}
}

type externalAuthTimeoutErrorDB struct {
	corecosmosstorage.ResourcesDBClient
	err error
}

func (db *externalAuthTimeoutErrorDB) Operations(subscriptionID string) corecosmosstorage.OperationCRUD {
	return &externalAuthTimeoutErrorOperationCRUD{OperationCRUD: db.ResourcesDBClient.Operations(subscriptionID), err: db.err}
}

type externalAuthTimeoutErrorOperationCRUD struct {
	corecosmosstorage.OperationCRUD
	err error
}

func (crud *externalAuthTimeoutErrorOperationCRUD) AddReplaceToTransaction(context.Context, cosmosstorageutils.DBTransaction, *coreapi.Operation, *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return "", crud.err
}
