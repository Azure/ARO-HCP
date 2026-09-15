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
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// FetchManagedIdentitiesInfoControllerName is the single source of truth
	// for this controller's name. It is used for the workqueue name (a Prometheus
	// label), context/logger controller name, and log fields.
	FetchManagedIdentitiesInfoControllerName = "FetchManagedIdentitiesInfo"

	// managedIdentitiesRecheckInterval is the base interval before re-querying
	// identity metadata sources when ClientID/PrincipalID/TenantID are already
	// resolved. Combined with managedIdentitiesRecheckJitter via wait.Jitter.
	managedIdentitiesRecheckInterval = 1 * time.Hour
	// managedIdentitiesRecheckJitter is the wait.Jitter factor applied to
	// managedIdentitiesRecheckInterval when setting this controller's
	// Spec.EarliestRecheckTimesByController entry.
	managedIdentitiesRecheckJitter = 0.5
)

// identityToResolve describes one unique managed identity resource ID for which
// FetchManagedIdentitiesInfo should retrieve metadata from the sources that
// apply to it.
type identityToResolve struct {
	// resourceID is the ARM resource ID of the user-assigned managed identity.
	// ServiceProviderCluster map keys must ToLower the string form because
	// ResourceID.String() is not a stable fully-lowercased key.
	resourceID *azcorearm.ResourceID
	// isControlPlaneOperatorIdentity is true when this resource ID is specified
	// as a Cluster's control-plane operator. Those identities are registered
	// with the Managed Identities Data Plane when that service is available. This
	// means that the Managed Identities Data Plane metadata source or
	// the Hardcoded Identity metadata source applies to it depending on the availability
	// of the real Managed Identities Data Plane service.
	isControlPlaneOperatorIdentity bool
	// isDataPlaneOperatorIdentity is true when this resource ID is specified as a
	// Cluster's data-plane operator. This means that the ARM User Assigned Identities metadata source applies to it.
	isDataPlaneOperatorIdentity bool
	// isServiceManagedIdentity is true when this resource ID is specified as the Cluster's
	// Service Managed Identity.  This means that the Managed Identities Data Plane metadata source or
	// the Hardcoded Identity metadata source applies to it depending on the availability
	// of the real Managed Identities Data Plane service.
	// ARM User Assigned Identities Get is skipped only when this identity
	// is SMI-only: end-users are not asked to grant the SMI read on itself. If
	// the same resource ID is also a control-plane or data-plane operator, ARM
	// still applies because we ask end-users to grant the role over those.
	isServiceManagedIdentity bool
}

// registeredWithManagedIdentitiesDataplane reports whether the identity
// is configured with the Managed Identities Data Plane integration. This is, whether it's a MSI-based identity
// Cluster's control plane operators and the cluster's service managed identity
// are MSI-based identities.
func (i *identityToResolve) registeredWithManagedIdentitiesDataplane() bool {
	return i.isControlPlaneOperatorIdentity || i.isServiceManagedIdentity
}

// appliesToARMUserAssignedIdentitiesAPI reports whether the ARM User Assigned
// Identities metadata source applies to this identity. Control-plane and data-plane
// operator identities apply because end-users are asked to grant the SMI
// read on them. SMI-only identities are not: end-users are not asked to grant
// the SMI read on itself. When the same resource ID is also a control-plane or
// data-plane operator, ARM still applies.
func (i *identityToResolve) appliesToARMUserAssignedIdentitiesAPI() bool {
	return i.isControlPlaneOperatorIdentity || i.isDataPlaneOperatorIdentity
}

