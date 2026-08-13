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

package capacityreporting

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpinformers "github.com/openshift/hypershift/client/informers/externalversions/hypershift/v1beta1"
	hcplisters "github.com/openshift/hypershift/client/listers/hypershift/v1beta1"

	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller"
	applyconfigv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/generated/applyconfiguration/capacityreport/v1alpha1"
	capacityreportclient "github.com/Azure/ARO-HCP/mgmt-agent/pkg/generated/clientset/versioned"
)

const (
	// ControllerName is the single source of truth for this controller's name.
	// It feeds the workqueue name, which surfaces as a Prometheus label.
	ControllerName = "capacityreporting"

	fieldManager       = "mgmt-agent-capacity-reporting"
	reportResourceName = "cluster"
	workerNodeLabel    = "aro-hcp.azure.com/role"
	workerLabelValue   = "worker"
	skuLabel           = "node.kubernetes.io/instance-type"
	ocmNamespacePrefix = "ocm-"

	// swiftNICsPerHCP is hardcoded because legacy clusters (v20240610preview)
	// may not use SWIFT NICs, which would drag the computed average below the
	// actual requirement for new clusters. All current API versions require
	// SWIFT, and every HCP consumes exactly 3 NICs.
	swiftNICsPerHCP int64 = 3
)

type CapacityReportController struct {
	nodeLister           corelisters.NodeLister
	podLister            corelisters.PodLister
	hcpLister            hcplisters.HostedControlPlaneLister
	metricsClient        metricsclientset.Interface
	capacityReportClient capacityreportclient.Interface
	nowFunc              func() time.Time

	hasSynced []cache.InformerSynced
	workqueue workqueue.TypedRateLimitingInterface[string]
}

func NewCapacityReportController(
	nodeInformer coreinformers.NodeInformer,
	podInformer coreinformers.PodInformer,
	hcpInformer hcpinformers.HostedControlPlaneInformer,
	metricsClient metricsclientset.Interface,
	capacityReportClient capacityreportclient.Interface,
	nowFunc func() time.Time,
) *CapacityReportController {
	if nowFunc == nil {
		nowFunc = time.Now
	}

	return &CapacityReportController{
		nodeLister:           nodeInformer.Lister(),
		podLister:            podInformer.Lister(),
		hcpLister:            hcpInformer.Lister(),
		metricsClient:        metricsClient,
		capacityReportClient: capacityReportClient,
		nowFunc:              nowFunc,
		hasSynced: []cache.InformerSynced{
			nodeInformer.Informer().HasSynced,
			podInformer.Informer().HasSynced,
			hcpInformer.Informer().HasSynced,
		},
		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: ControllerName},
		),
	}
}

func (c *CapacityReportController) Run(ctx context.Context) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting capacity reporting controller")

	logger.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.hasSynced...); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	go wait.UntilWithContext(ctx, c.runWorker, time.Second)

	go wait.UntilWithContext(ctx, func(ctx context.Context) {
		c.workqueue.Add(reportResourceName)
	}, time.Minute)

	logger.Info("Controller started")
	<-ctx.Done()
	logger.Info("Shutting down controller")

	return nil
}

func (c *CapacityReportController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *CapacityReportController) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}
	defer c.workqueue.Done(key)

	reconcileCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := c.syncOnce(reconcileCtx)
	if err == nil {
		c.workqueue.Forget(key)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("error syncing capacity report: %w", err))
	c.workqueue.AddRateLimited(key)
	return true
}

