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

package launch

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
	"github.com/Azure/ARO-HCP/backend/pkg/app"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/backups"
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

const (
	quotaSDKSeparator = "--------------------------------------------------------------------------------"

	// The Hypershift AllMachinesReady condition message for one machine that hit the
	// Azure vCPU quota, in the shape the Azure SDK, CAPZ and Hypershift produce.
	quotaMachinesNotReady = "1 of 1 machines are not ready\n" +
		"Machine pool-1-abc: Failed: virtualmachine failed to create or update. err: PUT https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm\n" +
		quotaSDKSeparator + "\nRESPONSE 409: 409 Conflict\nERROR CODE: OperationNotAllowed\n" + quotaSDKSeparator + "\n" +
		"{\n  \"error\": {\n    \"code\": \"OperationNotAllowed\",\n    \"message\": \"Operation could not be completed as it results in exceeding approved Total Regional Cores quota.\"\n  }\n}\n" +
		quotaSDKSeparator + "\n"

	platformFailureText = "cluster service internal failure text"
)

// A node pool create whose Azure vCPU quota is exhausted when the completion
// deadline passes must end as QuotaExceeded, not DeadlineExceeded, with a clean
// customer message. It runs the whole backend: operation controller, Hypershift
// mirror read through the union kube-applier informers, merge and persistence.
func TestBackendNodePoolCreateQuotaExceededAtDeadline(t *testing.T) {
	tests := []struct {
		name string
		// csState and csMessage are what Cluster Service reports for the node pool.
		csState   string
		csMessage string
	}{
		{
			name:    "quota alone",
			csState: string(operationbase.NodePoolStateInstalling),
		},
		{
			// Customer errors always win: the platform failure text is withheld from the
			// persisted error and kept in the backend log.
			name:      "quota with a concurrent platform failure",
			csState:   string(operationbase.NodePoolStateError),
			csMessage: platformFailureText,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {
				runQuotaScenario(t, withMock, tt.csState, tt.csMessage, true)
			})
		})
	}
}

// Quota that persists past the fail fast window fails the operation as QuotaExceeded
// without any deadline involved. The backend runs on a fake clock that the test
// advances after the first sync has started the window.
func TestBackendNodePoolCreateQuotaExceededAfterFailFastWindow(t *testing.T) {
	integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {
		runQuotaScenario(t, withMock, string(operationbase.NodePoolStateInstalling), "", false)
	})
}

