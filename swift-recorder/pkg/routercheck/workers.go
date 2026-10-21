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

package routercheck

import (
	"fmt"
	"net/netip"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	capzv1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // Deployed CAPI resources use v1beta1.

	"github.com/Azure/ARO-HCP/swift-recorder/pkg/probe"
)

const clusterNameLabel = clusterv1.ClusterNameLabel

var machineGVR = schema.GroupVersionResource{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "machines"}
var azureMachineGVR = schema.GroupVersionResource{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1", Resource: "azuremachines"}

// WorkerInput is a Machine paired with the AzureMachine from InfrastructureRef.
type WorkerInput struct {
	Machine      *clusterv1.Machine
	AzureMachine *capzv1.AzureMachine
}

// WorkerInfo preserves every correlated identity when workers share an address.
type WorkerInfo struct {
	ClusterName           string `json:"cluster_name"`
	MachineName           string `json:"machine_name"`
	MachineUID            string `json:"machine_uid"`
	AzureMachineName      string `json:"azure_machine_name"`
	AzureMachineUID       string `json:"azure_machine_uid"`
	ProviderID            string `json:"provider_id,omitempty"`
	MachineProviderID     string `json:"machine_provider_id,omitempty"`
	AddressSource         string `json:"address_source"`
	MachineAddressMatches *bool  `json:"machine_address_matches,omitempty"`
	MachineDeleting       bool   `json:"machine_deleting"`
	AzureMachineDeleting  bool   `json:"azure_machine_deleting"`
}

func workerTargets(workers []WorkerInput, swift []SwiftInterface) ([]TargetInfo, error) {
	if len(workers) == 0 {
		return nil, fmt.Errorf("workers empty: %w", ErrDiscoveryData)
	}
	var targets []TargetInfo
	indices := map[string]int{}
	for _, worker := range workers {
		machine, azure := worker.Machine, worker.AzureMachine
		if machine == nil || azure == nil {
			return nil, fmt.Errorf("worker resources: %w", ErrDiscoveryData)
		}
		addresses, err := workerInternalIPs(azure.Status.Addresses)
		if err != nil {
			return nil, err
		}
		if len(addresses) == 0 {
			// CAPZ provides destination addresses; Machine addresses provide corroboration.
			return nil, fmt.Errorf("azure machine %s InternalIP: %w", azure.Name, ErrDiscoveryData)
		}
		var machineAddresses []corev1.NodeAddress
		for _, address := range machine.Status.Addresses {
			machineAddresses = append(machineAddresses, corev1.NodeAddress{Type: corev1.NodeAddressType(address.Type), Address: address.Address})
		}
		corroboration, err := workerInternalIPs(machineAddresses)
		if err != nil {
			return nil, err
		}
		for _, address := range addresses {
			evidence := WorkerInfo{ClusterName: machine.Spec.ClusterName, MachineName: machine.Name, MachineUID: string(machine.UID),
				AzureMachineName: azure.Name, AzureMachineUID: string(azure.UID), ProviderID: ptr.Deref(azure.Spec.ProviderID, ""), MachineProviderID: ptr.Deref(machine.Spec.ProviderID, ""),
				AddressSource: "AzureMachine.status.addresses.InternalIP", MachineDeleting: machine.DeletionTimestamp != nil, AzureMachineDeleting: azure.DeletionTimestamp != nil}
			if len(corroboration) > 0 {
				matches := slices.Contains(corroboration, address)
				evidence.MachineAddressMatches = &matches
			}
			bound := false
			seenSources := map[string]bool{}
			for _, source := range swift {
				if !sameFamily(source.IP, address) || seenSources[source.IP] {
					continue
				}
				seenSources[source.IP], bound = true, true
				id := fmt.Sprintf("worker-outbound/%s/22/%s", address, source.IP)
				index, exists := indices[id]
				if !exists {
					index = len(targets)
					indices[id] = index
					targets = append(targets, TargetInfo{Target: probe.Target{ID: id, Role: "worker-outbound", Address: address, Port: 22, SourceIP: source.IP, TCPOnly: true}})
				}
				targets[index].Workers = append(targets[index].Workers, evidence)
				targets[index].Deleting = targets[index].Deleting || evidence.MachineDeleting || evidence.AzureMachineDeleting
			}
			if !bound {
				return nil, fmt.Errorf("worker %s Swift source family: %w", machine.Name, ErrDiscoveryData)
			}
		}
	}
	return targets, nil
}

func workerInternalIPs(entries []corev1.NodeAddress) ([]string, error) {
	var addresses []string
	for _, entry := range entries {
		if entry.Type != corev1.NodeInternalIP {
			continue
		}
		ip, err := netip.ParseAddr(entry.Address)
		if err != nil || ip.Zone() != "" || !ip.IsGlobalUnicast() {
			return nil, fmt.Errorf("worker InternalIP: %w", ErrDiscoveryData)
		}
		address := ip.Unmap().String()
		if !slices.Contains(addresses, address) {
			addresses = append(addresses, address)
		}
	}
	return addresses, nil
}
