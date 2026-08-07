package controlplaneversion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// RoundTrip allows customizing the RoundTripper (e.g. to support
// testing or proxy/TLS configuration) without having to create a local
// struct supplying the RoundTripper interface.  The interface only
// requires a RoundTrip function, and it is easier to just pass the
// function around.  https://github.com/golang/go/issues/38479 is
// proposing the type become a stdlib declaration, but until then,
// declare it locally.
type RoundTrip func(*http.Request) (*http.Response, error)

// metadata represents release metadata for a single node in the update graph.
type metadata struct {
	ChannelsString string `json:"io.openshift.upgrades.graph.release.channels,omitempty"`
}

// node represents a single cluster version in the update graph.
type node struct {
	// Version is the semantic version of this release.
	Version string `json:"version"`

	// Payload is the release image pullspec (payload) for this version.
	Payload string `json:"payload"`

	// Metadata contains additional release information such as URL, architecture,
	// and supported update channels.
	Metadata metadata `json:"metadata,omitempty"`
}

// graph represents the update graph structure returned by the Cincinnati service.
// It defines all available cluster versions and the valid upgrade paths between them.
type graph struct {
	Nodes []node `json:"nodes"`
}

var DefaultUpstreamUpdateService = "https://api.openshift.com/api/upgrades_info/graph"

// roundTrip converts a RoundTrip function into a RoundTripper.
type roundTrip struct {
	roundTripper RoundTrip
}

// RoundTrip executes a single HTTP transaction, returning a Response
// for the provided Request.
func (rt *roundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return rt.roundTripper(request)
}

func defaultInstall(ctx context.Context, roundTripper RoundTrip, updateService *url.URL, userAgent string, channel string, rankRelease rankRelease) (*semver.Version, error) {
	if updateService == nil {
		var err error
		updateService, err = url.Parse(DefaultUpstreamUpdateService)
		if err != nil {
			return nil, err
		}
	} else { // copy to avoid mutating function arguments
		u := *updateService
		updateService = &u
	}

	queryParams := updateService.Query()
	queryParams.Set("arch", "multi")
	queryParams.Set("channel", channel)
	updateService.RawQuery = queryParams.Encode()
	req, err := http.NewRequest("GET", updateService.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.redhat.cincinnati.v1+json")
	if len(userAgent) > 0 {
		req.Header.Set("User-Agent", userAgent)
	}
	client := &http.Client{}
	if roundTripper != nil {
		client.Transport = &roundTrip{roundTripper: roundTripper}
	}
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned unexpected HTTP status %s", updateService, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var graph graph
	err = json.Unmarshal(body, &graph)
	if err != nil {
		return nil, err
	}

	releases := make([]configv1.Release, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		channels := strings.Split(node.Metadata.ChannelsString, ",")
		if len(node.Metadata.ChannelsString) == 0 {
			channels = nil
		}
		releases = append(releases, configv1.Release{
			Version:  node.Version,
			Image:    node.Payload,
			Channels: channels,
		})
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
