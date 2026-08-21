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
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	metaac "k8s.io/client-go/applyconfigurations/meta/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftinformers "github.com/openshift/hypershift/client/informers/externalversions/hypershift/v1beta1"
	hypershiftlisters "github.com/openshift/hypershift/client/listers/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller"
	applyconfigv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/generated/applyconfiguration/capacityreport/v1alpha1"
	capacityreportclient "github.com/Azure/ARO-HCP/mgmt-agent/pkg/generated/clientset/versioned"
)

const (
	ControllerName = "capacity-reporting"

	fieldManager               = "mgmt-agent-capacity-reporting"
	capacityReportResourceName = "cluster"
	workerNodeLabel            = "aro-hcp.azure.com/role"
	workerLabelValue           = "worker"
	skuLabel                   = "node.kubernetes.io/instance-type"
	reportInterval             = 30 * time.Second
	dataCollectionTimeout      = 25 * time.Second
)

type CapacityReportController struct {
	nodeLister           corelisters.NodeLister
	podLister            corelisters.PodLister
	namespaceLister      corelisters.NamespaceLister
	hcpLister            hypershiftlisters.HostedControlPlaneLister
	metricsClient        metricsclientset.Interface
	capacityReportClient capacityreportclient.Interface
	nowFunc              func() time.Time

	hasSynced []cache.InformerSynced
}

func NewCapacityReportController(
	nodeInformer coreinformers.NodeInformer,
	podInformer coreinformers.PodInformer,
	namespaceInformer coreinformers.NamespaceInformer,
	hcpInformer hypershiftinformers.HostedControlPlaneInformer,
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
		namespaceLister:      namespaceInformer.Lister(),
		hcpLister:            hcpInformer.Lister(),
		metricsClient:        metricsClient,
		capacityReportClient: capacityReportClient,
		nowFunc:              nowFunc,
		hasSynced: []cache.InformerSynced{
			nodeInformer.Informer().HasSynced,
			podInformer.Informer().HasSynced,
			namespaceInformer.Informer().HasSynced,
			hcpInformer.Informer().HasSynced,
		},
	}
}

func (c *CapacityReportController) Run(ctx context.Context) error {
	defer utilruntime.HandleCrash()

	ctx = utils.ContextWithControllerName(ctx, ControllerName)
	logger := klog.FromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(ControllerName)...)
	ctx = klog.NewContext(ctx, logger)
	logger.Info("Starting controller")

	logger.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.hasSynced...); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	if err := c.ensureCapacityReport(ctx); err != nil {
		return err
	}

	logger.Info("Started")
	wait.UntilWithContext(ctx, func(ctx context.Context) {
		start := time.Now()
		err := c.syncOnce(ctx)
		syncDuration.Observe(time.Since(start).Seconds())
		if err != nil {
			syncErrorsTotal.Inc()
			utilruntime.HandleError(err)
		}
	}, reportInterval)

	logger.Info("Shutting down")
	return nil
}

