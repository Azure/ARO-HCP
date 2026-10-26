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

package stamp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// StuckClusterCleanupCreateRequest is the POST body.
//
// The namespace must be named exactly; a prefix is never accepted. The optional
// identifiers are what let the controller's preflight perform its ARM Cosmos and
// Clusters Service point reads: without at least one of them those checks cannot
// run, the inspection still reports its namespace findings, and the request is
// reported as not fully confirmable.
type StuckClusterCleanupCreateRequest struct {
	NamespaceName     string `json:"namespaceName"`
	RequestReason     string `json:"requestReason"`
	Mode              string `json:"mode,omitempty"`
	ClusterServiceID  string `json:"clusterServiceID,omitempty"`
	ClusterResourceID string `json:"clusterResourceID,omitempty"`
}

// StuckClusterCleanupCreateResponse names the created (or already existing)
// cleanup so the caller can poll it with the GET route.
type StuckClusterCleanupCreateResponse struct {
	Name          string `json:"name"`
	NamespaceName string `json:"namespaceName"`
	Mode          string `json:"mode"`
	Created       bool   `json:"created"`
}

// StuckClusterCleanupGetResponse reports progress and findings.
type StuckClusterCleanupGetResponse struct {
	Name              string                                       `json:"name"`
	NamespaceName     string                                       `json:"namespaceName"`
	NamespaceUID      string                                       `json:"namespaceUID,omitempty"`
	Mode              string                                       `json:"mode"`
	Phase             string                                       `json:"phase,omitempty"`
	Message           string                                       `json:"message,omitempty"`
	Conditions        []metav1.Condition                           `json:"conditions,omitempty"`
	Preflight         *fleetapi.StuckClusterCleanupPreflightResult `json:"preflight,omitempty"`
	ObservedNamespace *fleetapi.ObservedTerminatingNamespace       `json:"observedNamespace,omitempty"`
}

// StuckClusterCleanupCreateHandler handles
// POST /admin/v1/stamps/{stampIdentifier}/managementclusters/{managementClusterName}/stuckclustercleanups.
//
// The handler only writes desired state: it never contacts a management cluster
// and never holds the request open while work happens. The backend
// StuckClusterCleanupPreflight controller reconciles what is written here.
type StuckClusterCleanupCreateHandler struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient
}

func NewStuckClusterCleanupCreateHandler(fleetDBClient fleetcosmosstorage.FleetDBClient) *StuckClusterCleanupCreateHandler {
	return &StuckClusterCleanupCreateHandler{fleetDBClient: fleetDBClient}
}

func (h *StuckClusterCleanupCreateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	stampIdentifier := r.PathValue("stampIdentifier")
	managementClusterName := r.PathValue("managementClusterName")

	if err := validateStampIdentifier(stampIdentifier); err != nil {
		return err
	}
	if err := validateManagementClusterName(managementClusterName); err != nil {
		return err
	}

	var body StuckClusterCleanupCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return coreapi.NewCloudError(
			http.StatusBadRequest,
			coreapi.CloudErrorCodeInvalidRequestContent, "",
			"The request content was invalid and could not be deserialized: %q", err,
		)
	}

	mode := fleetapi.StuckClusterCleanupMode(body.Mode)
	if len(body.Mode) == 0 {
		// Inspect is the safe default: an operator who does not say otherwise
		// gets a dry run, never a mutation.
		mode = fleetapi.StuckClusterCleanupModeInspect
	}

	var clusterResourceID *azcorearm.ResourceID
	if len(body.ClusterResourceID) > 0 {
		parsed, err := azcorearm.ParseResourceID(body.ClusterResourceID)
		if err != nil {
			return coreapi.NewCloudError(
				http.StatusBadRequest,
				coreapi.CloudErrorCodeInvalidRequestContent, "clusterResourceID",
				"Invalid cluster resource ID %q: %v", body.ClusterResourceID, err,
			)
		}
		clusterResourceID = parsed
	}

	managementClusters := h.fleetDBClient.Stamps().ManagementClusters(stampIdentifier)
	if _, err := managementClusters.Get(ctx, managementClusterName); err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "",
				"Management cluster %q not found in stamp %q", managementClusterName, stampIdentifier)
		}
		return utils.TrackError(err)
	}

	name := StuckClusterCleanupNameFor(body.NamespaceName)
	cleanupCRUD := managementClusters.StuckClusterCleanups()

	// Deduplicate on management cluster plus namespace name, which is all the
	// handler can do synchronously: the namespace UID is only knowable once the
	// controller has read the namespace, so UID-level deduplication belongs to
	// the controller and not here.
	if existing, err := cleanupCRUD.Get(ctx, name); err == nil {
		return writeJSON(w, http.StatusOK, StuckClusterCleanupCreateResponse{
			Name:          name,
			NamespaceName: existing.Spec.NamespaceName,
			Mode:          string(existing.Spec.Mode),
			Created:       false,
		})
	} else if !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(err)
	}

	resourceID, err := fleetapihelpers.ToStuckClusterCleanupResourceID(stampIdentifier, name)
	if err != nil {
		return utils.TrackError(err)
	}

	cleanup := &fleetapi.StuckClusterCleanup{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(stampIdentifier),
		},
		Spec: fleetapi.StuckClusterCleanupSpec{
			Mode:              mode,
			NamespaceName:     body.NamespaceName,
			ClusterServiceID:  body.ClusterServiceID,
			ClusterResourceID: clusterResourceID,
			RequestReason:     body.RequestReason,
		},
		Status: fleetapi.StuckClusterCleanupStatus{
			Phase: fleetapi.StuckClusterCleanupPhasePending,
		},
	}

	created, err := cleanupCRUD.Create(ctx, cleanup, nil)
	if err != nil {
		if cosmosstorageutils.IsConflictError(err) {
			// Lost a create race; the winner's document is the answer.
			return writeJSON(w, http.StatusOK, StuckClusterCleanupCreateResponse{
				Name:          name,
				NamespaceName: body.NamespaceName,
				Mode:          string(mode),
				Created:       false,
			})
		}
		if validationErr := asCloudValidationError(err); validationErr != nil {
			return validationErr
		}
		return utils.TrackError(err)
	}

	return writeJSON(w, http.StatusCreated, StuckClusterCleanupCreateResponse{
		Name:          name,
		NamespaceName: created.Spec.NamespaceName,
		Mode:          string(created.Spec.Mode),
		Created:       true,
	})
}

