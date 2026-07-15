// Copyright 2025 Microsoft Corporation
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

package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	"github.com/Azure/ARO-HCP/internal/azsdk"
)

const (
	// SwiftNICResourceName is the extended resource name for SWIFT NICs.
	SwiftNICResourceName corev1.ResourceName = "aro.openshift.io/swift-nic"

	// fieldManager is the SSA field manager name for this controller.
	fieldManager = "mgmt-agent-swift-nic"
)

// GetVMFunc is a function that retrieves a VMSS VM by its coordinates.
// The controller uses this to inspect the VM's network configuration.
type GetVMFunc func(ctx context.Context, subscriptionID, resourceGroup, vmssName, instanceID string) (armcompute.VirtualMachineScaleSetVMsClientGetResponse, error)

// SwiftNICController watches Node objects and sets the aro.openshift.io/swift-nic
// extended resource in status.capacity via Server-Side Apply, enabling the
// Kubernetes scheduler to assign SWIFT NICs to pods.
type SwiftNICController struct {
	kubeClientset kubernetes.Interface
	nodeLister    corelisters.NodeLister
	nodeSynced    cache.InformerSynced
	workqueue     workqueue.TypedRateLimitingInterface[string]
	getVM         GetVMFunc
}

// NewSwiftNICController creates a new SwiftNICController.
// If getVM is nil, the default Azure SDK implementation is used.
func NewSwiftNICController(
	kubeClientset kubernetes.Interface,
	nodeInformer coreinformers.NodeInformer,
	credential azcore.TokenCredential,
	getVM GetVMFunc,
) (*SwiftNICController, error) {
	if getVM == nil {
		getVM = defaultGetVM(credential)
	}
	c := &SwiftNICController{
		kubeClientset: kubeClientset,
		nodeLister:    nodeInformer.Lister(),
		nodeSynced:    nodeInformer.Informer().HasSynced,
		workqueue:     workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[string](), workqueue.TypedRateLimitingQueueConfig[string]{Name: "SwiftNIC"}),
		getVM:         getVM,
	}

	if _, err := nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				c.workqueue.Add(key)
			}
		},
		UpdateFunc: func(old, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil {
				c.workqueue.Add(key)
			}
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler: %w", err)
	}

	return c, nil
}

// Run starts the controller workers and blocks until the context is cancelled.
func (c *SwiftNICController) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting SwiftNIC controller")

	logger.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.nodeSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Starting workers", "count", workers)
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("Controller started")
	<-ctx.Done()
	logger.Info("Shutting down controller")

	return nil
}

func (c *SwiftNICController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *SwiftNICController) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}
	defer c.workqueue.Done(key)

	err := c.syncHandler(ctx, key)
	if err == nil {
		c.workqueue.Forget(key)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("error syncing node %q: %w", key, err))
	c.workqueue.AddRateLimited(key)
	return true
}

func (c *SwiftNICController) syncHandler(ctx context.Context, key string) error {
	logger := klog.FromContext(ctx)

	node, err := c.nodeLister.Get(key)
	if err != nil {
		logger.V(4).Info("Node no longer exists, skipping", "node", key)
		return nil
	}

	nodeApply, err := c.process(ctx, node)
	if err != nil {
		return fmt.Errorf("failed to process node %s: %w", key, err)
	}
	if nodeApply == nil {
		return nil
	}

	_, err = c.kubeClientset.CoreV1().Nodes().ApplyStatus(ctx, nodeApply, metav1.ApplyOptions{
		FieldManager: fieldManager,
	})
	if err != nil {
		return fmt.Errorf("failed to apply node status for %s: %w", key, err)
	}

	logger.Info("Successfully set SWIFT NIC capacity", "node", key)
	return nil
}