// runQuotaScenario seeds a node pool create stuck on Azure vCPU quota and waits for the
// operation to fail. With pastDeadline the create deadline already passed; otherwise the
// test advances a fake clock past the fail fast window.
func runQuotaScenario(t *testing.T, withMock bool, csState, csMessage string, pastDeadline bool) {
	defer integrationutils.VerifyNoNewGoLeaks(t)

	ctx, cancel := context.WithCancel(t.Context())
	ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))
	defer cancel()

	var (
		storageInfo integrationutils.StorageIntegrationTestInfo
		err         error
	)
	if withMock {
		storageInfo, err = integrationutils.NewMockCosmosFromTestingEnv(ctx, t)
	} else {
		storageInfo, err = integrationutils.NewCosmosFromTestingEnv(ctx, t)
	}
	require.NoError(t, err)

	fixture := operationtesting.NewNodePoolTestFixture()

	clusterServiceMock := integrationutils.NewClusterServiceMock(t, storageInfo.GetArtifactDir())
	cs := clusterServiceMock.MockClusterServiceClient
	cs.EXPECT().ListProvisionShards().Return(ocm.NewSimpleProvisionShardListIterator(nil, nil)).AnyTimes()
	cs.EXPECT().GetClusterResources(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	statusBuilder := arohcpv1alpha1.NewNodePoolStatus().
		State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(csState))
	if csMessage != "" {
		statusBuilder = statusBuilder.Message(csMessage)
	}
	nodePoolStatus, err := statusBuilder.Build()
	require.NoError(t, err)
	cs.EXPECT().GetNodePoolStatus(gomock.Any(), fixture.NodePoolInternalID).Return(nodePoolStatus, nil).AnyTimes()

	cleanupCtx := utils.ContextWithLogger(context.Background(), integrationutils.DefaultLogger(t))
	defer storageInfo.Cleanup(cleanupCtx)
	defer clusterServiceMock.Cleanup(cleanupCtx)

	oldDefaultServeMux := http.DefaultServeMux
	http.DefaultServeMux = http.NewServeMux()
	defer func() { http.DefaultServeMux = oldDefaultServeMux }()

	// Resources: cluster, node pool past its create deadline, and the create operation.
	resourcesDBClient := storageInfo.ResourcesDBClient()
	cluster := fixture.NewCluster()
	_, err = resourcesDBClient.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	nodePool := fixture.NewNodePool()
	nodePool.Properties.Replicas = 2
	if pastDeadline {
		deadline := metav1.NewTime(time.Now().Add(-time.Hour))
		nodePool.ServiceProviderProperties.CreateOperationCompletionDeadline = &deadline
	}
	_, err = resourcesDBClient.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).
		NodePools(operationtesting.TestClusterName).Create(ctx, nodePool, nil)
	require.NoError(t, err)

	operation := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)
	_, err = resourcesDBClient.Operations(operationtesting.TestSubscriptionID).Create(ctx, operation, nil)
	require.NoError(t, err)

	// Management cluster, and the Hypershift NodePool mirrored from it with the quota failure.
	const stampIdentifier = "s1"
	managementClusterResourceID := metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(stampIdentifier))
	fleetClient := fleetcosmosstoragetesting.NewMockFleetDBClient()
	createQuotaTestManagementCluster(ctx, t, fleetClient, stampIdentifier)

	mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	readDesires, err := mockKubeApplier.ReadDesiresForNodePool(
		operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName, operationtesting.TestClusterName, operationtesting.TestNodePoolName)
	require.NoError(t, err)
	_, err = readDesires.Create(ctx, newQuotaFailingReadDesire(t, managementClusterResourceID), nil)
	require.NoError(t, err)
	kubeApplierClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	kubeApplierClients.Register(managementClusterResourceID, mockKubeApplier)

	cloudEnvironment, err := azureconfig.NewAzureCloudEnvironment(apisconfigv1.AzurePublicCloud, nil)
	require.NoError(t, err)
	registry := prometheus.NewRegistry()
	metricsListener := newMetricsTestListener(t)
	var clk *clocktesting.FakePassiveClock
	backendOptions := &app.BackendOptions{
		AppShortDescriptionName: "backend",
		AppVersion:              "test",
		AzureLocation:           "fake-location",
		LeaderElectionLock:      newFakeLeaderElectionLock("quota-test"),
		StorageFactory: &metricsTestStorageFactory{
			resources:   resourcesDBClient,
			billing:     storageInfo.BillingDBClient(),
			fleet:       fleetClient,
			kubeApplier: kubeApplierClients,
		},
		ClustersServiceClient:              cs,
		MetricsRegisterer:                  registry,
		MetricsGatherer:                    registry,
		MetricsServerListenAddress:         metricsListener.Addr().String(),
		MetricsServerListener:              metricsListener,
		TracerProviderShutdownFunc:         func(context.Context) error { return nil },
		MaestroSourceEnvironmentIdentifier: "test",
		CloudEnvironment:                   cloudEnvironment,
		BackupConfig: &backups.BackupConfig{
			BackupScheduleState:  coreapi.BackupScheduleStateEnabled,
			BackupCadenceProfile: backups.BackupCadenceProduction,
		},
		HardcodedIdentity: &azureclient.HardcodedIdentity{
			ClientID:     "test-client-id",
			ClientSecret: "test-client-secret",
			PrincipalID:  "test-principal-id",
			TenantID:     "test-tenant-id",
		},
	}

	if !pastDeadline {
		clk = clocktesting.NewFakePassiveClock(time.Now())
		backendOptions.Clock = clk
	}

	backendErrCh := make(chan error, 1)
	go func() { backendErrCh <- backendOptions.RunBackend(ctx) }()
	defer func() {
		cancel()
		require.NoError(t, <-backendErrCh)
	}()

	if clk != nil {
		// The first sync sees quota and starts the window; the operation is still in progress.
		require.Eventually(t, func() bool {
			op, err := resourcesDBClient.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
			return err == nil && op.Status == coreapi.ProvisioningStateProvisioning
		}, 30*time.Second, 100*time.Millisecond, "operation never reached Provisioning")
		clk.SetTime(clk.Now().Add(6 * time.Minute))
	}

	var failed *coreapi.Operation
	require.Eventually(t, func() bool {
		op, err := resourcesDBClient.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
		if err != nil || op.Status != coreapi.ProvisioningStateFailed {
			return false
		}
		failed = op
		return true
	}, 30*time.Second, 100*time.Millisecond, "operation never reached Failed")

	require.NotNil(t, failed.Error, "failed operation must carry an error body")
	require.Equal(t, coreapi.CloudErrorCodeQuotaExceeded, failed.Error.Code, "quota at the deadline must not end as DeadlineExceeded")
	require.True(t, coreapi.IsCustomerCaused(failed.Error.Code))
	require.Contains(t, failed.Error.Message, "enough Azure compute quota")
	require.Contains(t, failed.Error.Message, "Total Regional Cores quota")
	require.Empty(t, failed.Error.Details)
	for _, leak := range []string{"management.azure.com", "ERROR CODE", "[hypershiftNodePool]", "[clusterServiceNodePoolStatus]", platformFailureText} {
		require.NotContains(t, failed.Error.Message, leak, "customer message must stay clean")
	}
}

