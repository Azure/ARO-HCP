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

package app

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	clusterbackups "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/backups"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type ControllerContext struct {
	AzureLocation                                       string
	BackendIdentityAzureCachedReaders                   *cachedreader.BackendIdentityAzureCachedReaders
	BackupConfig                                        *clusterbackups.BackupConfig
	BillingDBClient                                     billingcosmosstorage.BillingDBClient
	CheckAccessV2ClientBuilder                          azureclient.CheckAccessV2ClientBuilder
	CloudEnvironment                                    *azureconfig.AzureCloudEnvironment
	ClusterScopedIdentitiesConfig                       *internalazure.ClusterScopedIdentitiesConfig
	ClustersServiceClient                               ocm.ClusterServiceClientSpec
	FPAClientBuilder                                    azureclient.FirstPartyApplicationClientBuilder
	FPAMIDataplaneClientBuilder                         azureclient.FPAMIDataplaneClientBuilder
	FleetDBClient                                       fleetcosmosstorage.FleetDBClient
	HasRealFPA                                          bool
	KubeApplierDBClients                                kubeappliercosmosstorage.KubeApplierDBClients
	MIDataplaneBasedIdentityAccessTokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder
	MaestroSourceEnvironmentIdentifier                  string
	MetricsRegisterer                                   prometheus.Registerer
	ResourcesDBClient                                   corecosmosstorage.ResourcesDBClient
	SMIClientBuilder                                    azureclient.ServiceManagedIdentityClientBuilder
	Clock                                               utilsclock.PassiveClock
	HTTPClient                                          *http.Client

	BackendInformers                                 coreinformers.BackendInformers
	FleetInformers                                   fleetinformers.FleetInformers
	UnionKubeApplierInformers                        *unionkubeapplierinformers.UnionKubeApplierInformers
	UnionKubeApplierInformersController              *unionkubeapplierinformers.UnionKubeApplierInformersController
	VirtualMachineResourceSKUsCachedReaderController *cachedreader.FPAVirtualMachineResourceSKUsCachedReaderController
	SubscriptionLister                               corelisters.SubscriptionLister
	ActiveOperationInformer                          cache.SharedIndexInformer
	ActiveOperationLister                            corelisters.ActiveOperationLister
	ManagementClusterInformer                        cache.SharedIndexInformer
	ManagementClusterLister                          fleetlisters.ManagementClusterLister
	UnionReadDesireLister                            kubeapplierlisters.ReadDesireLister
	UnionApplyDesireLister                           kubeapplierlisters.ApplyDesireLister
	ClusterInformer                                  cache.SharedIndexInformer
	ClusterLister                                    corelisters.ClusterLister
	ServiceProviderClusterInformer                   cache.SharedIndexInformer
	BillingLister                                    corelisters.BillingLister
	NodePoolInformer                                 cache.SharedIndexInformer
	NodePoolLister                                   corelisters.NodePoolLister
	ExternalAuthInformer                             cache.SharedIndexInformer
	ExternalAuthLister                               corelisters.ExternalAuthLister
	ControllerLister                                 corelisters.ControllerLister
	ServiceProviderClusterLister                     corelisters.ServiceProviderClusterLister
	ServiceProviderNodePoolLister                    corelisters.ServiceProviderNodePoolLister
	ManagementClusterSchedulingLister                fleetlisters.ManagementClusterSchedulingLister
}

