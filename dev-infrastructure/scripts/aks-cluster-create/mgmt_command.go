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
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type managementRawOptions struct {
	commonRawOptions
	system, user, infra rawPoolOptions
}

func newManagementRawOptionsFromEnv(env func(string) string) *managementRawOptions {
	return &managementRawOptions{
		commonRawOptions: commonOptionsFromEnv(env),
		system:           poolOptionsFromEnv(env, "SYSTEM_POOL"),
		user:             poolOptionsFromEnv(env, "USER_POOL"),
		infra:            poolOptionsFromEnv(env, "INFRA_POOL"),
	}
}

func (in *managementRawOptions) Validate() (*validatedOptions, error) {
	o, err := in.commonRawOptions.Validate()
	if err != nil {
		return nil, utils.TrackError(err)
	}
	for _, pool := range []struct {
		target  *validatedPoolConfig
		prefix  string
		raw     rawPoolOptions
		class   compute.PoolClass
		mode    compute.PoolMode
		maxPods int32
	}{
		{&o.system, "SYSTEM_POOL", in.system, compute.SystemPools, compute.PoolModeRegional, 100},
		{&o.user, "USER_POOL", in.user, compute.WorkerPools, compute.PoolModePerZone, 225},
		{&o.infra, "INFRA_POOL", in.infra, compute.InfraPools, compute.PoolModePerZone, 225},
	} {
		cfg, err := validatePool(pool.prefix, pool.raw)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		cfg.class = pool.class
		cfg.mode = pool.mode
		cfg.maxPods = pool.maxPods
		cfg.upgradeSettings = armcontainerservice.AgentPoolUpgradeSettings{MaxSurge: ptr.To("10%")}
		*pool.target = cfg
	}

	return o, nil
}
