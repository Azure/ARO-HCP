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
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/internal/azsdk"
)

type AzureClient interface {
	Pool(context.Context, string, string) (PoolObservation, error)
	Instance(context.Context, string, string, string, string) (InstanceObservation, bool, error)
	Machine(context.Context, string, string, string) (string, error)
	DeleteMachine(context.Context, string, string) (MachineOperation, error)
	PollDeletion(context.Context, string, string) (MachineOperation, error)
}

type MachineOperation struct {
	Token     string
	Outcome   string
	Message   string
	PollAfter time.Time
}

type InstanceObservation struct {
	ID        string
	CreatedAt time.Time
}

// Resource IDs scope every request; only the AKS executor submits a mutation.
type azureClient struct {
	credential azcore.TokenCredential
	options    azcorearm.ClientOptions
	clock      func() time.Time
}

// NewAzureClient uses the same clock as its controller to timestamp observations.
func NewAzureClient(credential azcore.TokenCredential, clock func() time.Time) AzureClient {
	if clock == nil {
		clock = time.Now
	}
	return &azureClient{
		credential: credential, clock: clock,
		options: azcorearm.ClientOptions{ClientOptions: azsdk.NewClientOptions(azsdk.ComponentMgmtAgent)},
	}
}

func (a *azureClient) Machine(ctx context.Context, clusterID, pool, providerID string) (string, error) {
	id, err := azcorearm.ParseResourceID(clusterID)
	if err != nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.ContainerService/managedClusters") {
		return "", fmt.Errorf("invalid AKS cluster identity")
	}
	vm, err := providerResourceID(providerID)
	if err != nil {
		return "", err
	}
	client, err := armcontainerservice.NewMachinesClient(id.SubscriptionID, a.credential, &a.options)
	if err != nil {
		return "", err
	}
	name := ""
	pager := client.NewListPager(id.ResourceGroupName, id.Name, pool, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("map VM to AKS machine: %w", err)
		}
		for _, machine := range page.Value {
			if machine == nil || machine.Properties == nil || machine.Properties.ResourceID == nil ||
				!strings.EqualFold(*machine.Properties.ResourceID, vm.String()) {
				continue
			}
			if name != "" || machine.Name == nil || *machine.Name == "" || strings.Contains(*machine.Name, "/") ||
				machine.ID == nil || !strings.EqualFold(*machine.ID, clusterID+"/agentPools/"+pool+"/machines/"+*machine.Name) {
				return "", fmt.Errorf("ambiguous or invalid AKS machine identity")
			}
			name = *machine.Name
		}
	}
	if name == "" {
		return "", fmt.Errorf("VM has no matching AKS machine")
	}
	return name, nil
}

func (a *azureClient) deletionClient(poolID string) (*armcontainerservice.AgentPoolsClient, *azcorearm.ResourceID, error) {
	id, err := azcorearm.ParseResourceID(poolID)
	if err != nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.ContainerService/managedClusters/agentPools") {
		return nil, nil, fmt.Errorf("invalid AKS pool identity")
	}
	options := a.options
	// A lost POST response must not be retried against a reused machine name.
	options.Retry.MaxRetries = -1
	client, err := armcontainerservice.NewAgentPoolsClient(id.SubscriptionID, a.credential, &options)
	return client, id, err
}

func deletionOperation(ctx context.Context, poller *azruntime.Poller[armcontainerservice.AgentPoolsClientDeleteMachinesResponse]) (MachineOperation, error) {
	if poller.Done() {
		_, err := poller.Result(ctx)
		if err != nil {
			var response *azcore.ResponseError
			if errors.As(err, &response) && response.StatusCode >= 200 && response.StatusCode < 300 {
				return MachineOperation{Outcome: "Failed", Message: err.Error()}, nil
			}
			return MachineOperation{}, err
		}
		return MachineOperation{Outcome: "Succeeded"}, nil
	}
	token, err := poller.ResumeToken()
	if err != nil {
		return MachineOperation{}, err
	}
	return MachineOperation{Token: token, Outcome: "Pending"}, nil
}

func (a *azureClient) DeleteMachine(ctx context.Context, poolID, machine string) (MachineOperation, error) {
	client, id, err := a.deletionClient(poolID)
	if err != nil {
		return MachineOperation{}, err
	}
	if machine == "" || strings.Contains(machine, "/") {
		return MachineOperation{}, fmt.Errorf("invalid AKS machine name")
	}
	var response *http.Response
	poller, err := client.BeginDeleteMachines(policy.WithCaptureResponse(ctx, &response), id.ResourceGroupName, id.Parent.Name, id.Name,
		armcontainerservice.AgentPoolDeleteMachinesParameter{MachineNames: []*string{&machine}}, nil)
	if err != nil {
		return MachineOperation{}, err
	}
	op, err := deletionOperation(ctx, poller)
	op.PollAfter = nextPoll(response, a.clock())
	return op, err
}

