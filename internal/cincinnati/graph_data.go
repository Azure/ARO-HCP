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

package cincinnati

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
)

// GraphDataURL is the archive endpoint documented by openshift/cincinnati-graph-data.
const GraphDataURL = "https://api.openshift.com/api/upgrades_info/graph-data"

const (
	maxGraphDataCompressed = 16 << 20
	maxGraphDataExpanded   = 64 << 20
	maxGraphDataEntry      = 4 << 20
	maxGraphDataEntries    = 10000
	graphDataTimeout       = time.Minute
)

type GraphDataClient struct {
	client *http.Client
	url    string
}

func NewGraphDataClient() *GraphDataClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Each discovery request owns its connections and releases them before returning.
	transport.DisableKeepAlives = true
	return &GraphDataClient{client: &http.Client{Timeout: graphDataTimeout, Transport: transport}, url: GraphDataURL}
}

// VersionProfiles returns complete, validated minor version and channel group discovery results.
func (c *GraphDataClient) VersionProfiles(ctx context.Context) ([]coreapi.VersionProfile, error) {
	defer c.client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, graphDataTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch graph-data: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch graph-data: HTTP %d", resp.StatusCode)
	}
	return ParseGraphData(resp.Body)
}

// ParseGraphData reads the documented gzip tar format entirely in memory, with
// independent compressed, expanded, entry-size and entry-count bounds. Gzip
// checksums are validated before returning data.
func ParseGraphData(reader io.Reader) ([]coreapi.VersionProfile, error) {
	compressed, err := io.ReadAll(io.LimitReader(reader, maxGraphDataCompressed+1))
	if err != nil {
		return nil, fmt.Errorf("read graph-data: %w", err)
	}
	if len(compressed) > maxGraphDataCompressed {
		return nil, fmt.Errorf("compressed graph-data exceeds limit")
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("open graph-data gzip: %w", err)
	}
	defer gz.Close()
	expanded, err := io.ReadAll(io.LimitReader(gz, maxGraphDataExpanded+1))
	if err != nil {
		return nil, fmt.Errorf("expand graph-data: %w", err)
	}
	if len(expanded) > maxGraphDataExpanded {
		return nil, fmt.Errorf("expanded graph-data exceeds limit")
	}
	if len(expanded)%512 != 0 {
		return nil, fmt.Errorf("graph-data tar contains a partial block")
	}
	tarBytes := bytes.NewReader(expanded)
	archive := tar.NewReader(terminatedTarReader{tarBytes})
	seenPaths := sets.New[string]()
	profiles := map[string]coreapi.VersionProfile{}
	versionSeen, channelSeen := false, false
	for entries := 0; ; entries++ {
		header, err := archive.Next()
		if err == io.EOF {
			trailing, _ := io.ReadAll(tarBytes)
			if len(bytes.Trim(trailing, "\x00")) != 0 {
				return nil, fmt.Errorf("unexpected data after graph-data tar end marker")
			}
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read graph-data tar: %w", err)
		}
		if entries >= maxGraphDataEntries || header.Size > maxGraphDataEntry {
			return nil, fmt.Errorf("graph-data archive entry limit exceeded")
		}
		name := strings.TrimPrefix(header.Name, "./")
		if header.Typeflag == tar.TypeDir && (name == "" || name == ".") {
			continue
		}
		if path.IsAbs(name) || strings.Contains(name, "\\") || path.Clean(name) != strings.TrimSuffix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("invalid graph-data path %q", header.Name)
		}
		if seenPaths.Has(name) {
			return nil, fmt.Errorf("duplicate graph-data path %q", name)
		}
		seenPaths.Insert(name)
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("unsupported graph-data entry type for %q", name)
		}
		data, err := io.ReadAll(archive)
		if err != nil {
			return nil, fmt.Errorf("read graph-data entry %q: %w", name, err)
		}
		if name == "version" {
			version, err := semver.Parse(strings.TrimSpace(string(data)))
			if err != nil || version.Major != 1 || version.Minor > 1 || version.Patch != 0 || len(version.Pre) != 0 || len(version.Build) != 0 {
				return nil, fmt.Errorf("unsupported graph-data schema %q (supports 1.1.0)", strings.TrimSpace(string(data)))
			}
			versionSeen = true
			continue
		}
		if path.Dir(name) != "channels" || (path.Ext(name) != ".yaml" && path.Ext(name) != ".yml") {
			continue
		}
		// Inspect YAML nodes directly and track keys in a set for linear-time
		// duplicate detection.
		var channel yaml.Node
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		if err := decoder.Decode(&channel); err != nil {
			return nil, fmt.Errorf("parse channel %q: %w", name, err)
		}
		var extra yaml.Node
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("channel %q must contain exactly one YAML document", name)
		}
		if len(channel.Content) != 1 || channel.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("channel %q must contain a YAML mapping", name)
		}
		keys := sets.New[string]()
		channelName := ""
		fields := channel.Content[0].Content
		for i := 0; i < len(fields); i += 2 {
			key, value := fields[i], fields[i+1]
			if key.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("channel %q must contain scalar keys", name)
			}
			if keys.Has(key.Value) {
				return nil, fmt.Errorf("channel %q has duplicate key %q", name, key.Value)
			}
			keys.Insert(key.Value)
			if key.Value == "name" {
				if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
					return nil, fmt.Errorf("channel %q name must be a scalar string", name)
				}
				channelName = value.Value
			}
		}
		if channelName == "" {
			return nil, fmt.Errorf("channel %q has no name", name)
		}
		channelSeen = true
		group, _, _ := strings.Cut(channelName, "-")
		if !metadataapi.AllowedChannelGroupsWithExperimentalFlag.Has(group) {
			continue
		}
		profile, err := fleetapihelpers.RolloutVersionFromName(channelName)
		if err != nil {
			return nil, err
		}
		profiles[channelName] = profile
	}
	if !versionSeen || !channelSeen {
		return nil, fmt.Errorf("graph-data archive must contain a schema version and channels")
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	result := make([]coreapi.VersionProfile, 0, len(names))
	for _, name := range names {
		result = append(result, profiles[name])
	}
	return result, nil
}

// terminatedTarReader makes physical exhaustion an error. archive/tar returns
// io.EOF itself after consuming two zero header blocks at a parsed record boundary,
// which establishes completeness even when the last file contains zero bytes.
type terminatedTarReader struct {
	io.Reader
}

func (r terminatedTarReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}
