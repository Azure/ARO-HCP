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

package fleetapihelpers

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

var rolloutChannelPattern = regexp.MustCompile(`^(stable|fast|candidate|nightly)-(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// RolloutVersionFromName parses the canonical y-stream channel used as a rollout name.
func RolloutVersionFromName(name string) (coreapi.VersionProfile, error) {
	group, minor, _ := strings.Cut(name, "-")
	_, parseErr := semver.Parse(minor + ".0")
	if !rolloutChannelPattern.MatchString(name) || parseErr != nil {
		return coreapi.VersionProfile{}, fmt.Errorf("y-stream channel must be <stable|fast|candidate|nightly>-<major>.<minor>")
	}
	return coreapi.VersionProfile{ID: minor, ChannelGroup: group}, nil
}
