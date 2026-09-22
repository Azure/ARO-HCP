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

package apihelpers

import (
	"slices"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// OcpV5ArtDevMirrorSource is the platform-managed image source that OpenShift
// 5.y data-plane releases are published under. A cluster whose HostedCluster
// spec.imageContentSources lacks this entry cannot pull 5.y data-plane images,
// so an upgrade into 5.y would strand its nodes.
const OcpV5ArtDevMirrorSource = "quay.io/openshift-release-dev/ocp-v5.0-art-dev"

// HostedClusterHasImageContentSource reports whether the HostedCluster's
// spec.imageContentSources contains an entry for source. A nil HostedCluster
// reports false; callers that need to distinguish "not observed yet" from
// "observed and absent" must check for nil themselves before calling.
func HostedClusterHasImageContentSource(hostedCluster *hsv1beta1.HostedCluster, source string) bool {
	if hostedCluster == nil {
		return false
	}
	return slices.ContainsFunc(hostedCluster.Spec.ImageContentSources, func(imageContentSource hsv1beta1.ImageContentSource) bool {
		return imageContentSource.Source == source
	})
}
