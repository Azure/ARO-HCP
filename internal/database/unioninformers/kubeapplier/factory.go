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

package kubeapplier

import (
	"context"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/informers"
)

// NewKubeApplierInformerFactory wires a database.KubeApplierDBClients into
// the PerMCKubeApplierInformerFactory shape the controller expects. It is
// the production wiring used by the backend; the integration test wires up
// an equivalent factory inline against the in-memory mock registry.
//
// relistDuration is forwarded to the per-MC informer factory; pass nil to
// use the default cadence from internal/database/informers.
func NewKubeApplierInformerFactory(
	clients database.KubeApplierDBClients,
	relistDuration *time.Duration,
) PerMCKubeApplierInformerFactory {
	return &kubeApplierInformerFactory{
		kubeApplierClients: clients,
		relistDuration:     relistDuration,
	}
}

type kubeApplierInformerFactory struct {
	kubeApplierClients database.KubeApplierDBClients
	relistDuration     *time.Duration
}

func (factory *kubeApplierInformerFactory) NewKubeApplierInformers(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) informers.KubeApplierInformers {
	client := factory.kubeApplierClients.For(ctx, managementClusterResourceID)
	if client == nil {
		return nil
	}
	return informers.NewKubeApplierInformersWithRelistDuration(ctx, client.Listers(), client, factory.relistDuration)
}
