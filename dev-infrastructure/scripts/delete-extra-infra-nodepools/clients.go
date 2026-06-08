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

package main

import (
"context"
"fmt"
"time"

armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

"github.com/Azure/ARO-HCP/dev-infrastructure/scripts/internal/aksclient"
"github.com/Azure/ARO-HCP/dev-infrastructure/scripts/internal/akslog"
"github.com/Azure/ARO-HCP/dev-infrastructure/scripts/internal/aksnode"
)

const roleLabel = "aro-hcp.azure.com/role"

// scriptClients wraps the shared aksclient.Clients with script-specific
// configuration so run.go stays thin.
type scriptClients struct {
cfg *config
*aksclient.Clients
}

func newClients(cfg *config) (*scriptClients, error) {
c, err := aksclient.New(cfg.subscriptionID)
if err != nil {
return nil, err
}
return &scriptClients{cfg: cfg, Clients: c}, nil
}

func (c *scriptClients) bootstrapKube(ctx context.Context, clusterID string) error {
return c.Clients.BootstrapKube(ctx, clusterID)
}

func (c *scriptClients) getCluster(ctx context.Context) (*armcs.ManagedCluster, error) {
resp, err := c.MC.Get(ctx, c.cfg.resourceGroup, c.cfg.clusterName, nil)
if err != nil {
return nil, fmt.Errorf("cluster get: %w", err)
}
return &resp.ManagedCluster, nil
}

func (c *scriptClients) listPoolsByTag(ctx context.Context) (map[string]*armcs.AgentPool, error) {
return c.Clients.ListPoolsByNodeLabel(ctx, c.cfg.resourceGroup, c.cfg.clusterName, roleLabel, c.cfg.poolTag)
}

func (c *scriptClients) allExpectedPoolsReady(ctx context.Context, expectedNames []string, livePools map[string]*armcs.AgentPool) error {
akslog.Banner("SAFETY CHECK — expected pools ready")
if err := aksnode.AllExpectedReady(
ctx, c.Kube, expectedNames, livePools,
c.cfg.poolMinCount,
time.Duration(c.cfg.readyTimeoutMin)*time.Minute,
); err != nil {
return err
}
akslog.Logf("all %d expected pool(s) healthy — safe to delete extras", len(expectedNames))
return nil
}

func (c *scriptClients) drainPool(ctx context.Context, pool string) error {
return aksnode.Drain(ctx, c.Kube, pool, time.Duration(c.cfg.drainTimeoutMin)*time.Minute)
}

func (c *scriptClients) deletePool(ctx context.Context, pool string) error {
akslog.Logf(">>> deleting pool %s", pool)
if err := c.Clients.DeletePool(ctx, c.cfg.resourceGroup, c.cfg.clusterName, pool); err != nil {
return err
}
akslog.Logf("pool %s deleted", pool)
return nil
}