func (c *CapacityReportController) syncOnce(ctx context.Context) error {
	logger := klog.FromContext(ctx)

	nodes, err := c.nodeLister.List(workerNodeSelectorParsed)
	if err != nil {
		return fmt.Errorf("failed to list worker nodes: %w", err)
	}
	nodeCapacity := aggregateNodesBySKU(nodes)

	pods, err := c.podLister.List(everythingSelector())
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}
	requested := aggregatePodRequests(pods)

	// List cluster-wide because the Metrics API does not support namespace-prefix
	// filtering. aggregatePodMetrics filters to ocm-* namespaces client-side.
	// On purpose-built management clusters most pods are in ocm-* namespaces,
	// so the overhead is minimal.
	metricsCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	allMetrics, err := c.metricsClient.MetricsV1beta1().PodMetricses("").List(metricsCtx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pod metrics: %w", err)
	}
	usage := aggregatePodMetrics(allMetrics.Items)

	hcps, err := c.hcpLister.List(everythingSelector())
	if err != nil {
		return fmt.Errorf("failed to list HostedControlPlanes: %w", err)
	}
	hcpCount := countHCPs(hcps)

	applyConfig := buildReport(nodeCapacity, usage, requested, hcpCount, c.nowFunc())

	capacityReports := c.capacityReportClient.MgmtagentV1alpha1().CapacityReports()
	applyOptions := metav1.ApplyOptions{
		FieldManager: fieldManager,
		Force:        true,
	}

	// SSA with status subresources requires two mutations for initial creation:
	// 1. Apply creates the resource (status fields are ignored by the main endpoint)
	// 2. ApplyStatus sets the status via the status subresource
	// After creation, subsequent reconciles only call ApplyStatus (single mutation).
	_, err = capacityReports.ApplyStatus(ctx, applyConfig, applyOptions)
	if apierrors.IsNotFound(err) {
		if _, err := capacityReports.Apply(ctx, applyConfig, applyOptions); err != nil {
			return fmt.Errorf("failed to create capacity report: %w", err)
		}
		if _, err := capacityReports.ApplyStatus(ctx, applyConfig, applyOptions); err != nil {
			return fmt.Errorf("failed to apply capacity report status after creation: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to apply capacity report status: %w", err)
	}

	logger.V(4).Info("Capacity report updated")
	return nil
}

func buildReport(
	nodes []capacityreportv1alpha1.NodeSKUCapacity,
	usage corev1.ResourceList,
	requested corev1.ResourceList,
	hcpCount capacityreportv1alpha1.HostedControlPlaneCount,
	now time.Time,
) *applyconfigv1alpha1.CapacityReportApplyConfiguration {
	// SWIFT NICs are discrete, non-compressible resources: requested == used.
	// Pod requests already capture the exact NIC count via aggregatePodRequests.
	if swiftNIC, exists := requested[controller.SwiftNICResourceName]; exists {
		usage[controller.SwiftNICResourceName] = swiftNIC
	}

	nodeConfigs := make([]*applyconfigv1alpha1.NodeSKUCapacityApplyConfiguration, 0, len(nodes))
	for _, node := range nodes {
		nodeConfigs = append(nodeConfigs,
			applyconfigv1alpha1.NodeSKUCapacity().
				WithSKU(node.SKU).
				WithReady(node.Ready).
				WithNotReady(node.NotReady).
				WithAllocatable(node.Allocatable),
		)
	}

	statusConfig := applyconfigv1alpha1.CapacityReportStatus().
		WithLastReportedAt(metav1.NewTime(now)).
		WithNodes(nodeConfigs...).
		WithUsage(usage).
		WithRequested(requested)

	statusConfig = statusConfig.WithHostedControlPlanes(
		applyconfigv1alpha1.HostedControlPlaneCount().
			WithTotal(hcpCount.Total).
			WithReady(hcpCount.Ready),
	)

	if hcpCount.Ready > 0 {
		statusConfig = statusConfig.WithAverageHCPFootprint(
			computeAverageFootprint(usage, hcpCount.Ready),
		)
	}

	return applyconfigv1alpha1.CapacityReport(reportResourceName).
		WithStatus(statusConfig)
}

// aggregateNodesBySKU groups worker nodes by VM SKU and reports ready/notReady
// counts and total allocatable resources across ready nodes.
func aggregateNodesBySKU(nodes []*corev1.Node) []capacityreportv1alpha1.NodeSKUCapacity {
	type skuData struct {
		ready       int32
		notReady    int32
		allocatable corev1.ResourceList
	}
	bysku := make(map[string]*skuData)

	for _, node := range nodes {
		sku := node.Labels[skuLabel]
		if len(sku) == 0 {
			continue
		}

		data, exists := bysku[sku]
		if !exists {
			data = &skuData{allocatable: corev1.ResourceList{}}
			bysku[sku] = data
		}

		if isNodeReady(node) {
			data.ready++
			addResourceList(data.allocatable, filterTrackedResources(node.Status.Allocatable))
		} else {
			data.notReady++
		}
	}

	result := make([]capacityreportv1alpha1.NodeSKUCapacity, 0, len(bysku))
	for sku, data := range bysku {
		result = append(result, capacityreportv1alpha1.NodeSKUCapacity{
			SKU:         sku,
			Ready:       data.ready,
			NotReady:    data.notReady,
			Allocatable: data.allocatable,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].SKU < result[j].SKU
	})
	return result
}