// StuckClusterCleanupGetHandler handles
// GET /admin/v1/stamps/{stampIdentifier}/managementclusters/{managementClusterName}/stuckclustercleanups/{name}.
type StuckClusterCleanupGetHandler struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient
}

func NewStuckClusterCleanupGetHandler(fleetDBClient fleetcosmosstorage.FleetDBClient) *StuckClusterCleanupGetHandler {
	return &StuckClusterCleanupGetHandler{fleetDBClient: fleetDBClient}
}

func (h *StuckClusterCleanupGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	stampIdentifier := r.PathValue("stampIdentifier")
	managementClusterName := r.PathValue("managementClusterName")
	name := r.PathValue("name")

	if err := validateStampIdentifier(stampIdentifier); err != nil {
		return err
	}
	if err := validateManagementClusterName(managementClusterName); err != nil {
		return err
	}

	cleanup, err := h.fleetDBClient.Stamps().ManagementClusters(stampIdentifier).StuckClusterCleanups().Get(ctx, name)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return coreapi.NewCloudError(http.StatusNotFound, coreapi.CloudErrorCodeNotFound, "",
				"Stuck cluster cleanup %q not found on management cluster %q in stamp %q", name, managementClusterName, stampIdentifier)
		}
		return utils.TrackError(err)
	}

	return writeJSON(w, http.StatusOK, StuckClusterCleanupGetResponse{
		Name:              name,
		NamespaceName:     cleanup.Spec.NamespaceName,
		NamespaceUID:      cleanup.Spec.NamespaceUID,
		Mode:              string(cleanup.Spec.Mode),
		Phase:             string(cleanup.Status.Phase),
		Message:           cleanup.Status.Message,
		Conditions:        cleanup.Status.Conditions,
		Preflight:         cleanup.Status.Preflight,
		ObservedNamespace: cleanup.Status.ObservedNamespace,
	})
}

// StuckClusterCleanupNameFor derives the cleanup's resource name from the target
// namespace name.
//
// It is a hash rather than the namespace name itself because the name has to be
// a single ARM path segment and a stable function of the namespace, so that a
// repeated request for the same namespace addresses the same document and
// deduplicates instead of piling up. The namespace name travels in the spec,
// where it stays readable.
func StuckClusterCleanupNameFor(namespaceName string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(namespaceName)))
	return "ns-" + hex.EncodeToString(sum[:8])
}

// validateManagementClusterName rejects anything but the singleton name. The
// management cluster is one per stamp — ManagementClusterResourceName is the
// constant "default" and the Cosmos scope is derived from the stamp alone — so
// the path segment exists for consistency with the sibling stamp routes and
// would otherwise be silently ignored.
func validateManagementClusterName(managementClusterName string) error {
	if !strings.EqualFold(managementClusterName, fleetapi.ManagementClusterResourceName) {
		return coreapi.NewCloudError(
			http.StatusBadRequest,
			coreapi.CloudErrorCodeInvalidRequestContent, "managementClusterName",
			"Invalid management cluster name %q: the management cluster is a singleton and must be %q",
			managementClusterName, fleetapi.ManagementClusterResourceName,
		)
	}
	return nil
}

// asCloudValidationError turns the CRUD layer's validation failure into a 400
// so an operator sees which field was wrong instead of an opaque 500. The
// validating CRUD wraps the field.ErrorList aggregate with a fixed prefix,
// which is the only marker available to distinguish it from a storage error.
func asCloudValidationError(err error) error {
	if err == nil || !strings.Contains(err.Error(), "create validation failed") {
		return nil
	}
	return coreapi.NewCloudError(
		http.StatusBadRequest,
		coreapi.CloudErrorCodeInvalidRequestContent, "",
		"The cleanup request was rejected: %v", err,
	)
}

func writeJSON(w http.ResponseWriter, statusCode int, body any) error {
	_, err := coreapihelpers.WriteJSONResponse(w, statusCode, body)
	return utils.TrackError(err)
}
