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

package hostedcluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/utils/lru"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	internalctrlutils "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/cpoutofband"
	cpoutofbandhelpers "github.com/Azure/ARO-HCP/internal/cpoutofband/helpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const HostedClusterReleaseImagesManagementControllerName = "HostedClusterReleaseImagesManagement"

const hostedClusterReleaseImagesManagementCincinnatiFetchTimeout = 30 * time.Second

// cincinnatiUpdatesCacheKey is the LRU key for GetUpdates results.
// for the hostedClusterReleaseImagesManagementSyncer's cincinnatiUpdatesCache.
// This type should only be used in the hostedClusterReleaseImagesManagementSyncer's cincinnatiUpdatesCache.
type cincinnatiUpdatesCacheKey struct {
	// Channel is the Cincinnati channel name (lowercased).
	Channel string
	// Version is the Cincinnati version being queried.
	Version string
}

type hostedClusterReleaseImagesManagementSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	rolloutLister                fleetlisters.ControlPlaneVersionRolloutLister
	readDesireLister             kubeapplierlisters.ReadDesireLister
	cincinnatiClientCache        cincinnati.ClientCache
	cincinnatiUpdatesCache       *lru.Cache
	ocpImagesACRHostname         string
}

var _ controllerutils.ClusterSyncer = (*hostedClusterReleaseImagesManagementSyncer)(nil)

func NewHostedClusterReleaseImagesManagementController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
	fleetInformers fleetinformers.FleetInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	ocpImagesACRHostname string,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	_, readDesireLister := kubeApplierInformers.ReadDesires()

	if len(ocpImagesACRHostname) == 0 {
		panic("ocpImagesACRHostname is required")
	}

	syncer := &hostedClusterReleaseImagesManagementSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		rolloutLister:                rolloutLister,
		readDesireLister:             readDesireLister,
		cincinnatiClientCache:        cincinnati.NewClientCache(),
		cincinnatiUpdatesCache:       lru.New(100000),
		ocpImagesACRHostname:         ocpImagesACRHostname,
	}

	controller := controllerutils.NewClusterWatchingController(
		HostedClusterReleaseImagesManagementControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		30*time.Second,
		syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	controller.AddInterestingResource(kubeapplierapi.ClusterScopedReadDesireResourceType, kubeapplierapi.ReadDesireNameReadonlyHostedCluster)

	return controller
}

func (s *hostedClusterReleaseImagesManagementSyncer) needsWork(cluster *coreapi.Cluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil
}

func (s *hostedClusterReleaseImagesManagementSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	if !s.needsWork(cluster) {
		return nil
	}

	spc, err := s.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	desiredVersion := spc.Spec.ControlPlaneVersion.DesiredVersion
	if desiredVersion == nil {
		return nil
	}

	channelGroup := cluster.CustomerProperties.Version.ChannelGroup
	if len(channelGroup) == 0 {
		return utils.TrackError(fmt.Errorf("cluster %s has no channel group", key.HCPClusterName))
	}

	cincinnatiURI, err := cincinnati.GetCincinnatiURI(channelGroup)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cincinnati URI: %w", err))
	}

	desiredVersionIsOutOfBand := cpoutofband.IsOutOfBandControlPlaneVersion(desiredVersion.String())

	// When the desired version is out of band we set releaseImage to the channel's best exact version  the data plane stays at the channel's best exact
	// version (the version the cluster would run without the pin). This is because we can't send to Cluster Service the out-of-band version, as it's
	// not recognized by it as it's not being published to the Cincinnati graph, which would make CS fail. We workaround this by thn setting the
	// control plane release image to the out of band version but keeping the release image to the channel's best exact version.
	// A nightly version that is out of band is not supported and the controller will error if that occurs.
	var cincinnatiVersion = desiredVersion
	if desiredVersionIsOutOfBand {
		substitute, err := cpoutofbandhelpers.RecognizedVersionForOutOfBandPin(ctx, s.rolloutLister, cluster, *desiredVersion)
		if err != nil {
			return utils.TrackError(err)
		}
		cincinnatiVersion = substitute
	}

	cincinnatiChannel := fmt.Sprintf("%s-%d.%d", channelGroup, cincinnatiVersion.Major, cincinnatiVersion.Minor)
	clusterUUID, _, err := kubeapplierhelpers.GetCachedHostedClusterUUIDForCluster(ctx, s.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	// UUID is sent as id= query param for analytics/conditional-risk
	// evaluation only; with the Always registry it does not affect version
	// payload resolution, so uuid.Nil is fine when no HostedCluster exists yet.

	var cincinnatiResult configv1.Release
	cacheKey := cincinnatiUpdatesCacheKey{
		Channel: strings.ToLower(cincinnatiChannel),
		Version: cincinnatiVersion.String(),
	}
	if cached, ok := s.cincinnatiUpdatesCache.Get(cacheKey); ok {
		cincinnatiResult = cached.(configv1.Release)
	} else {
		cincinnatiCtx, cincinnatiCtxCancel := context.WithTimeout(ctx, hostedClusterReleaseImagesManagementCincinnatiFetchTimeout)
		defer cincinnatiCtxCancel()
		cincinnatiClient := s.cincinnatiClientCache.GetOrCreateClient(clusterUUID)
		cincinnatiResult, _, _, err = cincinnatiClient.GetUpdates(cincinnatiCtx, cincinnatiURI, "multi", "multi", cincinnatiChannel, *cincinnatiVersion)
		if err != nil {
			if cincinnati.IsCincinnatiVersionNotFoundError(err) {
				return utils.TrackError(fmt.Errorf("version %s not found in Cincinnati channel %s: %w", cincinnatiVersion.String(), cincinnatiChannel, err))
			}
			return utils.TrackError(fmt.Errorf("failed to query Cincinnati for version %s in channel %s: %w", cincinnatiVersion.String(), cincinnatiChannel, err))
		}
		s.cincinnatiUpdatesCache.Add(cacheKey, cincinnatiResult)
	}

	releaseImage := cincinnatiResult.Image
	if len(releaseImage) == 0 {
		return utils.TrackError(fmt.Errorf("cincinnati returned empty release image for version %s in channel %s", cincinnatiVersion.String(), cincinnatiChannel))
	}

	replacement := spc.DeepCopy()
	if replacement.Spec.DesiredHostedCluster == nil {
		replacement.Spec.DesiredHostedCluster = &v1beta1.HostedCluster{}
	}
	desiredHostedCluster := replacement.Spec.DesiredHostedCluster

	// Workaround for OCPBUGS-83564: For nightly versions, override the release image
	// to use the ACR mirror since nightly images are not publicly accessible from
	// quay.io or registry.ci.openshift.org. Cincinnati returns images like
	// registry.ci.openshift.org/ocp-multi/4.19-art-latest-multi@sha256:... which
	// must be remapped to the ACR mirror's nightly repository.
	if channelGroup == metadataapi.ChannelGroupNightly {
		var err error
		releaseImage, err = s.mirrorReleaseImageForNightly(releaseImage)
		if err != nil {
			return utils.TrackError(err)
		}
	}
	desiredHostedCluster.Spec.Release.Image = releaseImage

	annotations := desiredHostedCluster.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	if desiredVersionIsOutOfBand {
		controlPlaneReleaseImage, ok := cpoutofband.OutOfBandControlPlaneReleaseImage(desiredVersion.String())
		if !ok {
			return utils.TrackError(fmt.Errorf("unexpected out-of-band control plane release image for version %s not found", desiredVersion.String()))
		}
		if desiredHostedCluster.Spec.ControlPlaneRelease == nil {
			desiredHostedCluster.Spec.ControlPlaneRelease = &v1beta1.Release{}
		}

		desiredHostedCluster.Spec.ControlPlaneRelease.Image = controlPlaneReleaseImage

		annotations[v1beta1.ForceUpgradeToAnnotation] = controlPlaneReleaseImage
		desiredHostedCluster.SetAnnotations(annotations)
	} else {
		desiredHostedCluster.Spec.ControlPlaneRelease = nil
		annotations[v1beta1.ForceUpgradeToAnnotation] = releaseImage
		desiredHostedCluster.SetAnnotations(annotations)
	}

	if !internalctrlutils.NeedsUpdate(spc, replacement) {
		return nil
	}

	_, err = s.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update DesiredHostedCluster: %w", err))
	}

	return nil
}

// mirrorReleaseImageForNightly converts a Cincinnati-provided release image into
// the ACR-mirrored nightly image. Cincinnati returns images from
// registry.ci.openshift.org (e.g. registry.ci.openshift.org/<path>@sha256:<digest>)
// which are not directly pullable. They are mirrored in the ACR OCP Images repository at
// <ocpImagesACRHostname>/openshift-release-dev/ocp-release-nightly@sha256:<digest>.
func (s *hostedClusterReleaseImagesManagementSyncer) mirrorReleaseImageForNightly(releaseImage string) (string, error) {
	const prefix = "registry.ci.openshift.org/"
	if !strings.HasPrefix(releaseImage, prefix) {
		return "", utils.TrackError(fmt.Errorf("unexpected nightly release image format: expected %q prefix, got %q", prefix, releaseImage))
	}
	atIdx := strings.LastIndex(releaseImage, "@")
	if atIdx == -1 {
		return "", utils.TrackError(fmt.Errorf("unexpected nightly release image format: missing @sha256 digest in %q", releaseImage))
	}
	digest := releaseImage[atIdx+1:]
	if !strings.HasPrefix(digest, "sha256:") {
		return "", utils.TrackError(fmt.Errorf("unexpected nightly release image format: expected @sha256:<digest>, got %q", releaseImage))
	}
	if len(digest) <= len("sha256:") {
		return "", utils.TrackError(fmt.Errorf("unexpected nightly release image format: empty sha256 digest in %q", releaseImage))
	}
	return fmt.Sprintf("%s/openshift-release-dev/ocp-release-nightly@%s", s.ocpImagesACRHostname, digest), nil
}
