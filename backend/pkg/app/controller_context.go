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

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
)

type ControllerContext = controllerconfig.ControllerContext

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
		AsyncOperationNotificationClient:                    http.DefaultClient,
		BackendInformers:                                    backendInformers,
		FleetInformers:                                      fleetInformers,
		UnionKubeApplierInformers:                           unionKubeApplierInformers,
		UnionKubeApplierInformersController:                 unionKubeApplierInformersController,
		VirtualMachineResourceSKUsCachedReaderController:    virtualMachineResourceSKUsCachedReaderController,
	}
}
