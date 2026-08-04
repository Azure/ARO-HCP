package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

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

// edge represents an unconditional upgrade path between two versions in the graph.
// It is serialized as a two-element array [origin, destination] in JSON.
type edge struct {
	// Origin is the index of the source version node.
	Origin int

	// Destination is the index of the target version node.
	Destination int
}

// conditionalEdge represents a conditional update path between two versions in the graph.
type conditionalEdge struct {
	// From is the semantic version string of the source release.
	From string `json:"from"`

	// To is the semantic version string of the target release.
	To string `json:"to"`
}

// conditionalEdges groups a set of conditional upgrade edges with their shared risks.
// All edges in this group are subject to the same risk conditions.
type conditionalEdges struct {
	// Edges contains the conditional upgrade paths sharing these risks.
	Edges []conditionalEdge `json:"edges"`

	// Risks defines the conditions that must be evaluated to determine if
	// these conditional updates are recommended for a particular cluster.
	Risks []configv1.ConditionalUpdateRisk `json:"risks"`
}

// graph represents the update graph structure returned by the Cincinnati service.
// It defines all available cluster versions and the valid upgrade paths between them.
type graph struct {
	// Nodes contains all cluster version releases available in this channel.
	Nodes []node `json:"nodes"`

	// Edges defines unconditional upgrade paths as index pairs referencing Nodes.
	// Each edge indicates a recommended upgrade from one version to another.
	Edges []edge `json:"edges"`

	// ConditionalEdges defines upgrade paths that require risk evaluation.
	// These upgrades are only recommended if their associated risks are acceptable.
	ConditionalEdges []conditionalEdges `json:"conditionalEdges"`
}

// UnmarshalJSON deserializes an edge from its JSON representation.
// Edges are represented in JSON as two-element arrays [origin, destination],
// but are stored in Go as a struct with named fields, requiring this custom
// unmarshaling logic.
func (e *edge) UnmarshalJSON(data []byte) error {
	var fields []int
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	if len(fields) != 2 {
		return fmt.Errorf("expected 2 fields, found %d", len(fields))
	}

	e.Origin = fields[0]
	e.Destination = fields[1]

	return nil
}

// MarshalJSON serializes an edge into its JSON representation, inverting UnmarshalJSON.
func (e *edge) MarshalJSON() ([]byte, error) {
	rawEdge := []int{e.Origin, e.Destination}
	return json.Marshal(rawEdge)
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
func cincinnati(ctx context.Context, roundTripper RoundTrip, updateService *url.URL, userAgent string, channel string) ([]configv1.Release, *graph, *url.URL, error) {
	if updateService == nil {
		var err error
		updateService, err = url.Parse(defaultUpstreamUpdateService)
		if err != nil {
			return nil, nil, nil, err
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
		return nil, nil, updateService, err
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
		return nil, nil, updateService, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, updateService, fmt.Errorf("%s returned unexpected HTTP status %s", updateService, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, updateService, err
	}
	var graph graph
	err = json.Unmarshal(body, &graph)
	if err != nil {
		return nil, nil, updateService, err
	}

	releases, err := nodesToReleases(graph.Nodes)
	return releases, &graph, updateService, err
}

// nodesToReleases converts a slice of update-service nodes to a slice
// of Releases (mostly parsing channel metadata).
func nodesToReleases(nodes []node) ([]configv1.Release, error) {
	releases := make([]configv1.Release, 0, len(nodes))
	for _, node := range nodes {
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

	return releases, nil
}
