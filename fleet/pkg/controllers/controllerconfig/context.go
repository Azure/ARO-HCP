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

package controllerconfig

import (
	"context"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/ARO-HCP/fleet/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type ControllerContext struct {
	FleetDBClient                fleetcosmosstorage.FleetDBClient
	ClustersServiceClient        ocm.ClusterServiceClientSpec
	MaestroConsumerClientFactory maestro.ConsumerClientFactory
	KubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	Region                       string
	AzureCredential              azcore.TokenCredential
	AzureClientOptions           *policy.ClientOptions
	NodePoolProfile              *compute.Profile
	NodePoolZones                []string
	AMWWorkspaceResourceIDs      []string
	AMWScalingPollInterval       time.Duration

	FleetInformers                      fleetinformers.FleetInformers
	UnionKubeApplierInformers           *unionkubeapplierinformers.UnionKubeApplierInformers
	UnionKubeApplierInformersController *unionkubeapplierinformers.UnionKubeApplierInformersController
}

type ControllerRegistration = controllerregistry.Registration[ControllerContext]
type Runnable = controllerregistry.Runnable

// SingleWorker adapts a controller with its own polling loop to the registry.
// The controller retains responsibility for its worker count and scheduling.
type SingleWorker struct {
	Controller interface{ Run(context.Context) }
}

func (c SingleWorker) Run(ctx context.Context, _ int) {
	c.Controller.Run(ctx)
}
