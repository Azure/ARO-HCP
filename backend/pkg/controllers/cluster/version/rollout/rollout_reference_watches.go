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

package rollout

import (
	"context"
	"errors"
	"fmt"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// watchRolloutReferences maps dependencies, not eligibility: reconciliation must
// read current references before deciding whether to create or retire a rollout.
func watchRolloutReferences(
	clusterInformer, spcInformer controllerutil.Notifier,
	clusterLister corelisters.ClusterLister,
	spcLister corelisters.ServiceProviderClusterLister,
	queue controllerutil.Enqueuer,
	name string,
	resyncDuration time.Duration,
) error {
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(name)...)
	ctx := utils.ContextWithLogger(utils.ContextWithControllerName(context.Background(), name), logger)
	enqueue := func(obj any) {
		if err := enqueueRolloutReferences(ctx, obj, clusterLister, spcLister, queue); err != nil {
			utilruntime.HandleErrorWithContext(ctx, err, "Cannot enqueue all rollout references; informer resync will retry")
		}
	}
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc: enqueue,
		UpdateFunc: func(oldObj, newObj any) {
			enqueue(oldObj)
			enqueue(newObj)
		},
		DeleteFunc: enqueue,
	}
	var errs []error
	for _, informer := range []controllerutil.Notifier{clusterInformer, spcInformer} {
		_, err := informer.AddEventHandlerWithOptions(handler, cache.HandlerOptions{Logger: &logger, ResyncPeriod: ptr.To(resyncDuration)})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func enqueueRolloutReferences(ctx context.Context, obj any, clusterLister corelisters.ClusterLister, spcLister corelisters.ServiceProviderClusterLister, queue controllerutil.Enqueuer) error {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	var cluster *coreapi.Cluster
	var spc *coreapi.ServiceProviderCluster
	var err error
	switch obj := obj.(type) {
	case *coreapi.Cluster:
		cluster = obj
		if cluster.ID == nil {
			return fmt.Errorf("cluster without resource ID")
		}
		id := cluster.ID
		spc, err = spcLister.Get(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
		if cosmosstorageutils.IsNotFoundError(err) {
			err = nil
		}
	case *coreapi.ServiceProviderCluster:
		spc = obj
		if spc.ResourceID == nil || spc.ResourceID.Parent == nil {
			return fmt.Errorf("service provider cluster without parent")
		}
		id := spc.ResourceID.Parent
		cluster, err = clusterLister.Get(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
	default:
		return fmt.Errorf("unexpected rollout reference object %T", obj)
	}
	var clusters []*coreapi.Cluster
	var spcs []*coreapi.ServiceProviderCluster
	if cluster != nil {
		clusters = append(clusters, cluster)
	}
	if spc != nil {
		spcs = append(spcs, spc)
	}
	refs, referenceErr := collectRolloutReferences(clusters, spcs)
	for profile := range refs {
		queue.Enqueue(controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: profile.ChannelGroup + "-" + profile.ID})
	}
	return errors.Join(err, referenceErr)
}