func createQuotaTestManagementCluster(ctx context.Context, t *testing.T, fleetClient fleetcosmosstorage.FleetDBClient, stampIdentifier string) {
	t.Helper()

	stampResourceID := metadataapi.Must(fleetapihelpers.ToStampResourceID(stampIdentifier))
	_, err := fleetClient.Stamps().Create(ctx, &fleetapi.Stamp{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: stampResourceID, PartitionKey: strings.ToLower(stampIdentifier)},
	}, nil)
	require.NoError(t, err)

	managementClusterResourceID := metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(stampIdentifier))
	_, err = fleetClient.Stamps().ManagementClusters(stampIdentifier).Create(ctx, &fleetapi.ManagementCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: managementClusterResourceID, PartitionKey: strings.ToLower(stampIdentifier)},
		Spec: fleetapi.ManagementClusterSpec{
			SchedulingPolicy: fleetapi.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleetapi.ManagementClusterStatus{
			AKSResourceID: metadataapi.Must(azcorearm.ParseResourceID(
				"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks-" + stampIdentifier)),
			PublicDNSZoneResourceID: metadataapi.Must(azcorearm.ParseResourceID(
				"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/test.example.com")),
			HostedClustersSecretsKeyVaultURL:                     "https://cx-kv.vault.azure.net/",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://mi-kv.vault.azure.net/",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "c2bde1aa-d904-48cd-a728-9de33e3ddca9",
			ClusterServiceProvisionShardID: ptr.To(metadataapi.Must(metadataapi.NewInternalID(
				"/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee" + stampIdentifier))),
			MaestroConsumerName:            "consumer-" + stampIdentifier,
			MaestroRESTAPIURL:              "http://maestro.maestro.svc.cluster.local:8000",
			MaestroGRPCTarget:              "maestro-grpc.maestro.svc.cluster.local:8090",
			KubeApplierCosmosContainerName: "Manifests-MC-" + stampIdentifier,
		},
	}, nil)
	require.NoError(t, err)
}

func newQuotaFailingReadDesire(t *testing.T, managementClusterResourceID *azcorearm.ResourceID) *kubeapplierapi.ReadDesire {
	t.Helper()

	hsNodePool := &v1beta1.NodePool{
		Spec: v1beta1.NodePoolSpec{Replicas: ptr.To(int32(2))},
		Status: v1beta1.NodePoolStatus{
			Replicas: 2,
			Conditions: []v1beta1.NodePoolCondition{
				{Type: v1beta1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionFalse, Message: "2 of 2 machines are not healthy\nMachine pool-1-abc: NodeProvisioning: waiting\n"},
				{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionFalse, Reason: "Failed", Message: quotaMachinesNotReady},
			},
		},
	}
	raw, err := json.Marshal(hsNodePool)
	require.NoError(t, err)

	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapihelpers.ToNodePoolScopedReadDesireResourceIDString(
			operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName, operationtesting.TestClusterName, operationtesting.TestNodePoolName,
			kubeapplierhelpers.ReadDesireNameReadonlyNodePool)))
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementClusterResourceID.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{ManagementCluster: managementClusterResourceID},
		Status: kubeapplierapi.ReadDesireStatus{
			Conditions: []metav1.Condition{
				{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: kubeapplierapi.ConditionReasonNoErrors},
			},
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}