// process inspects a Node and determines whether its SWIFT NIC extended resource
// needs to be set. It returns a node status apply configuration if an update is
// needed, or nil if no action is required. This is a pure function of the node
// state and the VM data returned by getVM.
func (c *SwiftNICController) process(ctx context.Context, node *corev1.Node) (*applycorev1.NodeApplyConfiguration, error) {
	logger := klog.FromContext(ctx)
	name := node.Name

	// If the extended resource is already set, skip — the NIC count for a VM
	// does not change during its lifetime.
	if _, exists := node.Status.Capacity[SwiftNICResourceName]; exists {
		logger.V(6).Info("Node already has SWIFT NIC capacity set, skipping", "node", name)
		return nil, nil
	}

	providerID := node.Spec.ProviderID
	if providerID == "" {
		logger.V(4).Info("Node has no providerID yet, skipping", "node", name)
		return nil, nil
	}

	subscriptionID, resourceGroup, vmssName, instanceID, err := parseProviderID(providerID)
	if err != nil {
		logger.Error(err, "Failed to parse providerID, skipping", "node", name, "providerID", providerID)
		return nil, nil // don't requeue — a malformed providerID won't fix itself
	}

	vm, err := c.getVM(ctx, subscriptionID, resourceGroup, vmssName, instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get VMSS VM for node %s: %w", name, err)
	}

	nicCount := countSwiftNICs(vm)
	logger.Info("Setting SWIFT NIC capacity on node", "node", name, "count", nicCount)

	return applycorev1.Node(name).
		WithStatus(applycorev1.NodeStatus().
			WithCapacity(corev1.ResourceList{
				SwiftNICResourceName: *resource.NewQuantity(int64(nicCount), resource.DecimalSI),
			}),
		), nil
}

// countSwiftNICs counts non-primary NICs on a VMSS VM.
func countSwiftNICs(vm armcompute.VirtualMachineScaleSetVMsClientGetResponse) int {
	count := 0
	if vm.Properties == nil || vm.Properties.NetworkProfileConfiguration == nil {
		return count
	}
	for _, nic := range vm.Properties.NetworkProfileConfiguration.NetworkInterfaceConfigurations {
		if nic == nil || nic.Properties == nil {
			continue
		}
		isPrimary := nic.Properties.Primary != nil && *nic.Properties.Primary
		if !isPrimary {
			count++
		}
	}
	return count
}

// defaultGetVM returns a GetVMFunc that uses the Azure Compute SDK,
// caching clients per subscription to avoid creating a new client on every call.
func defaultGetVM(credential azcore.TokenCredential) GetVMFunc {
	var mu sync.Mutex
	clients := make(map[string]*armcompute.VirtualMachineScaleSetVMsClient)
	clientOpts := azsdk.NewClientOptions(azsdk.ComponentMgmtAgent)
	armOpts := &azcorearm.ClientOptions{ClientOptions: clientOpts}

	return func(ctx context.Context, subscriptionID, resourceGroup, vmssName, instanceID string) (armcompute.VirtualMachineScaleSetVMsClientGetResponse, error) {
		mu.Lock()
		client, ok := clients[subscriptionID]
		mu.Unlock()
		if !ok {
			var err error
			client, err = armcompute.NewVirtualMachineScaleSetVMsClient(subscriptionID, credential, armOpts)
			if err != nil {
				return armcompute.VirtualMachineScaleSetVMsClientGetResponse{}, fmt.Errorf("failed to create VMSS VM client: %w", err)
			}
			mu.Lock()
			// Check again in case another goroutine created it concurrently.
			if existing, ok := clients[subscriptionID]; ok {
				client = existing
			} else {
				clients[subscriptionID] = client
			}
			mu.Unlock()
		}
		return client.Get(ctx, resourceGroup, vmssName, instanceID, nil)
	}
}

// parseProviderID extracts Azure resource coordinates from a Node's providerID.
// The expected format is:
//
//	azure:///subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Compute/virtualMachineScaleSets/<vmss>/virtualMachines/<id>
func parseProviderID(providerID string) (subscriptionID, resourceGroup, vmssName, instanceID string, err error) {
	const prefix = "azure:///"
	if !strings.HasPrefix(providerID, prefix) {
		return "", "", "", "", fmt.Errorf("providerID %q does not start with %q", providerID, prefix)
	}
	resourceID := "/" + providerID[len(prefix):]

	parsed, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to parse resource ID from providerID %q: %w", providerID, err)
	}

	if !strings.EqualFold(parsed.ResourceType.Type, "virtualMachineScaleSets/virtualMachines") {
		return "", "", "", "", fmt.Errorf("providerID %q is not a VMSS VM resource, got type %q", providerID, parsed.ResourceType.Type)
	}

	// For a child resource like virtualMachineScaleSets/<vmss>/virtualMachines/<id>,
	// parsed.Name is the instance ID and the parent VMSS name is in the resource ID path.
	// We need to extract the VMSS name from the parent.
	parent, err := azcorearm.ParseResourceID(parsed.Parent.String())
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to parse parent resource ID from providerID %q: %w", providerID, err)
	}

	return parsed.SubscriptionID, parsed.ResourceGroupName, parent.Name, parsed.Name, nil
}
