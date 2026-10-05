// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package autonode

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// AutoNodeStatusControllerName is the controller name.
const AutoNodeStatusControllerName = "AutoNodeStatus"

// autoNodeStatusSyncer mirrors the observed HostedCluster.status.autoNode
// onto ServiceProviderCluster.Status.AutoNode.
//
// Karpenter-provisioned nodes are not HyperShift NodePool machines and never
// appear in the ARM node pool list, so without this mirror nothing in Cosmos
// records how large a Karpenter-enabled cluster has grown. The counts
// themselves are computed by the karpenter-operator against the guest cluster
// and published on HostedControlPlane.status.autoNode, which the
// hypershift-operator copies onto the HostedCluster; this controller only reads
// the already-mirrored HostedCluster, so no guest-cluster access is involved.
//
// Kept separate from autoNodeSyncer deliberately: that one writes desired state
// (an ApplyDesire carrying spec.autoNode), this one reads observed state. Same
// split as the version package's desired/active controllers.
type autoNodeStatusSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	readDesireLister             kubeapplierlisters.ReadDesireLister
	applyDesireLister            kubeapplierlisters.ApplyDesireLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ controllerutils.ClusterSyncer = (*autoNodeStatusSyncer)(nil)

// NewAutoNodeStatusController wires the controller that populates
// ServiceProviderCluster.Status.AutoNode from the per-cluster ReadDesire's
// observed HostedCluster.
func NewAutoNodeStatusController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister kubeapplierlisters.ReadDesireLister,
) controllerutils.Controller {
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()
	_, clusterLister := informers.Clusters()

	syncer := &autoNodeStatusSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		readDesireLister:             readDesireLister,
		applyDesireLister:            applyDesireLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewClusterWatchingController(
		AutoNodeStatusControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *autoNodeStatusSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cached Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	hostedCluster, _, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HostedCluster from ReadDesire: %w", err))
	}
	if hostedCluster == nil {
		// ReadDesire absent or kubeContent not yet observed; we are retriggered
		// once the kube-applier writes status.
		return nil
	}

	newAutoNode := autoNodeStatusFromHostedCluster(hostedCluster)

	// AutoNode status normally comes from the HostedCluster. However, an older
	// management cluster may reject the AutoNode fields before they reach that
	// object. In that case, a nil status could mean either "not requested" or
	// "requested but rejected". Copy the ApplyDesire failure here so callers
	// can tell the difference. This does not enable AutoNode on clusters that
	// did not request it, and the failure does not stop cluster provisioning.
	if deliveryCondition, err := c.autoNodeDeliveryCondition(ctx, key); err != nil {
		return utils.TrackError(fmt.Errorf("failed to get AutoNode ApplyDesire: %w", err))
	} else if deliveryCondition != nil {
		if newAutoNode == nil {
			newAutoNode = &coreapi.ServiceProviderClusterAutoNodeStatus{}
		}
		newAutoNode.DeliveryCondition = deliveryCondition
	}

	cachedServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it; we'll be re-enqueued
		// via the ServiceProviderCluster informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	// Node counts move on every scale event, so the compare is what keeps this
	// from issuing a Cosmos Replace on every single resync.
	oldAutoNode := cachedServiceProviderCluster.Status.AutoNode
	if !controllerutil.NeedsUpdate(oldAutoNode, newAutoNode) {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)
	logger.Info("AutoNode status changed", "oldAutoNode", oldAutoNode, "newAutoNode", newAutoNode)

	replacement := cachedServiceProviderCluster.DeepCopy()
	replacement.Status.AutoNode = newAutoNode
	serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if _, err := serviceProviderClustersCosmosClient.Replace(ctx, replacement, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// autoNodeDeliveryCondition returns the distilled form of the AutoNode
// ApplyDesire's own SuccessfullyApplied (falling back to the legacy
// Successful) condition, but only when that condition is present and not
// True - a healthy or not-yet-observed delivery reports nil, since
// autoNodeStatusFromHostedCluster's HostedCluster-derived Condition already
// covers the healthy case once the object has actually been reconciled.
func (c *autoNodeStatusSyncer) autoNodeDeliveryCondition(ctx context.Context, key controllerutils.HCPClusterKey) (*coreapi.ServiceProviderClusterAutoNodeCondition, error) {
	applyDesire, err := c.applyDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, autoNodeApplyDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var deliveryCondition *metav1.Condition
	for _, conditionType := range []string{kubeapplierapi.ConditionTypeSuccessfullyApplied, kubeapplierapi.ConditionTypeSuccessful} {
		if c := meta.FindStatusCondition(applyDesire.Status.Conditions, conditionType); c != nil {
			deliveryCondition = c
			break
		}
	}
	if deliveryCondition == nil || deliveryCondition.Status == metav1.ConditionTrue {
		return nil, nil
	}
	return &coreapi.ServiceProviderClusterAutoNodeCondition{
		Status:  string(deliveryCondition.Status),
		Reason:  deliveryCondition.Reason,
		Message: deliveryCondition.Message,
	}, nil
}

// autoNodeStatusFromHostedCluster distills the observed
// HostedCluster.status.autoNode and its AutoNodeEnabled condition into their
// coreapi form.
//
// hypershift declares status.autoNode as a value (not a pointer) whose fields
// are all optional, and the hypershift-operator zeroes the whole struct when
// AutoNode is disabled - so the node-count fields alone cannot distinguish
// "AutoNode enabled but zero nodes currently provisioned" from "AutoNode not
// enabled": both read as all-nil. The AutoNodeEnabled condition is what
// resolves that ambiguity - hypershift reports it (e.g. with reason
// AutoNodeNotConfigured, AutoNodeProgressing, or AutoNodeEvaluationFailed)
// regardless of whether any nodes exist yet, so its presence is what "not yet
// observed at all" is mapped to nil against.
func autoNodeStatusFromHostedCluster(hostedCluster *hsv1beta1.HostedCluster) *coreapi.ServiceProviderClusterAutoNodeStatus {
	observed := hostedCluster.Status.AutoNode
	condition := meta.FindStatusCondition(hostedCluster.Status.Conditions, string(hsv1beta1.AutoNodeEnabled))

	if condition == nil && observed.NodeCount == nil && observed.NodeClaimCount == nil && observed.VCPUs == nil {
		return nil
	}

	status := &coreapi.ServiceProviderClusterAutoNodeStatus{
		NodeCount:      observed.NodeCount,
		NodeClaimCount: observed.NodeClaimCount,
		VCPUs:          observed.VCPUs,
	}
	if condition != nil {
		status.Enabled = ptr.To(condition.Status == metav1.ConditionTrue)
		status.Condition = &coreapi.ServiceProviderClusterAutoNodeCondition{
			Status:  string(condition.Status),
			Reason:  condition.Reason,
			Message: condition.Message,
		}
	}
	return status
}