// ensureCapacityReport creates the singleton CapacityReport if it does not yet
// exist. The controller owns this resource; syncOnce only updates its status.
func (c *CapacityReportController) ensureCapacityReport(ctx context.Context) error {
	_, err := c.capacityReportClient.MgmtagentV1alpha1().CapacityReports().Create(ctx, &capacityreportv1alpha1.CapacityReport{
		ObjectMeta: metav1.ObjectMeta{Name: capacityReportResourceName},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to ensure capacity report %q exists: %w", capacityReportResourceName, err)
	}
	return nil
}

func (c *CapacityReportController) syncOnce(ctx context.Context) error {
	logger := klog.FromContext(ctx)
	now := c.nowFunc()

	existing, err := c.capacityReportClient.MgmtagentV1alpha1().CapacityReports().Get(ctx, capacityReportResourceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get existing capacity report: %w", err)
	}

	collectionCtx, cancel := context.WithTimeout(ctx, dataCollectionTimeout)
	defer cancel()
	applyConfig, collectionErr := c.buildCapacityReportUpdate(collectionCtx, existing.Status, now)

	// The CR is ensured at controller startup, so ApplyStatus is sufficient.
	_, err = c.capacityReportClient.MgmtagentV1alpha1().CapacityReports().ApplyStatus(ctx, applyConfig, metav1.ApplyOptions{
		FieldManager: fieldManager,
		Force:        true,
	})
	if err != nil {
		if collectionErr != nil {
			return fmt.Errorf("failed to apply failure condition (%v): %w", err, collectionErr)
		}
		return fmt.Errorf("failed to apply capacity report status: %w", err)
	}

	if collectionErr != nil {
		return collectionErr
	}

	logger.V(4).Info("Capacity report updated")
	return nil
}

func (c *CapacityReportController) buildCapacityReportUpdate(ctx context.Context, existingStatus capacityreportv1alpha1.CapacityReportStatus, now time.Time) (*applyconfigv1alpha1.CapacityReportApplyConfiguration, error) {
	nodes, err := c.nodeLister.List(workerNodeSelectorParsed)
	if err != nil {
		return retainStatusWithCondition(existingStatus, metav1.ConditionFalse, capacityreportv1alpha1.ReasonDataCollectionFailed, err.Error(), now),
			fmt.Errorf("failed to list worker nodes: %w", err)
	}
	nodeCapacity := aggregateNodesBySKU(nodes)

	hcps, err := c.hcpLister.List(labels.Everything())
	if err != nil {
		return retainStatusWithCondition(existingStatus, metav1.ConditionFalse, capacityreportv1alpha1.ReasonDataCollectionFailed, err.Error(), now),
			fmt.Errorf("failed to list HostedControlPlanes: %w", err)
	}
	hcpNamespaces := collectHCPNamespaces(hcps)
	hostedControlPlanes := collectHostedControlPlanes(hcps, c.namespaceLister)

	pods, err := c.podLister.List(labels.Everything())
	if err != nil {
		return retainStatusWithCondition(existingStatus, metav1.ConditionFalse, capacityreportv1alpha1.ReasonDataCollectionFailed, err.Error(), now),
			fmt.Errorf("failed to list pods: %w", err)
	}
	requested := aggregatePodRequests(pods, hcpNamespaces)

	// List cluster-wide because the Metrics API does not support namespace
	// filtering, pagination, or field selectors. The metrics-server serves an
	// in-memory snapshot from kubelet scrapes, not etcd-backed storage, so
	// Limit/Continue and ResourceVersion in ListOptions are silently ignored.
	// aggregatePodMetrics filters to HCP namespaces client-side.
	// On purpose-built management clusters most pods are in HCP namespaces,
	// so the overhead is minimal.
	allMetrics, err := c.metricsClient.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return retainStatusWithCondition(existingStatus, metav1.ConditionFalse, capacityreportv1alpha1.ReasonDataCollectionFailed, err.Error(), now),
			fmt.Errorf("failed to list pod metrics: %w", err)
	}
	usage := aggregatePodMetrics(allMetrics.Items, hcpNamespaces)

	// SWIFT NICs are discrete, non-compressible resources: requested == used.
	// Pod requests already capture the exact NIC count via aggregatePodRequests.
	if swiftNIC, exists := requested[controller.SwiftNICResourceName]; exists {
		usage[controller.SwiftNICResourceName] = swiftNIC
	}

	condition := buildReportCurrentCondition(existingStatus.Conditions, metav1.ConditionTrue, capacityreportv1alpha1.ReasonDataCollected, "", now)
	return buildReport(nodeCapacity, usage, requested, hostedControlPlanes, condition, now), nil
}

func retainStatusWithCondition(existingStatus capacityreportv1alpha1.CapacityReportStatus, status metav1.ConditionStatus, reason, message string, now time.Time) *applyconfigv1alpha1.CapacityReportApplyConfiguration {
	condition := buildReportCurrentCondition(existingStatus.Conditions, status, reason, message, now)

	nodeConfigs := make([]*applyconfigv1alpha1.NodeSKUCapacityApplyConfiguration, 0, len(existingStatus.Nodes))
	for _, node := range existingStatus.Nodes {
		nodeConfigs = append(nodeConfigs, applyconfigv1alpha1.NodeSKUCapacity().
			WithSKU(node.SKU).WithReady(node.Ready).WithNotReady(node.NotReady).WithAllocatable(node.Allocatable))
	}

	statusConfig := applyconfigv1alpha1.CapacityReportStatus().
		WithConditions(condition).
		WithNodes(nodeConfigs...).
		WithUsage(existingStatus.Usage).
		WithRequested(existingStatus.Requested).
		WithHostedControlPlanes(applyconfigv1alpha1.HostedControlPlanes().
			WithReadyResourceIDs(existingStatus.HostedControlPlanes.ReadyResourceIDs...).
			WithNotReadyResourceIDs(existingStatus.HostedControlPlanes.NotReadyResourceIDs...))

	if existingStatus.LastReportedAt != nil {
		statusConfig = statusConfig.WithLastReportedAt(*existingStatus.LastReportedAt)
	}

	return applyconfigv1alpha1.CapacityReport(capacityReportResourceName).WithStatus(statusConfig)
}