// fetchManagedIdentitiesInfoSyncer fetches ClientID, PrincipalID, and TenantID
// for every cluster's managed identity in .Spec.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
// from the metadata sources that apply to that identity and writes them onto ServiceProviderCluster.Status.ManagedIdentityDetails.
type fetchManagedIdentitiesInfoSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	// hardcodedIdentity is the identity used for the cluster's control plane operators and the cluster's service managed identity when
	// the Data Plane Service is not available in the environment.
	// A non-nil value signals that the Data Plane Service is not available, and in that case the hardcoded identity
	// is used as the identity for the cluster's control plane and operators and the cluster's service managed identity.
	// Exactly one of hardcodedIdentity and fpaMIdataplaneClientBuilder must be set.
	hardcodedIdentity *azureclient.HardcodedIdentity
	// fpaMIdataplaneClientBuilder is the client builder used to build the clients to the real Managed Identities
	// Data Plane service.
	// A non-nil value signals that the Managed Identities Data Plane service is available.
	// Exactly one of hardcodedIdentity and fpaMIdataplaneClientBuilder must be set.
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder
	// smiClientBuilder is used to build the ARM User Assigned Identities client
	// authenticated as the cluster's Service Managed Identity.
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

var _ controllerutils.ClusterSyncer = (*fetchManagedIdentitiesInfoSyncer)(nil)

// NewFetchManagedIdentitiesInfoController creates a cluster-watching controller
// that resolves ClientID, PrincipalID, and TenantID for every cluster's managed
// identity in .Spec.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities of the cluster and persists them on
// ServiceProviderCluster.Status.ManagedIdentityDetails.
//
// The identities come from
// CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
// (control plane operators, data plane operators, and the service managed
// identity), de-duplicated by lowercased resource ID.
//
// hardcodedIdentity and fpaMIdataplaneClientBuilder are mutually exclusive
// optional dependencies: exactly one is set for a given environment.
// hardcodedIdentity is set (and the builder is nil) when the real Managed
// Identities Data Plane service is not available. The builder is set (and
// hardcodedIdentity is nil) when that service is available.
//
// For each identity the controller fills the sources that apply:
//   - Hardcoded identity, when hardcodedIdentity is set, and only for
//     identities registered with the Managed Identities Data Plane service (control plane operators and
//     the service managed identity).
//   - The real Managed Identities Data Plane, when fpaMIdataplaneClientBuilder
//     is set, and only for those same Managed Identities Data Plane service-registered identities.
//   - ARM User Assigned Identities (authenticated as the cluster's service
//     managed identity) for control-plane and data-plane operator identities.
//     SMI-only identities are skipped because end-users are not asked to grant
//     the SMI read on itself. If the same resource ID is also a control-plane
//     or data-plane operator, ARM still applies.
//
// Source failures are accumulated and processing continues so successfully
// resolved sources and identities can still be persisted. The accumulated
// error is returned so the workqueue retries. This controller's
// Spec.EarliestRecheckTimesByController entry is only set when every
// applicable source succeeded (identity not found is not a source failure).
func NewFetchManagedIdentitiesInfoController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	hardcodedIdentity *azureclient.HardcodedIdentity,
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder,
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
) controllerutils.Controller {
	if hardcodedIdentity == nil && fpaMIdataplaneClientBuilder == nil {
		panic("exactly one of hardcodedIdentity and fpaMIdataplaneClientBuilder must be set")
	}
	if hardcodedIdentity != nil && fpaMIdataplaneClientBuilder != nil {
		panic("exactly one of hardcodedIdentity and fpaMIdataplaneClientBuilder must be set")
	}

	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &fetchManagedIdentitiesInfoSyncer{
		clock:                        clock,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
		hardcodedIdentity:            hardcodedIdentity,
		fpaMIdataplaneClientBuilder:  fpaMIdataplaneClientBuilder,
		smiClientBuilder:             smiClientBuilder,
	}

	controller := controllerutils.NewClusterWatchingController(
		FetchManagedIdentitiesInfoControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)

	return controller
}

