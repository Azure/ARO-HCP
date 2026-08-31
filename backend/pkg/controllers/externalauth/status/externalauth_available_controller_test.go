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

package status

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	kubeapplierlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

const (
	testComponentName      = "console"
	testComponentNamespace = "openshift-console"
)

func newTestExternalAuthForAvailable(opts ...func(*coreapi.HCPOpenShiftClusterExternalAuth)) *coreapi.HCPOpenShiftClusterExternalAuth {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/externalAuths/" + statusutils.TestExternalAuthName,
	))
	ea := &coreapi.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ProxyResource: coreapi.ProxyResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: statusutils.TestExternalAuthName,
				Type: resourceID.ResourceType.String(),
			},
		},
		Properties: coreapi.HCPOpenShiftClusterExternalAuthProperties{
			Clients: []coreapi.ExternalAuthClientProfile{
				{
					Component: coreapi.ExternalAuthClientComponentProfile{
						Name:                testComponentName,
						AuthClientNamespace: testComponentNamespace,
					},
				},
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
			ClusterServiceID: ptrTo(metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))),
		},
	}
	for _, opt := range opts {
		opt(ea)
	}
	return ea
}

func newHostedClusterReadDesire(t *testing.T, hc *v1beta1.HostedCluster) *kubeapplierapi.ReadDesire {
	t.Helper()
	raw, err := json.Marshal(hc)
	require.NoError(t, err)
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
			statusutils.TestSubscriptionID, statusutils.TestResourceGroupName, statusutils.TestClusterName,
			kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster)))
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: kubeapplierapi.ReadDesireStatus{
			Conditions: []metav1.Condition{
				{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: kubeapplierapi.ConditionReasonNoErrors},
			},
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

func ptrTo[T any](v T) *T { return &v }

func TestMatchingOIDCClientStatuses(t *testing.T) {
	observed := []configv1.OIDCClientStatus{
		{ComponentName: "console", ComponentNamespace: "openshift-console"},
		{ComponentName: "cli", ComponentNamespace: "openshift-console"},
	}

	t.Run("no external auth clients returns all observed", func(t *testing.T) {
		ea := newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
			ea.Properties.Clients = nil
		})
		got := matchingOIDCClientStatuses(ea, observed)
		assert.Equal(t, observed, got)
	})

	t.Run("filters to matching component name and namespace", func(t *testing.T) {
		ea := newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
			ea.Properties.Clients = []coreapi.ExternalAuthClientProfile{
				{
					Component: coreapi.ExternalAuthClientComponentProfile{
						Name:                "console",
						AuthClientNamespace: "openshift-console",
					},
				},
			}
		})
		got := matchingOIDCClientStatuses(ea, observed)
		require.Len(t, got, 1)
		assert.Equal(t, "console", got[0].ComponentName)
	})

	t.Run("case-insensitive matching", func(t *testing.T) {
		ea := newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
			ea.Properties.Clients = []coreapi.ExternalAuthClientProfile{
				{
					Component: coreapi.ExternalAuthClientComponentProfile{
						Name:                "Console",
						AuthClientNamespace: "OpenShift-Console",
					},
				},
			}
		})
		got := matchingOIDCClientStatuses(ea, observed)
		require.Len(t, got, 1)
		assert.Equal(t, "console", got[0].ComponentName)
	})

	t.Run("no match returns empty", func(t *testing.T) {
		ea := newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
			ea.Properties.Clients = []coreapi.ExternalAuthClientProfile{
				{
					Component: coreapi.ExternalAuthClientComponentProfile{
						Name:                "nonexistent",
						AuthClientNamespace: "nonexistent-ns",
					},
				},
			}
		})
		got := matchingOIDCClientStatuses(ea, observed)
		assert.Empty(t, got)
	})
}

