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

package metrics

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	sharedmetrics "github.com/Azure/ARO-HCP/backend/pkg/controllers/shared/metrics"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Handler is a local alias for sharedmetrics.Handler so that test files
// that previously lived in the shared/metrics package continue to compile.
type Handler[T arm.CosmosPersistable] = sharedmetrics.Handler[T]

// Controller is a local copy of sharedmetrics.Controller with only
// the fields and methods needed by the test. The shared/metrics
// Controller struct has unexported fields that are not accessible
// from this package, so we duplicate the test-relevant subset.
type Controller[T arm.CosmosPersistable] struct {
	name    string
	indexer cache.Indexer
	handler sharedmetrics.Handler[T]
}

func (c *Controller[T]) syncResource(ctx context.Context, key string) error {
	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		c.handler.Delete(key)
		return nil
	}

	typed, ok := obj.(T)
	if !ok {
		logger := utils.LoggerFromContext(ctx)
		logger.Info("unexpected object type in indexer", "key", key, "controller", c.name, "type", fmt.Sprintf("%T", obj))
		c.handler.Delete(key)
		return nil
	}

	c.handler.Sync(ctx, typed)
	return nil
}

// resourceIDMetricLabel wraps the now-exported sharedmetrics function.
func resourceIDMetricLabel(resourceID *azcorearm.ResourceID) string {
	return sharedmetrics.ResourceIDMetricLabel(resourceID)
}

// subscriptionIDMetricLabel wraps the now-exported sharedmetrics function.
func subscriptionIDMetricLabel(resourceID *azcorearm.ResourceID) string {
	return sharedmetrics.SubscriptionIDMetricLabel(resourceID)
}

// resourceIDStoreKeyForObject computes a store key from a Cosmos-backed object
// (or a tombstone wrapping one).
func resourceIDStoreKeyForObject(obj interface{}) (string, error) {
	const maxUnwrapDepth = 5
	current := obj
	for i := 0; i < maxUnwrapDepth; i++ {
		switch v := current.(type) {
		case arm.CosmosPersistable:
			return resourceIDStoreKey(v)
		case cache.DeletedFinalStateUnknown:
			if v.Obj != nil {
				current = v.Obj
				continue
			}
			if v.Key != "" {
				return strings.ToLower(v.Key), nil
			}
			return "", fmt.Errorf("tombstone missing key and object")
		case *cache.DeletedFinalStateUnknown:
			if v.Obj != nil {
				current = v.Obj
				continue
			}
			if v.Key != "" {
				return strings.ToLower(v.Key), nil
			}
			return "", fmt.Errorf("tombstone missing key and object")
		default:
			return "", fmt.Errorf("unexpected object type %T", current)
		}
	}

	return "", fmt.Errorf("tombstone exceeded max unwrap depth")
}

// resourceIDStoreKey computes a store key from a CosmosPersistable.
func resourceIDStoreKey(obj arm.CosmosPersistable) (string, error) {
	cosmosData := obj.GetCosmosData()
	if cosmosData == nil || cosmosData.GetResourceID() == nil {
		return "", fmt.Errorf("object %T is missing a resource ID", obj)
	}
	return sharedmetrics.ResourceIDMetricLabel(cosmosData.GetResourceID()), nil
}

func newTestCluster(t *testing.T, name string, state arm.ProvisioningState, createdAt *time.Time) *api.HCPOpenShiftCluster {
	t.Helper()

	var systemData *arm.SystemData
	if createdAt != nil {
		systemData = &arm.SystemData{CreatedAt: createdAt}
	}

	return &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + name)),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:         api.Must(azcorearm.ParseResourceID("/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + name)),
				SystemData: systemData,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: state,
		},
	}
}
