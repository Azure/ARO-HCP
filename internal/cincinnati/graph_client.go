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

package cincinnati

//go:generate $MOCKGEN -typed -source=graph_client.go -destination=mock_graph_client.go -package cincinnati GraphClient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GraphClient queries the public Cincinnati graph and releasestream APIs.
type GraphClient interface {
	// ChannelExists reports whether Cincinnati publishes an update channel for
	// the given channel group and minor (e.g. stable + "4.21" -> stable-4.21).
	//
	// Observed production behavior (2026-07):
	//   - stable/candidate graph: existing channels return HTTP 200 with nodes; missing
	//     channels also return HTTP 200 with an empty nodes array (not 404).
	//   - nightly: uses the CI releasestream tags API; missing streams return HTTP 404.
	ChannelExists(ctx context.Context, channelGroup, minor string) (bool, error)
}

type graphClient struct {
	graphAPIBase   string
	nightlyAPIBase string
}

// NewGraphClient returns a GraphClient that queries the public Cincinnati APIs.
func NewGraphClient() GraphClient {
	return graphClient{
		graphAPIBase:   "https://api.openshift.com",
		nightlyAPIBase: "https://multi.ocp.releases.ci.openshift.org",
	}
}

func (c graphClient) ChannelExists(ctx context.Context, channelGroup, minor string) (bool, error) {
	if channelGroup == "nightly" {
		return c.nightlyChannelExists(ctx, minor)
	}
	return c.channelExistsFromGraphURL(ctx, channelGroup, minor)
}

func (c graphClient) nightlyChannelExists(ctx context.Context, minor string) (bool, error) {
	releaseStream := fmt.Sprintf("%s.0-0.nightly-multi", minor)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/releasestream/%s/tags?phase=Accepted", c.nightlyAPIBase, url.PathEscape(releaseStream)), nil)
	if err != nil {
		return false, fmt.Errorf("create nightly tags request for %s: %w", releaseStream, err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("query nightly tags for %s: %w", releaseStream, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("query nightly tags for %s returned %s: %s", releaseStream, resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Tags []json.RawMessage `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, fmt.Errorf("decode nightly tags response for %s: %w", releaseStream, err)
	}
	return len(payload.Tags) > 0, nil
}

func (c graphClient) channelExistsFromGraphURL(ctx context.Context, channelGroup, minor string) (bool, error) {
	channel := fmt.Sprintf("%s-%s", channelGroup, minor)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/upgrades_info/v1/graph?channel=%s", c.graphAPIBase, url.QueryEscape(channel)), nil)
	if err != nil {
		return false, fmt.Errorf("create graph request for %s: %w", channel, err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("query graph for %s: %w", channel, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("query graph for %s returned %s: %s", channel, resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, fmt.Errorf("decode graph response for %s: %w", channel, err)
	}
	return len(payload.Nodes) > 0, nil
}
