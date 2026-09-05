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

// Package agentpoolspec builds the AKS agent pool ARM properties for a
// compute.Pool. It has no dependency on controller machinery so it can be
// shared between the nodepool controller and standalone tools (e.g. the
// AKS cluster creation tool) without pulling in unrelated dependencies.
package agentpoolspec

import (
	"strconv"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// Swift NIC tags mark an AKS agent pool for Swift networking (multi-tenant
// NIC) and carry the configured per-node secondary NIC count. Set here on
// pool creation, and read back by the controller (current.go) to detect
// whether a live pool has Swift enabled.
const (
	SwiftMultiTenancyTag      = "aks-nic-enable-multi-tenancy"
	SwiftSecondaryNICCountTag = "aks-nic-secondary-count"

	// SwiftMultiTenancyEnabledValue is the value written to SwiftMultiTenancyTag
	// when Swift is enabled, and matched by the controller when reading it back.
	SwiftMultiTenancyEnabledValue = "true"
)

// Build builds the AKS agent pool properties for a desired pool. Used both by
// the controller's create action (via a live client) and by tools that create
// pools outside the controller's reconcile loop, so pools always look the
// same regardless of which caller created them.
func Build(pool compute.Pool, networkConfig compute.NetworkConfig) *armcontainerservice.ManagedClusterAgentPoolProfileProperties {
	mode := armcontainerservice.AgentPoolModeUser
	if pool.Labels[compute.RoleLabel] == string(compute.PoolRoleSystem) {
		mode = armcontainerservice.AgentPoolModeSystem
	}

	labels := make(map[string]*string, len(pool.Labels))
	for k, v := range pool.Labels {
		labels[k] = ptr.To(v)
	}

	var taints []*string
	for _, t := range pool.Taints {
		taints = append(taints, ptr.To(t))
	}

	var tags map[string]*string
	if pool.EnableSwift && pool.Spec.SecondaryNICs > 0 {
		tags = map[string]*string{
			SwiftMultiTenancyTag:      ptr.To(SwiftMultiTenancyEnabledValue),
			SwiftSecondaryNICCountTag: ptr.To(strconv.FormatInt(pool.Spec.SecondaryNICs, 10)),
		}
	}

	var zones []*string
	for _, z := range pool.AvailabilityZones {
		zones = append(zones, ptr.To(z))
	}

	properties := &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
		VMSize:                 ptr.To(pool.Spec.Size),
		AvailabilityZones:      zones,
		OSDiskSizeGB:           ptr.To(pool.OSDiskSizeGB),
		OSDiskType:             ptr.To(armcontainerservice.OSDiskTypeEphemeral),
		EnableAutoScaling:      ptr.To(true),
		MinCount:               ptr.To(pool.InitialMinCount),
		MaxCount:               ptr.To(pool.MaxCount),
		Mode:                   ptr.To(mode),
		Type:                   ptr.To(armcontainerservice.AgentPoolTypeVirtualMachineScaleSets),
		OSSKU:                  ptr.To(armcontainerservice.OSSKUAzureLinux),
		OSType:                 ptr.To(armcontainerservice.OSTypeLinux),
		KubeletDiskType:        ptr.To(armcontainerservice.KubeletDiskTypeOS),
		EnableEncryptionAtHost: ptr.To(true),
		EnableFIPS:             ptr.To(true),
		EnableNodePublicIP:     ptr.To(false),
		MaxPods:                ptr.To(pool.MaxPods),
		SecurityProfile: &armcontainerservice.AgentPoolSecurityProfile{
			EnableSecureBoot: ptr.To(false),
			EnableVTPM:       ptr.To(false),
		},
		UpgradeSettings: &armcontainerservice.AgentPoolUpgradeSettings{
			MaxSurge: ptr.To("10%"),
		},
		NodeLabels: labels,
		NodeTaints: taints,
		Tags:       tags,
	}

	if len(networkConfig.VnetSubnetID) > 0 {
		properties.VnetSubnetID = ptr.To(networkConfig.VnetSubnetID)
	}
	if len(networkConfig.PodSubnetID) > 0 {
		properties.PodSubnetID = ptr.To(networkConfig.PodSubnetID)
	}

	return properties
}