// needsWork reports whether identity metadata sources should be queried.
// Spec.EarliestRecheckTimesByController[FetchManagedIdentitiesInfoControllerName]
// is honored only when the identities stored on the ServiceProviderCluster still
// match desiredIdentities. On mismatch it returns true immediately. When
// identities match, it returns false while that recheck time is in the future,
// and true when it is nil or already past. Callers must skip needsWork entirely
// when the cluster is deleting.
func (c *fetchManagedIdentitiesInfoSyncer) needsWork(existingServiceProviderCluster *coreapi.ServiceProviderCluster, desiredIdentities map[string]*identityToResolve) bool {
	if c.desiredIdentityResourceIDsMatchServiceProviderCluster(desiredIdentities, existingServiceProviderCluster) {
		earliestRecheckTime := existingServiceProviderCluster.Spec.EarliestRecheckTimesByController[FetchManagedIdentitiesInfoControllerName]
		if earliestRecheckTime != nil && c.clock.Now().Before(earliestRecheckTime.Time) {
			return false
		}
	}

	return true
}

// SyncOnce resolves ClientID, PrincipalID, and TenantID for every cluster's managed
// identity in .Spec.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
// and persists them on ServiceProviderCluster.Status.ManagedIdentityDetails.
//
// It returns nil without writing when the Cluster or ServiceProviderCluster is
// missing, or when the cluster is deleting. Collect errors return before any
// write so a partial desired set cannot prune stored identities. When
// needsWork is false it returns nil without querying sources.
//
// Each reconcile rebuilds ManagedIdentityDetails from the desired identity
// set. Applicable sources start as empty IdentityMetadataValue placeholders.
// Resolve methods return independent maps that SyncOnce assigns onto the
// matching MetadataFrom* field. Keys omitted from a resolver map (including a
// nil map on total query failure) keep that empty placeholder. Source failures
// are accumulated so successfully resolved sources are still persisted. The
// joined error is returned so the workqueue retries.
// Spec.EarliestRecheckTimesByController[FetchManagedIdentitiesInfoControllerName]
// is set only when every applicable source succeeded.
func (c *fetchManagedIdentitiesInfoSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	// If the cluster is being deleted we do not fetch the identity metadata.
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	existingServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}

	desiredIdentities, collectErrs := c.collectIdentitiesToResolve(existingCluster)
	if len(collectErrs) > 0 {
		// If we failed to collect the identities it means some error that should not occur happened. In that case
		// we return early.
		return errors.Join(collectErrs...)
	}
	if !c.needsWork(existingServiceProviderCluster, desiredIdentities) {
		return nil
	}

	// We reconstruct ManagedIdentityDetails from zero on each pass. For every
	// identity, each applicable source starts as an empty IdentityMetadataValue
	// (all fields within it nil). That empty value is what gets persisted when this pass
	// cannot resolve the source: the resolver omits that key, or returns a nil
	// map on a total query failure. Resolvers overwrite a placeholder only when
	// they return a value for that key.
	replacementManagedIdentityDetails := make(map[string]*coreapi.ManagedIdentityMetadata, len(desiredIdentities))
	for resourceIDKey, identity := range desiredIdentities {
		replacementManagedIdentityDetails[resourceIDKey] = &coreapi.ManagedIdentityMetadata{
			ResourceID: coreapi.DeepCopyResourceID(identity.resourceID),
		}
		if identity.appliesToARMUserAssignedIdentitiesAPI() {
			replacementManagedIdentityDetails[resourceIDKey].MetadataFromARMUserAssignedIdentitiesAPI = &coreapi.IdentityMetadataValue{}
		}
		if identity.registeredWithManagedIdentitiesDataplane() {
			if c.hardcodedIdentity != nil {
				replacementManagedIdentityDetails[resourceIDKey].MetadataFromHardcodedIdentity = &coreapi.IdentityMetadataValue{}
			} else {
				replacementManagedIdentityDetails[resourceIDKey].MetadataFromManagedIdentitiesDataplaneService = &coreapi.IdentityMetadataValue{}
			}
		}
	}

	var errs []error
	if c.hardcodedIdentity != nil {
		// If hardcodedIdentity is set it means that the Managed Identities Data Plane service is not available and we are
		// using the hardcoded identity for the control plane operators and the service managed identity (smi-based identities).
		hardcodedIdentityMetadata := c.resolveManagedIdentitiesMetadataFromHardcodedIdentity(desiredIdentities)
		for resourceIDKey, metadata := range hardcodedIdentityMetadata {
			replacementManagedIdentityDetails[resourceIDKey].MetadataFromHardcodedIdentity = metadata
		}
	} else {
		// If hardcodedIdentity is not set it means that the Managed Identities Data Plane service is available.
		// We resolve the metadata from the Managed Identities Data Plane service for the control plane operators and
		// the service managed identity (smi-based identities).
		dataplaneMetadata, dataplaneErrs := c.resolveManagedIdentitiesMetadataFromManagedIdentitiesDataplaneService(ctx, existingCluster, desiredIdentities)
		errs = append(errs, dataplaneErrs...)
		// Only successfully resolved identities are in dataplaneMetadata.
		// Failed identities are omitted, and a total query failure returns a
		// nil map, so those keys keep the empty IdentityMetadataValue from
		// the init loop. That empty value is persisted alongside the error.
		for resourceIDKey, metadata := range dataplaneMetadata {
			replacementManagedIdentityDetails[resourceIDKey].MetadataFromManagedIdentitiesDataplaneService = metadata
		}
	}

	// We resolve the metadata from the ARM User Assigned Identities API for control-plane and data-plane
	// operator identities. SMI-only identities are skipped because end-users are not asked to grant the
	// SMI read on itself. If the same resource ID is also a control-plane or data-plane operator, ARM
	// still applies.
	armMetadata, armErrs := c.resolveManagedIdentitiesMetadataFromARMUserAssignedIdentitiesAPI(ctx, existingCluster, desiredIdentities)
	errs = append(errs, armErrs...)
	// Per-identity ARM Get failures still return an IdentityMetadataValue with
	// RetrievalError set, so they overwrite the empty placeholder. A nil map
	// (no ARM identities, or the client cannot be built) leaves those
	// placeholders empty.
	for resourceIDKey, metadata := range armMetadata {
		replacementManagedIdentityDetails[resourceIDKey].MetadataFromARMUserAssignedIdentitiesAPI = metadata
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.ManagedIdentityDetails = replacementManagedIdentityDetails
	// This controller's entry in Spec.EarliestRecheckTimesByController is
	// intentionally cleared up-front and only set to a future value in the
	// len(errs) == 0 success branch below. On any accumulated source error it
	// stays absent so needsWork keeps returning true and the workqueue retry
	// re-queries instead of being gated by a stale (possibly future) recheck
	// time persisted alongside a partial update. delete on a nil map is a no-op.
	delete(replacement.Spec.EarliestRecheckTimesByController, FetchManagedIdentitiesInfoControllerName)

	if len(errs) == 0 {
		recheckAt := metav1.NewTime(c.clock.Now().Add(wait.Jitter(
			managedIdentitiesRecheckInterval,
			managedIdentitiesRecheckJitter,
		)))
		if replacement.Spec.EarliestRecheckTimesByController == nil {
			replacement.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{}
		}
		replacement.Spec.EarliestRecheckTimesByController[FetchManagedIdentitiesInfoControllerName] = &recheckAt
	}

	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		_, err = c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return errors.Join(errs...)
		}
		if err != nil {
			return errors.Join(append(errs, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)))...)
		}
	}

	return errors.Join(errs...)
}