// aggregatePodRequests sums resource requests across containers of
// non-terminal pods in ocm-* namespaces.
func aggregatePodRequests(pods []*corev1.Pod) corev1.ResourceList {
	total := corev1.ResourceList{}
	for _, pod := range pods {
		if !strings.HasPrefix(pod.Namespace, ocmNamespacePrefix) {
			continue
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		for _, container := range pod.Spec.Containers {
			addResourceList(total, filterTrackedResources(container.Resources.Requests))
		}
	}
	return total
}

// aggregatePodMetrics sums actual resource usage from PodMetrics for pods in
// ocm-* namespaces.
func aggregatePodMetrics(metrics []metricsv1beta1.PodMetrics) corev1.ResourceList {
	total := corev1.ResourceList{}
	for _, podMetric := range metrics {
		if !strings.HasPrefix(podMetric.Namespace, ocmNamespacePrefix) {
			continue
		}
		for _, container := range podMetric.Containers {
			addResourceList(total, filterTrackedResources(container.Usage))
		}
	}
	return total
}

// countHCPs counts total and ready HostedControlPlanes.
func countHCPs(hcps []*hypershiftv1beta1.HostedControlPlane) capacityreportv1alpha1.HostedControlPlaneCount {
	count := capacityreportv1alpha1.HostedControlPlaneCount{}
	for _, hcp := range hcps {
		if !strings.HasPrefix(hcp.Namespace, ocmNamespacePrefix) {
			continue
		}
		count.Total++
		if isHCPAvailable(hcp) {
			count.Ready++
		}
	}
	return count
}

// computeAverageFootprint divides total usage by the number of ready HCPs.
// SWIFT NICs are hardcoded: every new cluster requires swiftNICsPerHCP NICs
// regardless of what legacy clusters actually consume.
func computeAverageFootprint(usage corev1.ResourceList, readyCount int32) corev1.ResourceList {
	if readyCount <= 0 {
		return nil
	}
	result := corev1.ResourceList{}
	for resourceName, quantity := range usage {
		if resourceName == controller.SwiftNICResourceName {
			continue
		}
		quotient := quantity.Value() / int64(readyCount)
		format := resource.DecimalSI
		if resourceName == corev1.ResourceMemory {
			format = resource.BinarySI
		}
		result[resourceName] = *resource.NewQuantity(quotient, format)
	}
	result[controller.SwiftNICResourceName] = *resource.NewQuantity(swiftNICsPerHCP, resource.DecimalSI)
	return result
}

func isNodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func isHCPAvailable(hcp *hypershiftv1beta1.HostedControlPlane) bool {
	for _, condition := range hcp.Status.Conditions {
		if condition.Type == string(hypershiftv1beta1.HostedControlPlaneAvailable) {
			return condition.Status == metav1.ConditionTrue
		}
	}
	return false
}

// filterTrackedResources returns only the resources we track: memory and
// swift-nic.
func filterTrackedResources(resources corev1.ResourceList) corev1.ResourceList {
	filtered := corev1.ResourceList{}
	if quantity, exists := resources[corev1.ResourceMemory]; exists {
		filtered[corev1.ResourceMemory] = quantity
	}
	if quantity, exists := resources[controller.SwiftNICResourceName]; exists {
		filtered[controller.SwiftNICResourceName] = quantity
	}
	return filtered
}

func addResourceList(target, source corev1.ResourceList) {
	for resourceName, quantity := range source {
		existing := target[resourceName]
		existing.Add(quantity)
		target[resourceName] = existing
	}
}

var workerNodeSelectorParsed = func() labels.Selector {
	s, err := labels.Parse(workerNodeLabel + "=" + workerLabelValue)
	if err != nil {
		panic(fmt.Sprintf("BUG: invalid worker node selector: %v", err))
	}
	return s
}()

func everythingSelector() labels.Selector {
	return labels.Everything()
}
