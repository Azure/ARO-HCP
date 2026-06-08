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

// Package aksclient provides Azure ARM and Kubernetes client construction for
// ARO HCP pipeline scripts. It enforces the non-interactive credential chain
// (MSI / SP / workload-identity) required for EV2 Shell steps.
package aksclient

import (
"context"
"fmt"

"github.com/Azure/azure-sdk-for-go/sdk/azcore"
"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
"k8s.io/client-go/kubernetes"

mcclient "github.com/Azure/ARO-HCP/sessiongate/pkg/mc"
)

// Clients holds Azure ARM and Kubernetes clients for a single AKS cluster.
type Clients struct {
Cred  azcore.TokenCredential
Pools *armcs.AgentPoolsClient
MC    *armcs.ManagedClustersClient
Kube  kubernetes.Interface
}

// New creates Azure ARM clients. The Kubernetes client is deferred until
// BootstrapKube confirms the cluster is reachable.
func New(subscriptionID string) (*Clients, error) {
cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
RequireAzureTokenCredentials: true,
})
if err != nil {
return nil, fmt.Errorf("azidentity: %w", err)
}
factory, err := armcs.NewClientFactory(subscriptionID, cred, nil)
if err != nil {
return nil, fmt.Errorf("arm containerservice factory: %w", err)
}
return &Clients{
Cred:  cred,
Pools: factory.NewAgentPoolsClient(),
MC:    factory.NewManagedClustersClient(),
}, nil
}

// BootstrapKube initialises the Kubernetes client against the given AKS cluster
// ARM resource ID.
func (c *Clients) BootstrapKube(ctx context.Context, clusterID string) error {
if clusterID == "" {
return fmt.Errorf("cluster ARM ID empty; cannot bootstrap kube client")
}
restCfg, err := mcclient.GetAKSRESTConfig(ctx, clusterID, c.Cred)
if err != nil {
return fmt.Errorf("AKS REST config: %w", err)
}
kc, err := kubernetes.NewForConfig(restCfg)
if err != nil {
return fmt.Errorf("kubernetes client: %w", err)
}
c.Kube = kc
return nil
}

// ListPoolsByNodeLabel returns all agent pools whose node label at labelKey
// equals labelValue, keyed by pool name.
func (c *Clients) ListPoolsByNodeLabel(ctx context.Context, resourceGroup, clusterName, labelKey, labelValue string) (map[string]*armcs.AgentPool, error) {
result := make(map[string]*armcs.AgentPool)
pager := c.Pools.NewListPager(resourceGroup, clusterName, nil)
for pager.More() {
page, err := pager.NextPage(ctx)
if err != nil {
return nil, fmt.Errorf("list agent pools: %w", err)
}
for _, p := range page.Value {
if p.Name == nil || p.Properties == nil {
continue
}
labels := p.Properties.NodeLabels
if labels == nil {
continue
}
v, ok := labels[labelKey]
if !ok || v == nil || *v != labelValue {
continue
}
pool := p
result[*p.Name] = pool
}
}
return result, nil
}

// DeletePool issues the ARM agent-pool delete LRO and waits for completion.
func (c *Clients) DeletePool(ctx context.Context, resourceGroup, clusterName, pool string) error {
poller, err := c.Pools.BeginDelete(ctx, resourceGroup, clusterName, pool, nil)
if err != nil {
return fmt.Errorf("begin delete pool %s: %w", pool, err)
}
if _, err := poller.PollUntilDone(ctx, nil); err != nil {
return fmt.Errorf("poll delete pool %s: %w", pool, err)
}
return nil
}