// desiredIdentityResourceIDsMatchServiceProviderCluster reports whether the
// identity resource IDs stored on ManagedIdentityDetails match desiredIdentities.
// Comparison is by lowercased resource ID presence. Source metadata is ignored.
func (c *fetchManagedIdentitiesInfoSyncer) desiredIdentityResourceIDsMatchServiceProviderCluster(desiredIdentities map[string]*identityToResolve, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	stored := serviceProviderCluster.Status.ManagedIdentityDetails
	if len(desiredIdentities) != len(stored) {
		return false
	}

	for resourceIDKey := range desiredIdentities {
		if _, ok := stored[resourceIDKey]; !ok {
			return false
		}
	}

	return true
}

// collectIdentitiesToResolve returns the unique managed identities from the
// cluster's control plane operators, data plane operators, and Service Managed
// Identity, keyed by lowercased resource ID.
// Malformed entries are skipped and returned as accumulated errors. Callers must not persist the collected set
// when any error is returned: that set is incomplete and replacing
// ManagedIdentityDetails with it would prune identities that failed to collect.
// The returned map is keyed by lowercased resource ID. A failure at this level
// means that some error that should not occur happened. In that case we return early.
func (c *fetchManagedIdentitiesInfoSyncer) collectIdentitiesToResolve(cluster *coreapi.HCPOpenShiftCluster) (map[string]*identityToResolve, []error) {
	identitiesToResolve := map[string]*identityToResolve{}
	var errs []error

	userAssignedIdentities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities

	for controlPlaneOperatorName, controlPlaneOperatorIdentityResourceID := range userAssignedIdentities.ControlPlaneOperators {
		if len(controlPlaneOperatorName) == 0 {
			errs = append(errs, utils.TrackError(fmt.Errorf("unexpected empty operator name for control plane operator")))
			continue
		}
		if controlPlaneOperatorIdentityResourceID == nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("unexpected nil identity Resource ID for control plane operator %q", controlPlaneOperatorName)))
			continue
		}

		resourceIDKey := strings.ToLower(controlPlaneOperatorIdentityResourceID.String())
		existing, ok := identitiesToResolve[resourceIDKey]
		if !ok {
			existing = &identityToResolve{
				resourceID: coreapi.DeepCopyResourceID(controlPlaneOperatorIdentityResourceID),
			}
			identitiesToResolve[resourceIDKey] = existing
		}
		existing.isControlPlaneOperatorIdentity = true
	}

	for dataPlaneOperatorName, dataPlaneOperatorIdentityResourceID := range userAssignedIdentities.DataPlaneOperators {
		if len(dataPlaneOperatorName) == 0 {
			errs = append(errs, utils.TrackError(fmt.Errorf("unexpected empty operator name for data plane operator")))
			continue
		}
		if dataPlaneOperatorIdentityResourceID == nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("unexpected nil identity Resource ID for data plane operator %q", dataPlaneOperatorName)))
			continue
		}

		resourceIDKey := strings.ToLower(dataPlaneOperatorIdentityResourceID.String())
		existing, ok := identitiesToResolve[resourceIDKey]
		if !ok {
			existing = &identityToResolve{
				resourceID: coreapi.DeepCopyResourceID(dataPlaneOperatorIdentityResourceID),
			}
			identitiesToResolve[resourceIDKey] = existing
		}
		existing.isDataPlaneOperatorIdentity = true
	}

	serviceManagedIdentity := userAssignedIdentities.ServiceManagedIdentity
	if serviceManagedIdentity == nil {
		errs = append(errs, utils.TrackError(fmt.Errorf("unexpected nil identity Resource ID for service managed identity")))
	} else {
		resourceIDKey := strings.ToLower(serviceManagedIdentity.String())
		existing, ok := identitiesToResolve[resourceIDKey]
		if !ok {
			existing = &identityToResolve{
				resourceID: coreapi.DeepCopyResourceID(serviceManagedIdentity),
			}
			identitiesToResolve[resourceIDKey] = existing
		}
		existing.isServiceManagedIdentity = true
	}

	return identitiesToResolve, errs
}