func TestExternalAuthAvailableController_SyncOnce(t *testing.T) {
	parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
	))

	tests := []struct {
		name string

		externalAuth  *coreapi.HCPOpenShiftClusterExternalAuth
		hostedCluster *v1beta1.HostedCluster

		expectNoWrite bool
		expectStatus  metav1.ConditionStatus
		expectReason  string
		expectMessage string
	}{
		{
			name: "skip when external auth is being deleted",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				now := metav1.Now()
				ea.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			expectNoWrite: true,
		},
		{
			name: "skip when external auth has no ClusterServiceID",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.ClusterServiceID = nil
			}),
			expectNoWrite: true,
		},
		{
			name:          "HostedCluster not found -> Available: False, Reason: HostedClusterNotReady",
			externalAuth:  newTestExternalAuthForAvailable(),
			hostedCluster: nil,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  coreapi.ExternalAuthReasonHostedClusterNotReady,
			expectMessage: "Waiting for HostedCluster to be observed",
		},
		{
			name:         "HostedCluster found, no Configuration -> Available: Unknown, Reason: HostedClusterNotReady",
			externalAuth: newTestExternalAuthForAvailable(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: nil,
				},
			},
			expectStatus:  metav1.ConditionUnknown,
			expectReason:  coreapi.ExternalAuthReasonHostedClusterNotReady,
			expectMessage: "HostedCluster authentication status not yet available",
		},
		{
			name:         "OIDCClientStatus Available: True, Reason: OIDCConfigAvailable -> Available: True",
			externalAuth: newTestExternalAuthForAvailable(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{
								{
									ComponentName:      testComponentName,
									ComponentNamespace: testComponentNamespace,
									Conditions: []metav1.Condition{
										{Type: "Available", Status: metav1.ConditionTrue, Reason: coreapi.HCReasonOIDCConfigAvailable},
										{Type: "Degraded", Status: metav1.ConditionFalse, Reason: coreapi.HCReasonOIDCConfigAvailable},
										{Type: "Progressing", Status: metav1.ConditionFalse, Reason: coreapi.HCReasonOIDCConfigAvailable},
									},
								},
							},
						},
					},
				},
			},
			expectStatus: metav1.ConditionTrue,
			expectReason: coreapi.ExternalAuthReasonOIDCConfigAvailable,
		},
		{
			name:         "OIDCClientStatus Degraded: True, Reason: OIDCClientSecretGet -> Available: False, Reason: AwaitingSecret",
			externalAuth: newTestExternalAuthForAvailable(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{
								{
									ComponentName:      testComponentName,
									ComponentNamespace: testComponentNamespace,
									Conditions: []metav1.Condition{
										{Type: "Available", Status: metav1.ConditionFalse, Reason: "SomeReason"},
										{Type: "Degraded", Status: metav1.ConditionTrue, Reason: coreapi.HCReasonOIDCClientSecretGet, Message: "secret not found"},
										{Type: "Progressing", Status: metav1.ConditionFalse, Reason: "SomeReason"},
									},
								},
							},
						},
					},
				},
			},
			expectStatus:  metav1.ConditionFalse,
			expectReason:  coreapi.ExternalAuthReasonAwaitingSecret,
			expectMessage: "The external auth provider is waiting for the client secret to be created in the openshift-config namespace",
		},
		{
			name:         "OIDCClientStatus Degraded: True with other reason -> Available: False, forward HC reason/message",
			externalAuth: newTestExternalAuthForAvailable(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{
								{
									ComponentName:      testComponentName,
									ComponentNamespace: testComponentNamespace,
									Conditions: []metav1.Condition{
										{Type: "Available", Status: metav1.ConditionFalse, Reason: "SomeReason"},
										{Type: "Degraded", Status: metav1.ConditionTrue, Reason: "OtherDegraded", Message: "something else is wrong"},
										{Type: "Progressing", Status: metav1.ConditionFalse, Reason: "SomeReason"},
									},
								},
							},
						},
					},
				},
			},
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "OtherDegraded",
			expectMessage: "something else is wrong",
		},
		{
			name:         "OIDCClientStatus Available: False, no Degraded -> Available: False, Reason: AwaitingSecret",
			externalAuth: newTestExternalAuthForAvailable(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{
								{
									ComponentName:      testComponentName,
									ComponentNamespace: testComponentNamespace,
									Conditions: []metav1.Condition{
										{Type: "Available", Status: metav1.ConditionFalse, Reason: "SomeReason", Message: "not available yet"},
										{Type: "Degraded", Status: metav1.ConditionFalse, Reason: "SomeReason"},
									},
								},
							},
						},
					},
				},
			},
			expectStatus:  metav1.ConditionFalse,
			expectReason:  coreapi.ExternalAuthReasonAwaitingSecret,
			expectMessage: "not available yet",
		},
		{
			name:         "no matching OIDCClientStatus -> Available: False, Reason: AwaitingSecret",
			externalAuth: newTestExternalAuthForAvailable(),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{
								{
									ComponentName:      "other-component",
									ComponentNamespace: "other-namespace",
									Conditions: []metav1.Condition{
										{Type: "Available", Status: metav1.ConditionTrue, Reason: coreapi.HCReasonOIDCConfigAvailable},
									},
								},
							},
						},
					},
				},
			},
			expectStatus:  metav1.ConditionFalse,
			expectReason:  coreapi.ExternalAuthReasonAwaitingSecret,
			expectMessage: "OIDC client status not yet reported by the hosted cluster",
		},
		{
			name: "multi-client: worst condition wins when one Available and one AwaitingSecret",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "console",
							AuthClientNamespace: "openshift-console",
						},
					},
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "cli",
							AuthClientNamespace: "openshift-console",
						},
					},
				}
			}),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{
								{
									ComponentName:      "console",
									ComponentNamespace: "openshift-console",
									Conditions: []metav1.Condition{
										{Type: "Available", Status: metav1.ConditionTrue, Reason: coreapi.HCReasonOIDCConfigAvailable},
										{Type: "Degraded", Status: metav1.ConditionFalse, Reason: coreapi.HCReasonOIDCConfigAvailable},
									},
								},
								{
									ComponentName:      "cli",
									ComponentNamespace: "openshift-console",
									Conditions: []metav1.Condition{
										{Type: "Available", Status: metav1.ConditionFalse, Reason: "SomeReason"},
										{Type: "Degraded", Status: metav1.ConditionTrue, Reason: coreapi.HCReasonOIDCClientSecretGet, Message: "secret not found"},
									},
								},
							},
						},
					},
				},
			},
			expectStatus:  metav1.ConditionFalse,
			expectReason:  coreapi.ExternalAuthReasonAwaitingSecret,
			expectMessage: "The external auth provider is waiting for the client secret to be created in the openshift-config namespace",
		},
		{
			name: "no-op when UserFacingConditions already match",
			externalAuth: newTestExternalAuthForAvailable(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Status.UserFacingConditions = []metav1.Condition{
					{
						Type:    coreapi.ExternalAuthAvailableCondition,
						Status:  metav1.ConditionTrue,
						Reason:  coreapi.ExternalAuthReasonOIDCConfigAvailable,
						Message: "",
					},
				}
			}),
			hostedCluster: &v1beta1.HostedCluster{
				Status: v1beta1.HostedClusterStatus{
					Configuration: &v1beta1.ConfigurationStatus{
						Authentication: configv1.AuthenticationStatus{
							OIDCClients: []configv1.OIDCClientStatus{
								{
									ComponentName:      testComponentName,
									ComponentNamespace: testComponentNamespace,
									Conditions: []metav1.Condition{
										{Type: "Available", Status: metav1.ConditionTrue, Reason: coreapi.HCReasonOIDCConfigAvailable},
										{Type: "Degraded", Status: metav1.ConditionFalse, Reason: coreapi.HCReasonOIDCConfigAvailable},
									},
								},
							},
						},
					},
				},
			},
			expectNoWrite: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			parentCluster := &coreapi.HCPOpenShiftCluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   parentClusterID,
					PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{ID: parentClusterID, Name: statusutils.TestClusterName, Type: parentClusterID.ResourceType.String()},
				},
			}

			seed := []any{parentCluster, tc.externalAuth}
			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			var readDesireLister kubeapplierlistertesting.SliceReadDesireLister
			if tc.hostedCluster != nil {
				readDesireLister.Desires = []*kubeapplierapi.ReadDesire{
					newHostedClusterReadDesire(t, tc.hostedCluster),
				}
			}

			syncer := &externalAuthAvailableController{
				externalAuthLister: &corelistertesting.DBExternalAuthLister{ResourcesDBClient: mockDB},
				readDesireLister:   &readDesireLister,
				resourcesDBClient:  mockDB,
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPExternalAuthKey{
				SubscriptionID:      statusutils.TestSubscriptionID,
				ResourceGroupName:   statusutils.TestResourceGroupName,
				HCPClusterName:      statusutils.TestClusterName,
				HCPExternalAuthName: statusutils.TestExternalAuthName,
			})
			require.NoError(t, err)

			updated, err := mockDB.HCPClusters(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName).ExternalAuth(statusutils.TestClusterName).Get(ctx, statusutils.TestExternalAuthName)
			require.NoError(t, err)

			if tc.expectNoWrite {
				cond := apimeta.FindStatusCondition(updated.Status.UserFacingConditions, coreapi.ExternalAuthAvailableCondition)
				if len(tc.externalAuth.Status.UserFacingConditions) == 0 {
					assert.Nil(t, cond, "expected no Available condition to be set")
				} else {
					existing := apimeta.FindStatusCondition(tc.externalAuth.Status.UserFacingConditions, coreapi.ExternalAuthAvailableCondition)
					require.NotNil(t, cond, "expected existing Available condition to be preserved")
					assert.Equal(t, existing.Status, cond.Status, "status should not change")
					assert.Equal(t, existing.Reason, cond.Reason, "reason should not change")
				}
				return
			}

			cond := apimeta.FindStatusCondition(updated.Status.UserFacingConditions, coreapi.ExternalAuthAvailableCondition)
			require.NotNil(t, cond, "controller must set the Available condition on the external auth")
			assert.Equal(t, tc.expectStatus, cond.Status, "status")
			assert.Equal(t, tc.expectReason, cond.Reason, "reason")
			assert.Equal(t, tc.expectMessage, cond.Message, "message")
		})
	}
}
