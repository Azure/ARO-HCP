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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
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
	hcLister             hypershiftlisters.HostedClusterLister
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
	hcInformer hypershiftinformers.HostedClusterInformer,
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
		hcLister:             hcInformer.Lister(),
		metricsClient:        metricsClient,
		capacityReportClient: capacityReportClient,
		nowFunc:              nowFunc,
		hasSynced: []cache.InformerSynced{
			nodeInformer.Informer().HasSynced,
			podInformer.Informer().HasSynced,
			namespaceInformer.Informer().HasSynced,
			hcpInformer.Informer().HasSynced,
			hcInformer.Informer().HasSynced,
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

	pods, err := c.podLister.List(labels.Everything())
	if err != nil {
		return retainStatusWithCondition(existingStatus, metav1.ConditionFalse, capacityreportv1alpha1.ReasonDataCollectionFailed, err.Error(), now),
			fmt.Errorf("failed to list pods: %w", err)
	}

	// List cluster-wide because the Metrics API does not support namespace
	// filtering, pagination, or field selectors. The metrics-server serves an
	// in-memory snapshot from kubelet scrapes, not etcd-backed storage, so
	// Limit/Continue and ResourceVersion in ListOptions are silently ignored.
	// collectHCPResources filters to HCP namespaces client-side.
	// On purpose-built management clusters most pods are in HCP namespaces,
	// so the overhead is minimal.
	allMetrics, err := c.metricsClient.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return retainStatusWithCondition(existingStatus, metav1.ConditionFalse, capacityreportv1alpha1.ReasonDataCollectionFailed, err.Error(), now),
			fmt.Errorf("failed to list pod metrics: %w", err)
	}
	resources := collectHCPResources(pods, allMetrics.Items, collectHCPNamespaces(hcps))
	hostedControlPlanes := c.collectHostedControlPlanes(hcps, resources)

	condition := buildReportCurrentCondition(existingStatus.Conditions, metav1.ConditionTrue, capacityreportv1alpha1.ReasonDataCollected, "", now)
	return buildReport(nodeCapacity, hostedControlPlanes.Resources.Usage, hostedControlPlanes.Resources.Requested, hostedControlPlanes, condition, now), nil
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
		WithHostedControlPlanes(hostedControlPlanesApplyConfiguration(existingStatus.HostedControlPlanes))
	if existingStatus.Usage != nil {
		statusConfig.WithUsage(existingStatus.Usage)
	}
	if existingStatus.Requested != nil {
		statusConfig.WithRequested(existingStatus.Requested)
	}

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
		WithHostedControlPlanes(hostedControlPlanesApplyConfiguration(hostedControlPlanes))

	return applyconfigv1alpha1.CapacityReport(capacityReportResourceName).
		WithStatus(statusConfig)
}

func hostedControlPlanesApplyConfiguration(population capacityreportv1alpha1.HostedControlPlanes) *applyconfigv1alpha1.HostedControlPlanesApplyConfiguration {
	config := applyconfigv1alpha1.HostedControlPlanes().
		WithReadyResourceIDs(population.ReadyResourceIDs...).
		WithNotReadyResourceIDs(population.NotReadyResourceIDs...)
	if population.Resources != nil {
		config.WithResources(resourceTotalsApplyConfiguration(*population.Resources))
	}
	if population.ReadyResources != nil {
		config.WithReadyResources(resourceTotalsApplyConfiguration(*population.ReadyResources))
	}
	for _, bucket := range population.ByClusterSizingConfiguration {
		sizedConfig := applyconfigv1alpha1.HostedControlPlanesBySize().
			WithSize(bucket.Size).
			WithReadyResources(resourceTotalsApplyConfiguration(bucket.ReadyResources)).
			WithReadyCount(bucket.ReadyCount)
		config.WithByClusterSizingConfiguration(sizedConfig)
	}
	return config
}

func resourceTotalsApplyConfiguration(totals capacityreportv1alpha1.HostedControlPlaneResourceTotals) *applyconfigv1alpha1.HostedControlPlaneResourceTotalsApplyConfiguration {
	return applyconfigv1alpha1.HostedControlPlaneResourceTotals().WithUsage(totals.Usage).WithRequested(totals.Requested)
}

func (c *CapacityReportController) observedSize(hcp *hypershiftv1beta1.HostedControlPlane) string {
	namespace, name, found := strings.Cut(hcp.Annotations["hypershift.openshift.io/cluster"], "/")
	if !found || len(namespace) == 0 || len(name) == 0 || strings.Contains(name, "/") {
		return ""
	}
	hc, err := c.hcLister.HostedClusters(namespace).Get(name)
	if err != nil {
		return ""
	}
	size := hc.Labels[hypershiftv1beta1.HostedClusterSizeLabel]
	if len(size) == 0 || size != strings.ToLower(size) || size == "default" || len(validation.IsValidLabelValue(size)) > 0 {
		return ""
	}
	return size
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

// newZeroFloorResourceList returns a fresh ResourceList seeded with the
// scheduling-relevant resource types at zero quantity. Aggregation starts
// from this baseline instead of an empty map so usage/requested never end up
// with zero entries when there is no matching workload. usage/requested are
// granular (per-key) Server-Side Apply fields; a granular map whose owned key
// set drops to zero is collapsed to an invalid null during merge — a
// currently open, unfixed structured-merge-diff bug:
// https://github.com/kubernetes-sigs/structured-merge-diff/issues/305.
// Keeping a stable, non-empty key set (real data only adds to it or leaves
// entries at zero) sidesteps that collapse entirely.
func newZeroFloorResourceList() corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:              resource.MustParse("0"),
		corev1.ResourceMemory:           resource.MustParse("0"),
		controller.SwiftNICResourceName: resource.MustParse("0"),
	}
}