// resolveManagedIdentitiesMetadataFromHardcodedIdentity returns ClientID,
// PrincipalID, and TenantID from the controller's hardcoded identity for every
// desired identity registered with the Managed Identities Data Plane service (control
// plane operators and the service managed identity). Identities that do not
// apply are omitted. The returned map is keyed by lowercased resource ID.
func (c *fetchManagedIdentitiesInfoSyncer) resolveManagedIdentitiesMetadataFromHardcodedIdentity(desiredIdentities map[string]*identityToResolve) map[string]*coreapi.IdentityMetadataValue {
	metadata := make(map[string]*coreapi.IdentityMetadataValue)
	for resourceIDKey, identity := range desiredIdentities {
		if !identity.registeredWithManagedIdentitiesDataplane() {
			continue
		}
		metadata[resourceIDKey] = c.identityMetadataValueFromHardcodedIdentity()
	}
	return metadata
}

// resolveManagedIdentitiesMetadataFromManagedIdentitiesDataplaneService
// queries the real Managed Identities Data Plane service for ClientID, PrincipalID, and
// TenantID of every desired identity registered with that service (control
// plane operators and the service managed identity). Identities that do not
// apply are omitted. The returned map is keyed by lowercased resource ID.
//
// A missing identity in the dataplane response, a malformed credential, or a
// client or request failure is accumulated in the error slice. Successfully
// resolved identities are still returned so SyncOnce can persist them.
// Returns a nil map when there are no dataplane-registered identities or when
// the query cannot be issued.
func (c *fetchManagedIdentitiesInfoSyncer) resolveManagedIdentitiesMetadataFromManagedIdentitiesDataplaneService(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	desiredIdentities map[string]*identityToResolve,
) (map[string]*coreapi.IdentityMetadataValue, []error) {
	var identityResourceIDStrs []string
	// We build the list of identity IDs to query the Managed Identities Data Plane service for.
	// We only resolved the metadata for the identities that are registered with the Managed Identities Data Plane service (MSI-based identities).
	for _, identity := range desiredIdentities {
		if identity.registeredWithManagedIdentitiesDataplane() {
			identityResourceIDStrs = append(identityResourceIDStrs, identity.resourceID.String())
		}
	}
	if len(identityResourceIDStrs) == 0 {
		return nil, nil
	}

	if len(cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL) == 0 {
		return nil, []error{utils.TrackError(fmt.Errorf("cluster ManagedIdentitiesDataPlaneIdentityURL is empty. Cannot query Managed Identities Data Plane service"))}
	}

	fpaMIDataplaneClient, err := c.fpaMIdataplaneClientBuilder.ManagedIdentitiesDataplane(cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL)
	if err != nil {
		return nil, []error{utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Client: %w", err))}
	}

	fpaMIDataplaneCredentials, err := fpaMIDataplaneClient.GetUserAssignedIdentitiesCredentials(ctx, dataplane.UserAssignedIdentitiesRequest{IdentityIDs: identityResourceIDStrs})
	if err != nil {
		return nil, []error{utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Credentials: %w", err))}
	}

	if len(fpaMIDataplaneCredentials.ExplicitIdentities) != len(identityResourceIDStrs) {
		return nil, []error{utils.TrackError(fmt.Errorf("unexpected number of returned Managed Identities Data Plane Credentials. Expected: %d, Received: %d", len(identityResourceIDStrs), len(fpaMIDataplaneCredentials.ExplicitIdentities)))}
	}

	// Index returned credentials by lowercased Resource ID so later lookups are
	// case-insensitive. ARM resource IDs are case-insensitive and the MI dataplane may return a different casing than Cosmos, as well as a
	// different order than how it's been requested.
	returnedCredentialsByLowerResourceID := make(map[string]dataplane.UserAssignedIdentityCredentials, len(fpaMIDataplaneCredentials.ExplicitIdentities))
	var errs []error
	for idx, fpaMIDataplaneCredential := range fpaMIDataplaneCredentials.ExplicitIdentities {
		if fpaMIDataplaneCredential.ResourceID == nil || len(*fpaMIDataplaneCredential.ResourceID) == 0 {
			// The MIDataplane service should not return a nil or empty Resource ID. This is the case even when the identity does not exist in Azure.
			// If this occurs, we accumulate an error and continue.
			errs = append(errs, utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential Resource ID is nil or empty in MI Dataplane service response at index %d (Resource ID %q, Client ID %q, Principal ID %q)",
				idx,
				ptr.Deref(fpaMIDataplaneCredential.ResourceID, ""),
				ptr.Deref(fpaMIDataplaneCredential.ClientID, ""),
				ptr.Deref(fpaMIDataplaneCredential.ObjectID, ""),
			)))
			continue
		}
		returnedCredentialsByLowerResourceID[strings.ToLower(*fpaMIDataplaneCredential.ResourceID)] = fpaMIDataplaneCredential
	}

	metadata := make(map[string]*coreapi.IdentityMetadataValue)
	for _, identityResourceIDStr := range identityResourceIDStrs {
		resourceIDKey := strings.ToLower(identityResourceIDStr)
		credential, ok := returnedCredentialsByLowerResourceID[resourceIDKey]
		if !ok {
			// The MIDataplane service should return a Resource ID that matches one of the identities requested. That is even
			// if the identity actually does not exist anymore in Azure. If it does not, we accumulate an error and continue.
			errs = append(errs, utils.TrackError(fmt.Errorf("unexpected requested resource id credentials not found in managed identities data plane response. Requested resource ID %q", resourceIDKey)))
			continue
		}
		metadata[resourceIDKey] = c.identityMetadataValueFromManagedIdentitiesDataplaneCredential(&credential)
	}

	return metadata, errs
}