func (b *Backend) newControllerContext(ctx context.Context) ControllerContext {
	backendInformers := coreinformers.NewBackendInformers(ctx,
		b.options.ResourcesDBClient.ResourcesGlobalListers(),
		b.options.ResourcesDBClient,
		b.options.BillingDBClient.BillingGlobalListers(),
	)
	fleetInformers := fleetinformers.NewFleetInformers(ctx, b.options.FleetDBClient.GlobalListers(), b.options.FleetDBClient)
	managementClusterInformer, managementClusterLister := fleetInformers.ManagementClusters()
	unionKubeApplierInformersController := unionkubeapplierinformers.NewUnionKubeApplierInformersController(
		managementClusterInformer,
		managementClusterLister,
		unionkubeapplierinformers.NewKubeApplierInformerFactory(b.options.KubeApplierDBClients, nil),
	)
	unionKubeApplierInformers := unionKubeApplierInformersController.Union()
	_, unionReadDesireLister := unionKubeApplierInformers.ReadDesires()
	_, unionApplyDesireLister := unionKubeApplierInformers.ApplyDesires()
	_, subscriptionLister := backendInformers.Subscriptions()
	activeOperationInformer, activeOperationLister := backendInformers.ActiveOperations()
	clusterInformer, clusterLister := backendInformers.Clusters()
	serviceProviderClusterInformer, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, billingLister := backendInformers.BillingDocs()
	nodePoolInformer, nodePoolLister := backendInformers.NodePools()
	externalAuthInformer, externalAuthLister := backendInformers.ExternalAuths()
	_, controllerLister := backendInformers.Controllers()
	_, serviceProviderNodePoolLister := backendInformers.ServiceProviderNodePools()
	_, managementClusterSchedulingLister := fleetInformers.ManagementClusterSchedulings()
	virtualMachineResourceSKUsCachedReaderController := cachedreader.NewFPAVirtualMachineResourceSKUsCachedReaderController(
		b.options.FPAClientBuilder,
		b.options.AzureLocation,
	)

	return ControllerContext{
		AzureLocation:                     b.options.AzureLocation,
		BackendIdentityAzureCachedReaders: b.options.BackendIdentityAzureCachedReaders,
		BackupConfig:                      b.options.BackupConfig,
		BillingDBClient:                   b.options.BillingDBClient,
		CheckAccessV2ClientBuilder:        b.options.CheckAccessV2ClientBuilder,
		CloudEnvironment:                  b.options.CloudEnvironment,
		ClusterScopedIdentitiesConfig:     b.options.ClusterScopedIdentitiesConfig,
		ClustersServiceClient:             b.options.ClustersServiceClient,
		FPAClientBuilder:                  b.options.FPAClientBuilder,
		FPAMIDataplaneClientBuilder:       b.options.FPAMIDataplaneClientBuilder,
		FleetDBClient:                     b.options.FleetDBClient,
		HasRealFPA:                        b.options.HasRealFPA,
		KubeApplierDBClients:              b.options.KubeApplierDBClients,
		MIDataplaneBasedIdentityAccessTokenRetrieverBuilder: b.options.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder,
		MaestroSourceEnvironmentIdentifier:                  b.options.MaestroSourceEnvironmentIdentifier,
		MetricsRegisterer:                                   b.options.MetricsRegisterer,
		ResourcesDBClient:                                   b.options.ResourcesDBClient,
		SMIClientBuilder:                                    b.options.SMIClientBuilder,
		Clock:                                               b.clock,
		HTTPClient:                                          http.DefaultClient,
		BackendInformers:                                    backendInformers,
		FleetInformers:                                      fleetInformers,
		UnionKubeApplierInformers:                           unionKubeApplierInformers,
		UnionKubeApplierInformersController:                 unionKubeApplierInformersController,
		VirtualMachineResourceSKUsCachedReaderController: virtualMachineResourceSKUsCachedReaderController,
		SubscriptionLister:                subscriptionLister,
		ActiveOperationInformer:           activeOperationInformer,
		ActiveOperationLister:             activeOperationLister,
		ManagementClusterInformer:         managementClusterInformer,
		ManagementClusterLister:           managementClusterLister,
		UnionReadDesireLister:             unionReadDesireLister,
		UnionApplyDesireLister:            unionApplyDesireLister,
		ClusterInformer:                   clusterInformer,
		ClusterLister:                     clusterLister,
		ServiceProviderClusterInformer:    serviceProviderClusterInformer,
		BillingLister:                     billingLister,
		NodePoolInformer:                  nodePoolInformer,
		NodePoolLister:                    nodePoolLister,
		ExternalAuthInformer:              externalAuthInformer,
		ExternalAuthLister:                externalAuthLister,
		ControllerLister:                  controllerLister,
		ServiceProviderClusterLister:      serviceProviderClusterLister,
		ServiceProviderNodePoolLister:     serviceProviderNodePoolLister,
		ManagementClusterSchedulingLister: managementClusterSchedulingLister,
	}
}
