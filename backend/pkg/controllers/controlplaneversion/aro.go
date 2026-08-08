package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/klog/v2"
)

// ARO-HCP specific user agent; put whatever you like in here to identify yourself.
var userAgent = "AROHCPFixme/0.1"

// SelectControlPlaneVersion must run at a scale of an estimated 100 qps
// SelectControlPlaneVersion must return a z-stream that always has a path for 4.y+1 up to 4.y(latest) and then the latest 4.y(latest).latest.
// The selected z-stream must be the most recent available z-stream that meets those criteria
// The hostedCluster may not be present in all scenarios, since cases like install doesn't have a hostedCluster.
// The hostedCluster may never have achieved any stable level, but must still select the best z-stream.
// The z-steam chosen for 4.y, must be able to upgrade to a 4.y+1.z that can itself upgrade to a 4.y+2.z.
// This ensures that we can always upgrade to the latest level.
// When a particular channelStability 4.y+1 has no upgrade paths from 4.y, then we select the latest z-stream for that 4.y.
// We move from 4.22 to 5.0 in our numbering scheme, so 5.0 is considered the 4.y+1 for 4.22.
// If the hostedCluster has previous values in .status.controlPlaneVersion.history that are partially or fully installed,
// be sure to only select levels that have upgrade paths from *all* present versions.
// To the implementer: if you cannot honor conditional edges yet, know that future bugs will press on being able to provide
// that value on HostedCluster upgrade decisions.
// Remember that edges can be removed and if an edge from 4.20.12 to 4.21.4 is removed.  This diverges into two cases
//  1. no level of 4.20 can upgrade to 4.21.  In this case, upgrade to the latest 4.20.z
//  2. some levels of 4.20 can upgrade to 4.21.  In this case, select the 4.20.12 clusters must wait to upgrade until a new z-stream
//     can move to 4.21.  During this time, this function must return nil for the targetVersion and a structured error until
//     a new edge from 4.20 to 4.21 appears and we can upgrade to that later 4.20.z.
func SelectControlPlaneVersion(ctx context.Context, channelStability string, desiredYVersion semver.Version, roundTripper RoundTrip, updateService *url.URL, hostedCluster *hypershiftv1beta1.HostedCluster) (*semver.Version, error) {
	channel := fmt.Sprintf("%s-%d.%d", channelStability, desiredYVersion.Major, desiredYVersion.Minor)
	if hostedCluster == nil {
		connectivityRanker := preferFeatureConnectivityOverPatchFixes(ctx, roundTripper, updateService, userAgent, channelStability, channel)
		rankRelease := func(release configv1.Release) (float32, error) {
			if err := aroInstallAcceptable(release); err != nil {
				return 0, err
			}
			return connectivityRanker(release)
		}
		return defaultInstall(ctx, roundTripper, updateService, userAgent, channel, rankRelease)
	}
	clusterUpdateService := hostedCluster.Spec.UpdateService
	if len(clusterUpdateService) == 0 {
		clusterUpdateService = configv1.URL(defaultUpstreamUpdateService)
	}
	if updateService != nil && configv1.URL(updateService.String()) != clusterUpdateService {
		return nil, fmt.Errorf("HostedCluster spec.updateService %q diverges from the explicitly-requested update service %q.  Call SelectControlPlaneVersion with a nil update service to defer to the current HostedCluster configuration, or update the HostedCluster spec.updateService to match the update service you want that cluster to use.", hostedCluster.Spec.UpdateService, updateService)
	}
	updateService, err := url.Parse(string(clusterUpdateService))
	if err != nil {
		return nil, err
	}
	if channel != hostedCluster.Spec.Channel {
		return nil, fmt.Errorf("HostedCluster spec.channel %q diverges from the explicitly-requested channel %q.  Call SelectControlPlaneVersion with a channel stability and desired X.Y version that matches the current HostedCluster channel configuration, or update the HostedCluster spec.channel to %q.", hostedCluster.Spec.Channel, channel, channel)
	}
	connectivityRanker := preferFeatureConnectivityOverPatchFixes(ctx, roundTripper, updateService, userAgent, channelStability, channel)
	return defaultUpdate(ctx, hostedCluster, connectivityRanker)
}

// aroInstallAcceptable is maintained by ARO maintainers, not by OCP, to track things that ARO considers install-blocking bugs.
func aroInstallAcceptable(release configv1.Release) error {
	aroInstallBugs := map[string]error{
		"4.22.6": errors.New("FIXME: example ARO-maintainer concerns with 4.22.6 installs"),
	}

	return aroInstallBugs[release.Version] // Perhaps ARO maintainers have more keys to make this concern conditional on specific install-config choices.  If so, they can update this function or use a closure that understands this specific installation request's configuration.
}

// preferFeatureConnectivityOverPatchFixes ranks releases to
// prioritize connections to future features, to keep those updates
// reliably available for the fraction of clusters that decide they want
// those new features.  In exchange, this can leave clusters exposed to
// bug and CVE fixes that have shipped into the configured channel.
//
// Each call to preferFeatureConnectivityOverPatchFixes returns a closure with
// its own update-service cache state.  Create a new closure whenever you want
// to clear that cache.
func preferFeatureConnectivityOverPatchFixes(ctx context.Context, roundTripper RoundTrip, updateService *url.URL, userAgent string, channelStability string, initialChannel string) rankRelease {
	graphs := make(map[string]*graph)
	return func(release configv1.Release) (float32, error) {
		rank, err := walkHighestFeature(ctx, graphs, roundTripper, updateService, userAgent, channelStability, initialChannel, release)
		klog.V(4).Infof("ranked %g for %v with %v (%v)", rank, release.Version, release.Channels, err)
		return rank, err
	}
}