func buildReportCurrentCondition(existingConditions []metav1.Condition, status metav1.ConditionStatus, reason, message string, now time.Time) *metaac.ConditionApplyConfiguration {
	transitionTime := now
	for _, existing := range existingConditions {
		if existing.Type == capacityreportv1alpha1.ConditionTypeReportCurrent && existing.Status == status {
			transitionTime = existing.LastTransitionTime.Time
			break
		}
	}
	return metaac.Condition().
		WithType(capacityreportv1alpha1.ConditionTypeReportCurrent).
		WithStatus(status).
		WithReason(reason).
		WithMessage(message).
		WithLastTransitionTime(metav1.NewTime(transitionTime)).
		WithObservedGeneration(0)
}

func buildReport(
	nodes []capacityreportv1alpha1.NodeSKUCapacity,
	usage corev1.ResourceList,
	requested corev1.ResourceList,
	hostedControlPlanes capacityreportv1alpha1.HostedControlPlanes,
	condition *metaac.ConditionApplyConfiguration,
	now time.Time,
) *applyconfigv1alpha1.CapacityReportApplyConfiguration {
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
		WithConditions(condition).
		WithLastReportedAt(metav1.NewTime(now)).
		WithNodes(nodeConfigs...).
		WithUsage(usage).
		WithRequested(requested).
		WithHostedControlPlanes(applyconfigv1alpha1.HostedControlPlanes().
			WithReadyResourceIDs(hostedControlPlanes.ReadyResourceIDs...).
			WithNotReadyResourceIDs(hostedControlPlanes.NotReadyResourceIDs...))

	return applyconfigv1alpha1.CapacityReport(capacityReportResourceName).
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
			addResourceList(data.allocatable, node.Status.Allocatable)
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
	slices.SortFunc(result, func(a, b capacityreportv1alpha1.NodeSKUCapacity) int {
		return strings.Compare(a.SKU, b.SKU)
	})
	return result
}

// aggregatePodRequests sums resource requests across containers of
// non-terminal pods in HostedControlPlane namespaces.
func aggregatePodRequests(pods []*corev1.Pod, hcpNamespaces sets.Set[string]) corev1.ResourceList {
	total := corev1.ResourceList{}
	for _, pod := range pods {
		if !hcpNamespaces.Has(pod.Namespace) {
			continue
		}
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}
		for _, container := range pod.Spec.Containers {
			addResourceList(total, container.Resources.Requests)
		}
	}
	return total
}

// aggregatePodMetrics sums actual resource usage from PodMetrics for pods in
// HostedControlPlane namespaces.
func aggregatePodMetrics(metrics []metricsv1beta1.PodMetrics, hcpNamespaces sets.Set[string]) corev1.ResourceList {
	total := corev1.ResourceList{}
	for _, podMetric := range metrics {
		if !hcpNamespaces.Has(podMetric.Namespace) {
			continue
		}
		for _, container := range podMetric.Containers {
			addResourceList(total, container.Usage)
		}
	}
	return total
}

// collectHCPNamespaces returns the set of namespaces where HostedControlPlane
// objects exist.
func collectHCPNamespaces(hcps []*hypershiftv1beta1.HostedControlPlane) sets.Set[string] {
	namespaces := sets.New[string]()
	for _, hcp := range hcps {
		namespaces.Insert(hcp.Namespace)
	}
	return namespaces
}

func collectHostedControlPlanes(hcps []*hypershiftv1beta1.HostedControlPlane, namespaceLister corelisters.NamespaceLister) capacityreportv1alpha1.HostedControlPlanes {
	var readyResourceIDs, notReadyResourceIDs []string
	for _, hcp := range hcps {
		namespace, err := namespaceLister.Get(hcp.Namespace)
		if err != nil {
			continue
		}
		resourceID := namespace.Annotations[controllerutils.HcpClusterAzureResourceIdAnnotation]
		if len(resourceID) == 0 {
			continue
		}
		if isHCPAvailable(hcp) {
			readyResourceIDs = append(readyResourceIDs, resourceID)
		} else {
			notReadyResourceIDs = append(notReadyResourceIDs, resourceID)
		}
	}
	slices.Sort(readyResourceIDs)
	readyResourceIDs = slices.Compact(readyResourceIDs)
	slices.Sort(notReadyResourceIDs)
	notReadyResourceIDs = slices.Compact(notReadyResourceIDs)
	return capacityreportv1alpha1.HostedControlPlanes{
		ReadyResourceIDs:    readyResourceIDs,
		NotReadyResourceIDs: notReadyResourceIDs,
	}
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
