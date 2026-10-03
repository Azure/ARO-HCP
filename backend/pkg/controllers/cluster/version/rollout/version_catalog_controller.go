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
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/versionpolicy"
)

const VersionCatalogControllerName = "OpenShiftVersionCatalog"

// Every rollout change projects into the same regional Resources document.
type versionCatalogKey struct{}

func (versionCatalogKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(
		metadataapi.Must(coreapihelpers.ToOpenShiftVersionCatalogResourceID(coreapi.OpenShiftVersionCatalogName)))...)
}

type versionCatalogSyncer struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
	rolloutLister     fleetlisters.ControlPlaneVersionRolloutLister
	enqueuer          interface {
		EnqueueAfter(versionCatalogKey, time.Duration)
	}
}

// NewOpenShiftVersionCatalogController publishes the customer-facing view of
// internal version rollouts in Resources so the frontend can answer discovery with
// a local snapshot read. This projection keeps fleet rollout details backend-owned
// and decouples public request latency from upstream discovery and selection.
// It advertises minor version and channel group pairs; a pair becomes available
// as soon as its rollout has a selected exact version.
//
// The Resources snapshot is an exact projection subject to the public support
// floor. Withdrawal follows source state or policy, and writes occur only when
// public content changes. The frontend applies subscription visibility to this
// shared regional snapshot.
func NewOpenShiftVersionCatalogController(resourcesDBClient corecosmosstorage.ResourcesDBClient, fleetInformers fleetinformers.FleetInformers) controllerregistry.Runnable {
	informer, lister := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &versionCatalogSyncer{resourcesDBClient: resourcesDBClient, rolloutLister: lister}
	controller := controllerutil.NewTypedController(
		VersionCatalogControllerName, syncer.SyncOnce, controllerutils.ReconcileTotal)
	syncer.enqueuer = controller
	controller.AddCacheSyncs(informer.HasSynced)
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(VersionCatalogControllerName)...)
	_, err := informer.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { controller.Enqueue(versionCatalogKey{}) },
		UpdateFunc: func(any, any) { controller.Enqueue(versionCatalogKey{}) },
		// Every deletion, including a tombstone, triggers a full catalog projection.
		DeleteFunc: func(any) { controller.Enqueue(versionCatalogKey{}) },
	}, cache.HandlerOptions{Logger: &logger})
	if err != nil {
		panic(err)
	}
	// Seed even an empty informer; Run waits for its initial list before publishing.
	controller.Enqueue(versionCatalogKey{})
	return controller
}

func (c *versionCatalogSyncer) SyncOnce(ctx context.Context, key versionCatalogKey) error {
	// Schedule through the normal queue even when no rollouts remain.
	defer c.enqueuer.EnqueueAfter(key, 5*time.Minute)
	rollouts, err := c.rolloutLister.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list version rollouts: %w", err)
	}
	// Adapt legacy documents at the read boundary without hiding them from the
	// seeder's persisted backfill or mutating informer-owned objects.
	rollouts = slices.Clone(rollouts)
	for i, rollout := range rollouts {
		desired, err := fleetapihelpers.NormalizeRolloutVersion(rollout)
		if err != nil {
			return err
		}
		if desired != nil {
			rollouts[i] = desired
		}
	}
	crud := c.resourcesDBClient.OpenShiftVersionCatalogs()
	existing, err := crud.Get(ctx, coreapi.OpenShiftVersionCatalogName)
	if cosmosstorageutils.IsNotFoundError(err) {
		existing = nil
	} else if err != nil {
		return fmt.Errorf("failed to get version catalog: %w", err)
	}
	desired, err := reconcileVersionCatalog(rollouts, existing)
	if err != nil || desired == nil {
		return err
	}
	if existing == nil {
		_, err = crud.Create(ctx, desired, nil)
	} else {
		_, err = crud.Replace(ctx, desired, nil)
	}
	if err != nil {
		return fmt.Errorf("failed to publish version catalog: %w", err)
	}
	return nil
}

// reconcileVersionCatalog returns the exact public projection, or nil when the
// existing catalog is unchanged. It preserves inputs, including existing metadata.
func reconcileVersionCatalog(rollouts []*fleetapi.ControlPlaneVersionRollout, existing *coreapi.OpenShiftVersionCatalog) (*coreapi.OpenShiftVersionCatalog, error) {
	entries := make([]coreapi.OpenShiftVersionCatalogEntry, 0, len(rollouts))
	for _, rollout := range rollouts {
		if err := fleetapihelpers.ValidateRolloutVersion(rollout); err != nil {
			return nil, fmt.Errorf("failed to project version rollout: %w", err)
		}
		profile := rollout.Spec.Version
		if !versionpolicy.AtLeast(profile.ID, versionpolicy.MinimumPublicVersion) {
			continue
		}
		entries = append(entries, coreapi.OpenShiftVersionCatalogEntry{
			Version: profile, Available: rollout.Spec.BestExactVersion != nil,
		})
	}
	slices.SortFunc(entries, func(a, b coreapi.OpenShiftVersionCatalogEntry) int {
		if n := strings.Compare(a.Version.ID, b.Version.ID); n != 0 {
			return n
		}
		return strings.Compare(a.Version.ChannelGroup, b.Version.ChannelGroup)
	})

	if existing == nil {
		return &coreapi.OpenShiftVersionCatalog{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   metadataapi.Must(coreapihelpers.ToOpenShiftVersionCatalogResourceID(coreapi.OpenShiftVersionCatalogName)),
				PartitionKey: strings.ToLower(coreapi.ProviderNamespace),
			},
			Entries: entries,
		}, nil
	}
	if slices.Equal(existing.Entries, entries) {
		return nil, nil
	}
	desired := existing.DeepCopy()
	desired.Entries = entries
	return desired, nil
}
