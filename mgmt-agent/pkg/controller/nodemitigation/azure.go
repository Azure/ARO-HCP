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

package nodemitigation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/internal/azsdk"
)

type AzureReader interface {
	Pool(context.Context, string, string) (PoolObservation, error)
	Instance(context.Context, string, string, string, string) (string, bool, error)
}

// Azure reads are scoped by explicit resource IDs. This interface has no write
// methods; a failed read must never be interpreted as a missing instance.
type azureReader struct {
	credential azcore.TokenCredential
	options    azcorearm.ClientOptions
}

func NewAzureReader(credential azcore.TokenCredential) AzureReader {
	return &azureReader{credential: credential, options: azcorearm.ClientOptions{ClientOptions: azsdk.NewClientOptions(azsdk.ComponentMgmtAgent)}}
}

func (a *azureReader) Pool(ctx context.Context, clusterID, pool string) (PoolObservation, error) {
	id, err := azcorearm.ParseResourceID(clusterID)
	if err != nil || pool == "" || strings.Contains(pool, "/") {
		return PoolObservation{}, fmt.Errorf("invalid cluster or agent-pool identity")
	}
	client, err := armcontainerservice.NewAgentPoolsClient(id.SubscriptionID, a.credential, &a.options)
	if err != nil {
		return PoolObservation{}, err
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Name, pool, nil)
	if err != nil {
		return PoolObservation{}, fmt.Errorf("read agent pool: %w", err)
	}
	properties := response.Properties
	if response.ID == nil || !strings.EqualFold(*response.ID, clusterID+"/agentPools/"+pool) ||
		properties == nil || properties.Count == nil || properties.ProvisioningState == nil ||
		properties.Mode == nil || *properties.Mode != armcontainerservice.AgentPoolModeUser {
		return PoolObservation{}, fmt.Errorf("incomplete or unsupported user pool observation")
	}
	return PoolObservation{
		ID: strings.ToLower(*response.ID), Target: *properties.Count,
		Stable: *properties.ProvisioningState == "Succeeded", ObservedAt: time.Now(),
	}, nil
}

func providerResourceID(providerID string) (*azcorearm.ResourceID, error) {
	if !strings.HasPrefix(providerID, "azure://") {
		return nil, fmt.Errorf("unsupported node provider")
	}
	id, err := azcorearm.ParseResourceID(strings.TrimPrefix(providerID, "azure://"))
	if err != nil || id == nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.Compute/virtualMachineScaleSets/virtualMachines") {
		return nil, fmt.Errorf("provider ID must identify a VMSS instance")
	}
	return id, nil
}

func (a *azureReader) Instance(ctx context.Context, clusterID, pool, providerID, nodeName string) (string, bool, error) {
	vmID, err := providerResourceID(providerID)
	if err != nil {
		return "", false, err
	}
	cluster, err := azcorearm.ParseResourceID(clusterID)
	if err != nil || !strings.EqualFold(cluster.SubscriptionID, vmID.SubscriptionID) {
		return "", false, fmt.Errorf("instance does not belong to the configured subscription")
	}
	clusters, err := armcontainerservice.NewManagedClustersClient(cluster.SubscriptionID, a.credential, &a.options)
	if err != nil {
		return "", false, err
	}
	mc, err := clusters.Get(ctx, cluster.ResourceGroupName, cluster.Name, nil)
	if err != nil {
		return "", false, fmt.Errorf("read management cluster: %w", err)
	}
	if mc.Properties == nil || mc.Properties.NodeResourceGroup == nil ||
		!strings.EqualFold(*mc.Properties.NodeResourceGroup, vmID.ResourceGroupName) {
		return "", false, fmt.Errorf("instance resource group is not the configured cluster's node resource group")
	}
	scaleSets, err := armcompute.NewVirtualMachineScaleSetsClient(vmID.SubscriptionID, a.credential, &a.options)
	if err != nil {
		return "", false, err
	}
	scaleSet, err := scaleSets.Get(ctx, vmID.ResourceGroupName, vmID.Parent.Name, nil)
	if err != nil {
		var response *azcore.ResponseError
		if errors.As(err, &response) && response.StatusCode == http.StatusNotFound && response.ErrorCode == "ResourceNotFound" {
			return "", false, nil
		}
		return "", false, fmt.Errorf("verify instance parent: %w", err)
	}
	if name := scaleSet.Tags["aks-managed-poolName"]; name == nil || !strings.EqualFold(*name, pool) {
		return "", false, fmt.Errorf("instance parent does not identify the expected agent pool")
	}
	vms, err := armcompute.NewVirtualMachineScaleSetVMsClient(vmID.SubscriptionID, a.credential, &a.options)
	if err != nil {
		return "", false, err
	}
	vm, err := vms.Get(ctx, vmID.ResourceGroupName, vmID.Parent.Name, vmID.Name, nil)
	if err != nil {
		var response *azcore.ResponseError
		if errors.As(err, &response) && response.StatusCode == http.StatusNotFound && response.ErrorCode == "ResourceNotFound" {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read original instance: %w", err)
	}
	if vm.Properties == nil || vm.Properties.VMID == nil || *vm.Properties.VMID == "" {
		return "", false, fmt.Errorf("instance has no immutable VM ID")
	}
	if nodeName != "" && (vm.Properties.OSProfile == nil || vm.Properties.OSProfile.ComputerName == nil ||
		!strings.EqualFold(*vm.Properties.OSProfile.ComputerName, nodeName)) {
		return "", false, fmt.Errorf("instance computer name does not match the Node")
	}
	return strings.ToLower(*vm.Properties.VMID), true, nil
}

func ready(node *corev1.Node) bool {
	if node == nil || node.DeletionTimestamp != nil {
		return false
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func poolName(node *corev1.Node) string {
	return node.Labels["kubernetes.azure.com/agentpool"]
}
