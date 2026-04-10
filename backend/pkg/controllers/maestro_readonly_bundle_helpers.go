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

package controllers

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// maestroReadonlyBundleHelper encapsulates common operations for building and
// managing Maestro readonly bundles. It is used by both the cluster-scoped and
// node-pool-scoped controllers.
type maestroReadonlyBundleHelper struct {
	maestroSourceEnvironmentIdentifier string
	maestroClientBuilder               maestro.MaestroClientBuilder
	uuidV4Generator                    func() (uuid.UUID, error)
}

// buildInitialReadonlyMaestroBundle builds an initial readonly Maestro Bundle for a given resource specified in obj.
// maestroBundleNamespacedName is the ObjectMeta for the Maestro Bundle, including name, namespace, and labels
// (the caller is responsible for setting the managed-by label on the ObjectMeta).
// objResourceIdentifier is the resource identifier of the resource specified in obj.
func (h *maestroReadonlyBundleHelper) buildInitialReadonlyMaestroBundle(
	maestroBundleNamespacedName metav1.ObjectMeta,
	objResourceIdentifier workv1.ResourceIdentifier,
	obj runtime.Object,
) *workv1.ManifestWork {
	maestroBundle := &workv1.ManifestWork{
		ObjectMeta: maestroBundleNamespacedName,
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: []workv1.Manifest{
					{
						RawExtension: runtime.RawExtension{
							Object: obj,
						},
					},
				},
			},
			ManifestConfigs: []workv1.ManifestConfigOption{
				{
					ResourceIdentifier: objResourceIdentifier,
					UpdateStrategy: &workv1.UpdateStrategy{
						Type: workv1.UpdateStrategyTypeReadOnly,
					},
					FeedbackRules: []workv1.FeedbackRule{
						{
							Type: workv1.JSONPathsType,
							JsonPaths: []workv1.JsonPath{
								{
									Name: "resource",
									Path: "@",
								},
							},
						},
					},
				},
			},
		},
	}
	return maestroBundle
}

// buildInitialMaestroBundleReference builds an initial Maestro Bundle reference for a given maestro bundle internal name.
func (h *maestroReadonlyBundleHelper) buildInitialMaestroBundleReference(internalName api.MaestroBundleInternalName) (*api.MaestroBundleReference, error) {
	maestroAPIMaestroBundleName, err := h.generateNewMaestroAPIMaestroBundleName()
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to generate Maestro API Maestro Bundle name: %w", err))
	}
	return &api.MaestroBundleReference{
		Name:                        internalName,
		MaestroAPIMaestroBundleName: maestroAPIMaestroBundleName,
		MaestroAPIMaestroBundleID:   "",
	}, nil
}

// generateNewMaestroAPIMaestroBundleName generates a new Maestro API Maestro Bundle name.
// The generated name is a UUIDv4.
func (h *maestroReadonlyBundleHelper) generateNewMaestroAPIMaestroBundleName() (string, error) {
	newUUID, err := h.uuidV4Generator()
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to generate UUIDv4 for Maestro API Maestro Bundle name: %w", err))
	}
	return newUUID.String(), nil
}

// getOrCreateMaestroBundle gets (or creates if it does not exist) a Maestro Bundle for a given Maestro Bundle namespaced name.
func (h *maestroReadonlyBundleHelper) getOrCreateMaestroBundle(ctx context.Context, maestroClient maestro.Client, maestroBundle *workv1.ManifestWork) (*workv1.ManifestWork, error) {
	logger := utils.LoggerFromContext(ctx)
	existingMaestroBundle, err := maestroClient.Get(ctx, maestroBundle.Name, metav1.GetOptions{})
	if err == nil {
		logger.Info(fmt.Sprintf("retrieved maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name))
		return existingMaestroBundle, nil
	}
	if !k8serrors.IsNotFound(err) {
		logger.Error(err, "failed to get Maestro Bundle and it is not already exists error")
		return nil, utils.TrackError(fmt.Errorf("failed to get Maestro Bundle: %w", err))
	}

	logger.Info(fmt.Sprintf("attempting to create maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name))
	existingMaestroBundle, err = maestroClient.Create(ctx, maestroBundle, metav1.CreateOptions{})
	if err == nil {
		logger.Info(fmt.Sprintf("created maestro bundle name %s with resource name %s", maestroBundle.Name, maestroBundle.Spec.ManifestConfigs[0].ResourceIdentifier.Name))
		return existingMaestroBundle, nil
	}
	if !k8serrors.IsAlreadyExists(err) {
		logger.Error(err, "failed to create Maestro Bundle and it is not already exists error")
		return nil, utils.TrackError(fmt.Errorf("failed to create Maestro Bundle: %w", err))
	}
	logger.Error(err, "failed to create Maestro Bundle because it returned already exists error. Attempting to get it again")
	existingMaestroBundle, err = maestroClient.Get(ctx, maestroBundle.Name, metav1.GetOptions{})
	return existingMaestroBundle, err
}

// getHostedClusterNamespace gets the namespace for the hosted cluster based on the environment name and the cluster service OCM Cluster ID.
func (h *maestroReadonlyBundleHelper) getHostedClusterNamespace(envName string, csClusterID string) string {
	return fmt.Sprintf("ocm-%s-%s", envName, csClusterID)
}

// createMaestroClientFromProvisionShard creates a Maestro client for the given provision shard.
func (h *maestroReadonlyBundleHelper) createMaestroClientFromProvisionShard(
	ctx context.Context, provisionShard *arohcpv1alpha1.ProvisionShard,
) (maestro.Client, error) {
	provisionShardMaestroConsumerName := provisionShard.MaestroConfig().ConsumerName()
	provisionShardMaestroRESTAPIEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	provisionShardMaestroGRPCAPIEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	maestroSourceID := maestro.GenerateMaestroSourceID(h.maestroSourceEnvironmentIdentifier, provisionShard.ID())

	maestroClient, err := h.maestroClientBuilder.NewClient(ctx, provisionShardMaestroRESTAPIEndpoint, provisionShardMaestroGRPCAPIEndpoint, provisionShardMaestroConsumerName, maestroSourceID)

	return maestroClient, err
}