// resolveManagedIdentitiesMetadataFromARMUserAssignedIdentitiesAPI gets
// ClientID, PrincipalID, and TenantID from the ARM User Assigned Identities
// API for every desired identity that applies to ARMUserAssignedIdentitiesAPI
// (control-plane and data-plane operator identities). SMI-only identities
// are omitted. The returned map is keyed by lowercased resource ID.
//
// ARM Get not-found keeps the source entry with nil ClientID/PrincipalID/TenantID
// and records the error in RetrievalError. That is an expected, potentially
// transient state rather than a sync failure, so it is not accumulated.
// Other Get failures and a nil Properties also persist RetrievalError and
// are accumulated. Successfully resolved identities have RetrievalError nil.
// Returns a nil map when there are no ARM identities or when the client cannot
// be built.
func (c *fetchManagedIdentitiesInfoSyncer) resolveManagedIdentitiesMetadataFromARMUserAssignedIdentitiesAPI(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	desiredIdentities map[string]*identityToResolve,
) (map[string]*coreapi.IdentityMetadataValue, []error) {

	// We build the list of identities to query the ARM User Assigned Identities API for.
	// Control-plane and data-plane operator identities are queried. SMI-only identities
	// are skipped because end-users are not asked to grant the SMI read on itself.
	var armIdentities []*identityToResolve
	for _, identity := range desiredIdentities {
		if identity.appliesToARMUserAssignedIdentitiesAPI() {
			armIdentities = append(armIdentities, identity)
		}
	}
	if len(armIdentities) == 0 {
		return nil, nil
	}

	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	if smiResourceID == nil {
		return nil, []error{utils.TrackError(fmt.Errorf("cluster ServiceManagedIdentity is nil. Cannot resolve ARM User Assigned Identities metadata"))}
	}

	// If the Service Managed Identity does not exist, this call fails and the controller will accumulate the error.
	userAssignedIdentitiesClient, err := c.smiClientBuilder.UserAssignedIdentitiesClient(ctx, cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL, smiResourceID, cluster.ID.SubscriptionID)
	if err != nil {
		return nil, []error{utils.TrackError(fmt.Errorf("failed to get User Assigned Identities Client: %w", err))}
	}

	metadata := make(map[string]*coreapi.IdentityMetadataValue)
	var errs []error
	for _, identity := range armIdentities {
		resourceIDKey := strings.ToLower(identity.resourceID.String())
		// If the Service Managed Identity does not have permissions to do a GET over the identity resource ID it is querying, this call
		// fails and the controller accumulates the error.
		currentMI, err := userAssignedIdentitiesClient.Get(ctx, identity.resourceID.ResourceGroupName, identity.resourceID.Name, nil)
		if azureclient.IsResourceNotFoundErr(err) {
			// The identity is not found in Azure. Keep the source entry so the
			// ServiceProviderCluster still lists that ARM was queried, but leave
			// ClientID/PrincipalID/TenantID nil and record why in RetrievalError.
			// This is an expected, potentially transient state rather than a sync
			// failure, so it is not accumulated into errs.
			metadata[resourceIDKey] = c.identityMetadataValueFromRetrievalError(err)
			continue
		}
		if err != nil {
			// On any other Get failure, ClientID/PrincipalID/TenantID stay nil
			// because previously resolved values are no longer trustworthy, and
			// RetrievalError records the truncated Azure error. Accumulate the
			// failure and keep going so successfully resolved identities can still
			// be persisted; the accumulated error is returned so the workqueue
			// retry re-queries Azure.
			metadata[resourceIDKey] = c.identityMetadataValueFromRetrievalError(err)
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to get User Assigned Managed Identity %s: %w", resourceIDKey, err)))
			continue
		}
		if currentMI.Properties == nil {
			propsErr := fmt.Errorf("unexpected User Assigned Managed Identity %s Properties is nil", resourceIDKey)
			metadata[resourceIDKey] = c.identityMetadataValueFromRetrievalError(propsErr)
			errs = append(errs, utils.TrackError(propsErr))
			continue
		}
		metadata[resourceIDKey] = c.identityMetadataValueFromARMIdentity(&currentMI.Identity)
	}

	return metadata, errs
}

