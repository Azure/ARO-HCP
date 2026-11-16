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
	"fmt"
	"strconv"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var (
	serviceSystemPoolClass = compute.PoolClass{
		Role:          compute.PoolRoleSystem,
		AgentPoolMode: armcontainerservice.AgentPoolModeSystem,
		Taints:        []string{compute.TaintCriticalAddonsOnly},
	}
	serviceSystemOnlyPoolClass = compute.PoolClass{
		Role:          compute.PoolRoleSystem,
		AgentPoolMode: armcontainerservice.AgentPoolModeSystem,
		Taints:        []string{},
	}
	serviceWorkerPoolClass = compute.PoolClass{
		Role:          compute.PoolRoleWorker,
		AgentPoolMode: armcontainerservice.AgentPoolModeUser,
	}
	serviceInfraPoolClass = compute.PoolClass{
		Role:          compute.PoolRoleInfra,
		AgentPoolMode: armcontainerservice.AgentPoolModeUser,
		Taints:        []string{compute.TaintInfra},
	}
)

type serviceRawOptions struct {
	commonRawOptions
	system, user, infra      rawPoolOptions
	systemPoolOnly           string
	istioVersions            string
	maxSurge, maxUnavailable string
}

func newServiceRawOptionsFromEnv(env func(string) string) *serviceRawOptions {
	return &serviceRawOptions{
		commonRawOptions: commonOptionsFromEnv(env),
		system:           poolOptionsFromEnv(env, "SYSTEM_POOL"),
		user:             poolOptionsFromEnv(env, "USER_POOL"),
		infra:            poolOptionsFromEnv(env, "INFRA_POOL"),
		systemPoolOnly:   env("SYSTEM_POOL_ONLY"),
		istioVersions:    env("ISTIO_VERSIONS"),
		maxSurge:         env("UPGRADE_SETTINGS_MAX_SURGE"),
		maxUnavailable:   env("UPGRADE_SETTINGS_MAX_UNAVAILABLE"),
	}
}

func (in *serviceRawOptions) Validate() (*validatedOptions, error) {
	o, err := in.commonRawOptions.Validate()
	if err != nil {
		return nil, utils.TrackError(err)
	}
	var systemOnly bool
	if len(in.systemPoolOnly) > 0 {
		systemOnly, err = strconv.ParseBool(in.systemPoolOnly)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("SYSTEM_POOL_ONLY: %w", err))
		}
	}
	maxSurge, maxUnavailable := in.maxSurge, in.maxUnavailable
	if len(maxSurge) == 0 {
		maxSurge = "10%"
	}
	if len(maxUnavailable) == 0 {
		maxUnavailable = "0"
	}
	systemClass := serviceSystemPoolClass
	if systemOnly {
		systemClass = serviceSystemOnlyPoolClass
	}
	pools := []struct {
		target  *validatedPoolConfig
		prefix  string
		raw     rawPoolOptions
		class   compute.PoolClass
		mode    compute.PoolMode
		maxPods int32
	}{
		{&o.system, "SYSTEM_POOL", in.system, systemClass, compute.PoolModeRegional, 100},
		{&o.user, "USER_POOL", in.user, serviceWorkerPoolClass, compute.PoolModePerZone, 225},
		{&o.infra, "INFRA_POOL", in.infra, serviceInfraPoolClass, compute.PoolModePerZone, 225},
	}
	if systemOnly {
		pools = pools[:1]
	}
	for _, pool := range pools {
		cfg, err := validatePool(pool.prefix, pool.raw)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		cfg.class = pool.class
		cfg.mode = pool.mode
		cfg.maxPods = pool.maxPods
		cfg.upgradeSettings = armcontainerservice.AgentPoolUpgradeSettings{MaxSurge: ptr.To("10%")}
		if pool.class.AgentPoolMode != armcontainerservice.AgentPoolModeSystem {
			cfg.upgradeSettings = armcontainerservice.AgentPoolUpgradeSettings{MaxSurge: ptr.To(maxSurge), MaxUnavailable: ptr.To(maxUnavailable)}
		}
		*pool.target = cfg
	}
	revisions := parseCSVList(in.istioVersions)
	o.serviceMeshProfile = &armcontainerservice.ServiceMeshProfile{
		Mode: ptr.To(armcontainerservice.ServiceMeshModeIstio),
		Istio: &armcontainerservice.IstioServiceMesh{
			Components: &armcontainerservice.IstioComponents{
				IngressGateways: []*armcontainerservice.IstioIngressGateway{
					{Enabled: ptr.To(true), Mode: ptr.To(armcontainerservice.IstioIngressGatewayModeExternal)},
				},
			},
		},
	}
	o.ingressProfile = &armcontainerservice.ManagedClusterIngressProfile{
		GatewayAPI: &armcontainerservice.ManagedClusterIngressProfileGatewayConfiguration{Installation: ptr.To(armcontainerservice.ManagedGatewayTypeStandard)},
	}
	for _, revision := range revisions {
		o.serviceMeshProfile.Istio.Revisions = append(o.serviceMeshProfile.Istio.Revisions, ptr.To(revision))
	}
	o.systemPoolOnly = systemOnly

	return o, nil
}