// collectHCPResources measures each namespace once, independently of its size
// attribution and readiness. Pod request semantics match the overall report.
func collectHCPResources(pods []*corev1.Pod, metrics []metricsv1beta1.PodMetrics, namespaces sets.Set[string]) map[string]capacityreportv1alpha1.HostedControlPlaneResourceTotals {
	resources := make(map[string]capacityreportv1alpha1.HostedControlPlaneResourceTotals, len(namespaces))
	for namespace := range namespaces {
		resources[namespace] = newResourceTotals()
	}
	for _, pod := range pods {
		totals, found := resources[pod.Namespace]
		if !found || (pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending) {
			continue
		}
		for _, container := range pod.Spec.Containers {
			addResourceList(totals.Requested, container.Resources.Requests)
		}
	}
	for _, metric := range metrics {
		totals, found := resources[metric.Namespace]
		if !found {
			continue
		}
		for _, container := range metric.Containers {
			addResourceList(totals.Usage, container.Usage)
		}
	}
	for _, totals := range resources {
		// SWIFT NICs are discrete, non-compressible resources: requested == used.
		totals.Usage[controller.SwiftNICResourceName] = totals.Requested[controller.SwiftNICResourceName].DeepCopy()
	}
	return resources
}

func newResourceTotals() capacityreportv1alpha1.HostedControlPlaneResourceTotals {
	return capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: newZeroFloorResourceList(), Requested: newZeroFloorResourceList()}
}

func sumHCPResources(hcps []*hypershiftv1beta1.HostedControlPlane, resources map[string]capacityreportv1alpha1.HostedControlPlaneResourceTotals) (capacityreportv1alpha1.HostedControlPlaneResourceTotals, capacityreportv1alpha1.HostedControlPlaneResourceTotals) {
	total := newResourceTotals()
	namespaces := sets.New[string]()
	for _, hcp := range hcps {
		totals := resources[hcp.Namespace]
		if !namespaces.Has(hcp.Namespace) {
			addResourceList(total.Usage, totals.Usage)
			addResourceList(total.Requested, totals.Requested)
			namespaces.Insert(hcp.Namespace)
		}
	}
	return total, sumReadyHCPResources(hcps, resources)
}

func sumReadyHCPResources(hcps []*hypershiftv1beta1.HostedControlPlane, resources map[string]capacityreportv1alpha1.HostedControlPlaneResourceTotals) capacityreportv1alpha1.HostedControlPlaneResourceTotals {
	ready := newResourceTotals()
	readyNamespaces := sets.New[string]()
	for _, hcp := range hcps {
		if isHCPAvailable(hcp) && !readyNamespaces.Has(hcp.Namespace) {
			totals := resources[hcp.Namespace]
			addResourceList(ready.Usage, totals.Usage)
			addResourceList(ready.Requested, totals.Requested)
			readyNamespaces.Insert(hcp.Namespace)
		}
	}
	return ready
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

func countReadyResourceIDs(hcps []*hypershiftv1beta1.HostedControlPlane, namespaceLister corelisters.NamespaceLister) int32 {
	readyResourceIDs := sets.New[string]()
	for _, hcp := range hcps {
		if !isHCPAvailable(hcp) {
			continue
		}
		namespace, err := namespaceLister.Get(hcp.Namespace)
		if err != nil {
			continue
		}
		if resourceID := namespace.Annotations[controllerutils.HcpClusterAzureResourceIdAnnotation]; len(resourceID) > 0 {
			readyResourceIDs.Insert(resourceID)
		}
	}
	return int32(readyResourceIDs.Len())
}

func (c *CapacityReportController) collectHostedControlPlanes(hcps []*hypershiftv1beta1.HostedControlPlane, resources map[string]capacityreportv1alpha1.HostedControlPlaneResourceTotals) capacityreportv1alpha1.HostedControlPlanes {
	var readyResourceIDs, notReadyResourceIDs []string
	for _, hcp := range hcps {
		namespace, err := c.namespaceLister.Get(hcp.Namespace)
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
	totals, readyTotals := sumHCPResources(hcps, resources)
	var buckets []capacityreportv1alpha1.HostedControlPlanesBySize
	byClusterSizingConfiguration := map[string][]*hypershiftv1beta1.HostedControlPlane{}
	for _, hcp := range hcps {
		if size := c.observedSize(hcp); len(size) > 0 {
			byClusterSizingConfiguration[size] = append(byClusterSizingConfiguration[size], hcp)
		}
	}
	for size, sizedHCPs := range byClusterSizingConfiguration {
		sizedReadyTotals := sumReadyHCPResources(sizedHCPs, resources)
		buckets = append(buckets, capacityreportv1alpha1.HostedControlPlanesBySize{
			Size: size, ReadyResources: sizedReadyTotals,
			ReadyCount: countReadyResourceIDs(sizedHCPs, c.namespaceLister),
		})
	}
	slices.SortFunc(buckets, func(a, b capacityreportv1alpha1.HostedControlPlanesBySize) int {
		return strings.Compare(a.Size, b.Size)
	})

	return capacityreportv1alpha1.HostedControlPlanes{
		ReadyResourceIDs:             readyResourceIDs,
		NotReadyResourceIDs:          notReadyResourceIDs,
		Resources:                    &totals,
		ReadyResources:               &readyTotals,
		ByClusterSizingConfiguration: buckets,
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