// identityMetadataValueFromHardcodedIdentity maps the controller's hardcoded
// identity to an IdentityMetadataValue.
func (c *fetchManagedIdentitiesInfoSyncer) identityMetadataValueFromHardcodedIdentity() *coreapi.IdentityMetadataValue {
	return &coreapi.IdentityMetadataValue{
		ClientID:    ptr.To(c.hardcodedIdentity.ClientID),
		PrincipalID: ptr.To(c.hardcodedIdentity.PrincipalID),
		TenantID:    ptr.To(c.hardcodedIdentity.TenantID),
	}
}

// identityMetadataValueFromManagedIdentitiesDataplaneCredential maps a Managed Identities Data
// Plane credential to an IdentityMetadataValue. PrincipalID is taken from
// ObjectID.
func (c *fetchManagedIdentitiesInfoSyncer) identityMetadataValueFromManagedIdentitiesDataplaneCredential(credential *dataplane.UserAssignedIdentityCredentials) *coreapi.IdentityMetadataValue {
	return &coreapi.IdentityMetadataValue{
		ClientID:    credential.ClientID,
		PrincipalID: credential.ObjectID,
		TenantID:    credential.TenantID,
	}
}

// identityMetadataValueFromARMIdentity maps an ARM User Assigned Identity to an
// IdentityMetadataValue. Callers must pass an identity whose Properties is
// non-nil. RetrievalError is left nil because the retrieval succeeded.
func (c *fetchManagedIdentitiesInfoSyncer) identityMetadataValueFromARMIdentity(identity *armmsi.Identity) *coreapi.IdentityMetadataValue {
	return &coreapi.IdentityMetadataValue{
		ClientID:    identity.Properties.ClientID,
		PrincipalID: identity.Properties.PrincipalID,
		TenantID:    identity.Properties.TenantID,
	}
}

// identityMetadataValueFromRetrievalError returns an IdentityMetadataValue
// whose ClientID, PrincipalID, and TenantID are nil and whose RetrievalError
// is the truncated error message.
func (c *fetchManagedIdentitiesInfoSyncer) identityMetadataValueFromRetrievalError(err error) *coreapi.IdentityMetadataValue {
	return &coreapi.IdentityMetadataValue{
		RetrievalError: truncateRetrievalError(err.Error()),
	}
}
