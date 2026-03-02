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

package cincinatti

import (
	"errors"
	"net/url"

	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

// GetCincinnatiURI returns the appropriate Cincinnati graph URI based on the channel group.
// For nightly channels, it returns the CI nightly graph.
// For all other channels (stable, fast, candidate, eus), it returns the production graph.
func GetCincinnatiURI(channelGroup string) (*url.URL, error) {
	if channelGroup == "nightly" {
		return url.Parse("https://multi.ocp.releases.ci.openshift.org/graph")
	}
	return url.Parse("https://api.openshift.com/api/upgrades_info/graph")
}

// IsCincinnatiVersionNotFoundError checks if an error from Cincinnati is specifically a "VersionNotFound" error.
// This error indicates that the queried version does not exist in the Cincinnati update graph.
func IsCincinnatiVersionNotFoundError(err error) bool {
	var cincinnatiErr *cincinnati.Error
	return errors.As(err, &cincinnatiErr) && cincinnatiErr.Reason == "VersionNotFound"
}
