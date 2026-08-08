package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func defaultInstall(ctx context.Context, roundTripper RoundTrip, updateService *url.URL, userAgent string, channel string, rankRelease rankRelease) (*semver.Version, error) {
	releases, _, updateService, err := cincinnati(ctx, roundTripper, updateService, userAgent, channel)
	if err != nil {
		return nil, err
	}
	if len(releases) == 0 {
		return nil, fmt.Errorf("no install targets found in %s.", updateService)
	}
	return rankedSelection(releases, rankRelease)
}

func defaultUpdate(ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster, rankRelease rankRelease) (*semver.Version, error) {
	if hostedCluster.Status.Version == nil {
		return nil, errors.New("HostedCluster status.version is not set, so neither the current version nor available update advice are available.")
	}

	updates := slices.Clone(hostedCluster.Status.Version.AvailableUpdates)
	conditionalUpdates := slices.Clone(hostedCluster.Status.Version.ConditionalUpdates)

	// Process Upgradeable=False locally, until https://github.com/openshift/cluster-version-operator/tree/42ff52c75c65ca9351bc391f777709631bec3666/pkg/risk/upgradeable goes GA with the ClusterUpdateAcceptRisks feature-gate, https://github.com/openshift/api/blob/181bcde0d9c778458cf2faec55e5fde023fd3c20/features/features.go#L709-L715
	upgradeable := meta.FindStatusCondition(hostedCluster.Status.Conditions, string(hypershiftv1beta1.ClusterVersionUpgradeable))
	if upgradeable != nil && upgradeable.Status == metav1.ConditionFalse {
		currentTargetVersion, err := semver.Parse(hostedCluster.Status.Version.Desired.Version)
		if err != nil {
			return nil, fmt.Errorf("HostedCluster status.version.desired.version is not SemVer: %w", err)
		}
		for i := len(updates) - 1; i >= 0; i-- {
			nextVersion, err := semver.Parse(updates[i].Version)
			if err == nil && (nextVersion.Major > currentTargetVersion.Major ||
				(nextVersion.Major == currentTargetVersion.Major && nextVersion.Minor > currentTargetVersion.Minor)) {
				updates = append(updates[:i], updates[i+1:]...)
				found := false
				for _, conditionalUpdate := range conditionalUpdates {
					if conditionalUpdate.Release.Version == nextVersion.String() {
						found = true
					}
				}
				if !found {
					conditionalUpdates = append(conditionalUpdates, configv1.ConditionalUpdate{Release: configv1.Release{Version: nextVersion.String()}})
				}
			}
		}
	}

	if len(updates) == 0 {
		if len(conditionalUpdates) == 0 {
			return nil, errors.New("HostedCluster status.version.availableUpdates and conditionalUpdates are both empty, so no updates are currently recommended for this cluster.")
		}
		return nil, fmt.Errorf("HostedCluster status.version.availableUpdates is empty, so no updates are currently recommended for this cluster.  There are %d conditional updates, which are supported, but not recommended for this cluster without administrator approval.", len(conditionalUpdates))
	}

	return rankedSelection(updates, rankRelease)
}