// walkHighestFeature recursively retrieves update-service advice and
// walks from the initial channel through later channels, searching
// for the highest feature reachable from the initial release.  Both
// unconditional and conditionally-recommended updates are treated as
// walkable connections.  A mutable 'graphs' cache can be passed to
// make performance manageable.
func walkHighestFeature(ctx context.Context, graphs map[string]*graph, roundTripper RoundTrip, updateService *url.URL, userAgent string, channelStability string, initialChannel string, release configv1.Release) (float32, error) {
	largestRank, maxChannel := rankReleaseByHighestChannel(release, channelStability)
	if maxChannel == "" || maxChannel == initialChannel { // there's no way to walk from here to a higher channel
		return largestRank, nil
	}

	if graphs == nil {
		graphs = make(map[string]*graph)
	}
	var err error
	var releases []configv1.Release
	graph, ok := graphs[maxChannel]
	if ok {
		releases, err = nodesToReleases(graph.Nodes)
		if err != nil {
			return 0, err
		}
	} else {
		releases, graph, _, err = cincinnati(ctx, roundTripper, updateService, userAgent, maxChannel)
		if err != nil {
			return 0, err
		}
		graphs[maxChannel] = graph // cache to save later repeat requests
	}

	currentReleaseIndex := -1
	for i, r := range releases {
		if r.Version == release.Version {
			currentReleaseIndex = i
			break
		}
	}
	if currentReleaseIndex == -1 {
		return 0, fmt.Errorf("%q not found in channel %q", release.Version, maxChannel)
	}

	for _, edge := range graph.Edges {
		if edge.Origin == currentReleaseIndex {
			rank, err := walkHighestFeature(ctx, graphs, roundTripper, updateService, userAgent, channelStability, maxChannel, releases[edge.Destination])
			if err != nil {
				klog.Errorf("failure walking %q in %q while ranking: %v", releases[edge.Destination].Version, maxChannel, err)
			}
			if rank > largestRank {
				largestRank = rank
			}
		}
	}

	for _, conditionalEdge := range graph.ConditionalEdges {
		for _, edge := range conditionalEdge.Edges {
			if edge.From == release.Version {
				found := false
				for _, r := range releases {
					if r.Version == edge.To {
						found = true
						rank, err := walkHighestFeature(ctx, graphs, roundTripper, updateService, userAgent, channelStability, maxChannel, r)
						if err != nil {
							klog.Errorf("failure walking %q in %q while ranking: %v", r.Version, maxChannel, err)
						}
						if rank > largestRank {
							largestRank = rank
						}
						break
					}
				}
				if !found {
					riskNames := make([]string, 0, len(conditionalEdge.Risks))
					for _, risk := range conditionalEdge.Risks {
						riskNames = append(riskNames, risk.Name)
					}
					klog.Errorf("unable to find target %q, referenced by conditional update %v in %q", edge.To, riskNames, maxChannel)
				}
			}
		}
	}

	return largestRank, nil
}

// rankReleaseByHighestChannel ranks a release by the highest
// channelStability MAJOR.MINOR channel that release is a direct member
// of, returning both the rank and that maximum channel.
func rankReleaseByHighestChannel(release configv1.Release, channelStability string) (float32, string) {
	var rank float32
	maxChannel := ""
	for _, channel := range release.Channels {
		stability, major, minor, err := parseChannel(channel)
		if err != nil {
			klog.Errorf("failed to parse channel from %v while generating an ARO rank for %q: %v", release.Channels, release.Version, err)
			major = 0
			minor = 0
		}
		if stability == channelStability {
			var r float32
			r = float32(major) + float32(minor)/1000 // there will never be more than 1k minor releases in any given major release
			if r > rank {
				rank = r
				maxChannel = channel
			}
		}
	}
	return rank, maxChannel
}

// parseChannel parses a channel string like "stable-4.20" into
// channel stability ("stable"), major (4), and minor (20) components.
func parseChannel(channel string) (string, uint, uint, error) {
	i := strings.LastIndex(channel, "-")
	if i == -1 {
		return "", 0, 0, fmt.Errorf("no - delimiter found in channel %q", channel)
	}
	if i == 0 {
		return "", 0, 0, fmt.Errorf("no channel stability found before the - delimiter in channel %q", channel)
	}
	if i == len(channel)-1 {
		return "", 0, 0, fmt.Errorf("no target version found after the final - delimiter in channel %q", channel)
	}
	channelStability := channel[:i]
	versionString := channel[i+1:]
	versionSegments := strings.Split(versionString, ".")
	if len(versionSegments) != 2 {
		return channelStability, 0, 0, fmt.Errorf("expected a MAJOR.MINOR version in the %q portion of channel %q, but did not get two segments in %v", versionString, channel, versionSegments)
	}

	major, err := strconv.ParseUint(versionSegments[0], 10, 32)
	if err != nil {
		return channelStability, 0, 0, fmt.Errorf("expected a major version in the %q portion of channel %q: %w", versionSegments[0], channel, err)
	}

	minor, err := strconv.ParseUint(versionSegments[1], 10, 32)
	if err != nil {
		return channelStability, uint(major), 0, fmt.Errorf("expected a minor version in the %q portion of channel %q: %w", versionSegments[1], channel, err)
	}

	return channelStability, uint(major), uint(minor), nil
}
