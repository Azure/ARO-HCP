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

package azureresourcesinformers

import (
	"context"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// AzureResourcesInformers bundles one SharedIndexInformer per AzureResources
// type plus the matching azureresourceslisters. It mirrors fleetinformers.FleetInformers:
// a sibling aggregate for its own Cosmos container, not folded into
// coreinformers.BackendInformers, so a new container never widens that
// interface or its constructor signature.
type AzureResourcesInformers interface {
	DeploymentStackDesires() (cache.SharedIndexInformer, azureresourceslisters.DeploymentStackDesireLister)
	HasSynced() bool
	RunWithContext(ctx context.Context)
}

type azureResourcesInformers struct {
	deploymentStackDesireInformer cache.SharedIndexInformer
	deploymentStackDesireLister   azureresourceslisters.DeploymentStackDesireLister
}

func (a *azureResourcesInformers) DeploymentStackDesires() (cache.SharedIndexInformer, azureresourceslisters.DeploymentStackDesireLister) {
	return a.deploymentStackDesireInformer, a.deploymentStackDesireLister
}

func (a *azureResourcesInformers) HasSynced() bool {
	return a.deploymentStackDesireInformer.HasSynced()
}

// NewAzureResourcesInformers creates AzureResourcesInformers with default relist durations.
func NewAzureResourcesInformers(ctx context.Context, globalListers azureresourcescosmosstorage.AzureResourcesGlobalListers, azureResourcesDBClient azureresourcescosmosstorage.AzureResourcesDBClient) AzureResourcesInformers {
	return NewAzureResourcesInformersWithRelistDuration(ctx, globalListers, azureResourcesDBClient, nil)
}

func NewAzureResourcesInformersWithRelistDuration(ctx context.Context, globalListers azureresourcescosmosstorage.AzureResourcesGlobalListers, azureResourcesDBClient azureresourcescosmosstorage.AzureResourcesDBClient, relistDuration *time.Duration) AzureResourcesInformers {
	deploymentStackDesireRelistDuration := DeploymentStackDesireRelistDuration
	if relistDuration != nil {
		deploymentStackDesireRelistDuration = *relistDuration
	}

	ret := &azureResourcesInformers{}
	ret.deploymentStackDesireInformer = NewDeploymentStackDesireInformerWithRelistDuration(globalListers.DeploymentStackDesires(), azureResourcesDBClient, deploymentStackDesireRelistDuration)
	ret.deploymentStackDesireLister = azureresourceslisters.NewDeploymentStackDesireLister(ret.deploymentStackDesireInformer.GetIndexer())

	return ret
}

func (a *azureResourcesInformers) RunWithContext(ctx context.Context) {
	defer utilruntime.HandleCrash()
	logger := utils.LoggerFromContext(ctx)
	logger.Info("starting azure resources informers")
	defer logger.Info("stopped azure resources informers")

	a.deploymentStackDesireInformer.RunWithContext(ctx)
}
