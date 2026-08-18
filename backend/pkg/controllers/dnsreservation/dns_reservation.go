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

// Package dnsreservation contains the two controllers that manage the lifecycle
// of DNSReservation documents:
//
//   - DNSReservationController watches HCPOpenShiftClusters and reserves a unique
//     kube-apiserver DNS name for every cluster that declares a BaseDomainPrefix.
//   - DNSReservationCleanupController watches DNSReservation documents and reaps
//     orphaned, expired, or superseded reservations, enforcing a one-week
//     cooldown before a freed name may be reused.
//
// See docs/dns-name-creation.md for the full design.
package dnsreservation

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/tzvatot/go-clean-lang/pkg/cleanlang"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DNSReservationControllerName is the single source of truth for this
// controller's name; it feeds the workqueue metric label, the ctx controller
// name, and the log fields via NewClusterWatchingController.
const DNSReservationControllerName = "DNSReservationController"

// charset is the alphabet for the random DNS suffix: lowercase letters and
// digits only, so the result is a valid DNS label component.
const charset = "abcdefghijklmnopqrstuvwxyz0123456789"

// randomSuffixLength is the number of random characters appended after the
// base domain prefix (e.g. "mycluster.a1b2").
const randomSuffixLength = 4

// dnsReservationController reserves a unique kube-apiserver DNS name for each
// cluster that declares a BaseDomainPrefix. It implements
// controllerutils.ClusterSyncer and is driven by NewClusterWatchingController,
// which handles the workqueue, informer wiring, per-cluster Controller status
// document, and rate-limited requeue on error.
type dnsReservationController struct {
	clock             utilsclock.PassiveClock
	resourcesDBClient corecosmosstorage.ResourcesDBClient

	// randMu guards rand because *rand.Rand is not safe for concurrent use and
	// SyncOnce runs across many worker goroutines.
	randMu                 sync.Mutex
	rand                   *rand.Rand
	cleanLanguageValidator cleanlang.Validator
}

// NewDNSReservationController builds the cluster-watching controller that
// reserves DNS names. It is wired to fire on HCPOpenShiftCluster and
// ServiceProviderCluster events (kube-applier ReadDesire events are irrelevant
// here, so nil is passed for that informer surface).
func NewDNSReservationController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	syncer := &dnsReservationController{
		clock:                  utilsclock.RealClock{},
		resourcesDBClient:      resourcesDBClient,
		rand:                   rand.New(rand.NewSource(time.Now().UnixNano())),
		cleanLanguageValidator: cleanlang.NewValidator(),
	}

	return controllerutils.NewClusterWatchingController(
		DNSReservationControllerName,
		resourcesDBClient,
		informers,
		nil, // kube-applier ReadDesire events carry no DNS-reservation signal
		10*time.Minute,
		syncer,
	)
}

// randomDNSPart returns a fresh, offensive-word-filtered random DNS label
// component of randomSuffixLength characters drawn from charset. It loops until
// go-clean-lang deems the candidate clean, so customer-visible DNS names never
// contain an accidental slur.
func (c *dnsReservationController) randomDNSPart() string {
	for {
		b := make([]byte, randomSuffixLength)
		c.randMu.Lock()
		for i := range b {
			b[i] = charset[c.rand.Intn(len(charset))]
		}
		c.randMu.Unlock()
		candidate := string(b)
		if c.cleanLanguageValidator.IsClean(candidate) {
			return candidate
		}
	}
}