func (a *azureClient) PollDeletion(ctx context.Context, poolID, token string) (MachineOperation, error) {
	if token == "" {
		return MachineOperation{}, fmt.Errorf("deletion operation reference missing")
	}
	client, id, err := a.deletionClient(poolID)
	if err != nil {
		return MachineOperation{}, err
	}
	poller, err := client.BeginDeleteMachines(ctx, id.ResourceGroupName, id.Parent.Name, id.Name,
		armcontainerservice.AgentPoolDeleteMachinesParameter{},
		&armcontainerservice.AgentPoolsClientBeginDeleteMachinesOptions{ResumeToken: token})
	if err != nil {
		return MachineOperation{}, err
	}
	var response *http.Response
	_, err = poller.Poll(policy.WithCaptureResponse(ctx, &response))
	if err != nil {
		return MachineOperation{PollAfter: nextPoll(response, a.clock())}, err
	}
	op, err := deletionOperation(ctx, poller)
	op.PollAfter = nextPoll(response, a.clock())
	return op, err
}

func nextPoll(response *http.Response, now time.Time) time.Time {
	next := now.Add(30 * time.Second)
	if response == nil {
		return next
	}
	header := response.Header.Get("Retry-After")
	if seconds, err := strconv.ParseInt(header, 10, 32); err == nil && seconds > 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if date, err := http.ParseTime(header); err == nil && date.After(now) {
		return date
	}
	return next
}

func (a *azureClient) Pool(ctx context.Context, clusterID, pool string) (PoolObservation, error) {
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
		Stable: *properties.ProvisioningState == "Succeeded", ObservedAt: a.clock(),
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

func (a *azureClient) Instance(ctx context.Context, clusterID, pool, providerID, nodeName string) (InstanceObservation, bool, error) {
	vmID, err := providerResourceID(providerID)
	if err != nil {
		return InstanceObservation{}, false, err
	}
	cluster, err := azcorearm.ParseResourceID(clusterID)
	if err != nil || !strings.EqualFold(cluster.SubscriptionID, vmID.SubscriptionID) {
		return InstanceObservation{}, false, fmt.Errorf("instance does not belong to the configured subscription")
	}
	clusters, err := armcontainerservice.NewManagedClustersClient(cluster.SubscriptionID, a.credential, &a.options)
	if err != nil {
		return InstanceObservation{}, false, err
	}
	mc, err := clusters.Get(ctx, cluster.ResourceGroupName, cluster.Name, nil)
	if err != nil {
		return InstanceObservation{}, false, fmt.Errorf("read management cluster: %w", err)
	}
	if mc.Properties == nil || mc.Properties.NodeResourceGroup == nil ||
		!strings.EqualFold(*mc.Properties.NodeResourceGroup, vmID.ResourceGroupName) {
		return InstanceObservation{}, false, fmt.Errorf("instance resource group is not the configured cluster's node resource group")
	}
	scaleSets, err := armcompute.NewVirtualMachineScaleSetsClient(vmID.SubscriptionID, a.credential, &a.options)
	if err != nil {
		return InstanceObservation{}, false, err
	}
	scaleSet, err := scaleSets.Get(ctx, vmID.ResourceGroupName, vmID.Parent.Name, nil)
	if err != nil {
		var response *azcore.ResponseError
		if errors.As(err, &response) && response.StatusCode == http.StatusNotFound && response.ErrorCode == "ResourceNotFound" {
			return InstanceObservation{}, false, nil
		}
		return InstanceObservation{}, false, fmt.Errorf("verify instance parent: %w", err)
	}
	if name := scaleSet.Tags["aks-managed-poolName"]; name == nil || !strings.EqualFold(*name, pool) {
		return InstanceObservation{}, false, fmt.Errorf("instance parent does not identify the expected agent pool")
	}
	vms, err := armcompute.NewVirtualMachineScaleSetVMsClient(vmID.SubscriptionID, a.credential, &a.options)
	if err != nil {
		return InstanceObservation{}, false, err
	}
	vm, err := vms.Get(ctx, vmID.ResourceGroupName, vmID.Parent.Name, vmID.Name, nil)
	if err != nil {
		var response *azcore.ResponseError
		if errors.As(err, &response) && response.StatusCode == http.StatusNotFound &&
			(response.ErrorCode == "ResourceNotFound" || response.ErrorCode == "NotFound") {
			return InstanceObservation{}, false, nil
		}
		return InstanceObservation{}, false, fmt.Errorf("read original instance: %w", err)
	}
	if vm.Properties == nil || vm.Properties.VMID == nil || *vm.Properties.VMID == "" {
		return InstanceObservation{}, false, fmt.Errorf("instance has no immutable VM ID")
	}
	if nodeName != "" && (vm.Properties.OSProfile == nil || vm.Properties.OSProfile.ComputerName == nil ||
		!strings.EqualFold(*vm.Properties.OSProfile.ComputerName, nodeName)) {
		return InstanceObservation{}, false, fmt.Errorf("instance computer name does not match the Node")
	}
	observation := InstanceObservation{ID: strings.ToLower(*vm.Properties.VMID)}
	if vm.Properties.TimeCreated != nil {
		observation.CreatedAt = *vm.Properties.TimeCreated
	}
	return observation, true, nil
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
