package controlplaneversion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
)

// RoundTrip allows customizing the RoundTripper (e.g. to support
// testing or proxy/TLS configuration) without having to create a local
// struct supplying the RoundTripper interface.  The interface only
// requires a RoundTrip function, and it is easier to just pass the
// function around.  https://github.com/golang/go/issues/38479 is
// proposing the type become a stdlib declaration, but until then,
// declare it locally.
type RoundTrip func(*http.Request) (*http.Response, error)

// node represents a single cluster version in the update graph.
type node struct {
	// Version is the semantic version of this release.
	Version string `json:"version"`

	// Payload is the release image pullspec (payload) for this version.
	Payload string `json:"payload"`
}

// graph represents the update graph structure returned by the Cincinnati service.
// It defines all available cluster versions and the valid upgrade paths between them.
type graph struct {
	// Nodes contains all cluster version releases available in this channel.
	Nodes []node `json:"nodes"`
}

var defaultUpstreamUpdateService = "https://api.openshift.com/api/upgrades_info/graph"

// roundTrip converts a RoundTrip function into a RoundTripper.
type roundTrip struct {
	roundTripper RoundTrip
}

// RoundTrip executes a single HTTP transaction, returning a Response
// for the provided Request.
func (rt *roundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return rt.roundTripper(request)
}

// cincinnati calls an update-service to retrieve a list of releases,
// and the update recommendation graph connecting them.
func cincinnati(ctx context.Context, roundTripper RoundTrip, updateService *url.URL, userAgent string, channel string) ([]configv1.Release, *url.URL, error) {
	if updateService == nil {
		var err error
		updateService, err = url.Parse(defaultUpstreamUpdateService)
		if err != nil {
			return nil, nil, err
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
		return nil, updateService, err
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
		return nil, updateService, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, updateService, fmt.Errorf("%s returned unexpected HTTP status %s", updateService, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, updateService, err
	}
	var graph graph
	err = json.Unmarshal(body, &graph)
	if err != nil {
		return nil, updateService, err
	}

	releases, err := nodesToReleases(graph.Nodes)
	return releases, updateService, err
}

// nodesToReleases converts a slice of update-service nodes to a slice
// of Releases, sorted in descending SemVer order.
func nodesToReleases(nodes []node) ([]configv1.Release, error) {
	releases := make([]configv1.Release, 0, len(nodes))
	for _, node := range nodes {
		releases = append(releases, configv1.Release{
			Version: node.Version,
			Image:   node.Payload,
		})
	}

	slices.SortFunc(releases, func(a, b configv1.Release) int {
		vA := semver.MustParse(a.Version)
		vB := semver.MustParse(b.Version)
		return -vA.Compare(vB)
	})

	return releases, nil
}