// SyncOnce reserves a unique kube-apiserver DNS name for a single cluster.
//
// Lifecycle overview (see docs/dns-name-creation.md for the full picture):
//
//	Pending ──(bind succeeds)──▶ Bound ──(cluster gone / moved)──▶ PendingDeletion ──(1 week)──▶ deleted
//
// This controller owns the first two transitions; DNSReservationCleanupController
// owns the last two and is the eventual reaper of every reservation.
//
// The reservation name is "<baseDomainPrefix>.<4 random chars>". Uniqueness
// within a subscription is guaranteed by CosmosDB, NOT by a read-then-write
// check: the reservation's document id is derived deterministically from its
// resource ID, and the container is partitioned by subscription, so a second
// Create of the same name fails with HTTP 409 Conflict. We never pre-check
// whether a name is free; we simply try to Create it. If Create fails (conflict
// with a name that another cluster — or a previous run for this cluster — already
// took, or a transient outage), we return the error and the cluster-watching
// controller's rate-limited workqueue requeues us. On the next attempt we
// generate a brand-new random suffix, so retries naturally walk away from a
// contended name until one sticks. This is why no name is ever "special" or
// predictable: collisions self-heal by re-rolling.
//
// Crash-safety: we write the reservation document to Cosmos BEFORE recording the
// pointer on the ServiceProviderCluster, so a crash between the two writes leaves
// an orphaned Pending reservation (which the cleanup controller reaps once
// MustBindByTime passes) rather than a dangling pointer to a non-existent
// reservation. Every step is idempotent on re-entry.
func (c *dnsReservationController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	customerDesiredCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // cluster is gone, nothing to reserve
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HCP cluster: %w", err))
	}

	// A cluster only needs a reservation once it has declared a base domain prefix.
	if len(customerDesiredCluster.CustomerProperties.DNS.BaseDomainPrefix) == 0 {
		return nil
	}

	// The ServiceProviderCluster.Status.KubeAPIServerDNSReservation pointer is the
	// authoritative record of "this cluster already has a bound name". If it is
	// set, we are in steady state and there is nothing to do. This is also what
	// makes the controller idempotent: once bound, repeated syncs are no-ops.
	serviceProviderCluster, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create service provider cluster: %w", err))
	}
	if serviceProviderCluster.Status.KubeAPIServerDNSReservation != nil {
		return nil
	}

	dnsName := customerDesiredCluster.CustomerProperties.DNS.BaseDomainPrefix + "." + c.randomDNSPart()
	dnsReservationResourceID, err := coreapi.ToDNSReservationResourceID(key.SubscriptionID, dnsName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build DNS reservation resource ID: %w", err))
	}

	// Try to claim the name. Success means the name was free and is now ours;
	// failure (409 conflict or transient error) returns the error so the
	// rate-limited requeue tries again later with a fresh random suffix.
	// MustBindByTime gives the cleanup controller a deadline (61 min) after which
	// an unbound reservation is reaped, so a crash right after this Create cannot
	// leak the name forever.
	dnsReservation, err := c.resourcesDBClient.DNSReservations(key.SubscriptionID).Create(
		ctx,
		&coreapi.DNSReservation{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   dnsReservationResourceID,
				PartitionKey: strings.ToLower(key.SubscriptionID),
			},
			MustBindByTime: &metav1.Time{Time: c.clock.Now().Add(61 * time.Minute)},
			OwningCluster:  key.GetResourceID(),
			BindingState:   coreapi.BindingStatePending,
		},
		nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to reserve DNS name %q: %w", dnsName, err))
	}
	logger.Info("reserved DNS name", "kubeAPIServerDNSName", dnsReservationResourceID)

	// Record the pointer on the ServiceProviderCluster. This is the step that
	// "binds" the reservation to the cluster. If this Replace fails we return the
	// error: the reservation stays Pending and, on requeue, we notice the pointer
	// is still unset and reserve a NEW name (the previous Pending reservation is
	// later reaped by the cleanup controller once its MustBindByTime expires).
	serviceProviderCluster.Status.KubeAPIServerDNSReservation = dnsReservationResourceID
	_, err = c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, serviceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update service provider cluster: %w", err))
	}

	// Best-effort promotion to Bound. The binding is already complete once the
	// pointer above is persisted; flipping BindingState to Bound (and clearing
	// MustBindByTime) is just bookkeeping. If it fails we intentionally do NOT
	// return an error: the cleanup controller's steady-state reconcile (case 6)
	// will notice the cluster points at a still-Pending reservation and fix the
	// state to Bound. This keeps the create path resilient to a transient write.
	dnsReservation.BindingState = coreapi.BindingStateBound
	dnsReservation.MustBindByTime = nil
	if _, err := c.resourcesDBClient.DNSReservations(key.SubscriptionID).Replace(ctx, dnsReservation, nil); err != nil {
		logger.Error(err, "failed to mark DNS reservation as bound; cleanup controller will reconcile", "kubeAPIServerDNSName", dnsReservationResourceID)
	}

	return nil
}
