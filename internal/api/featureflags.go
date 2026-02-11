// Copyright 2025 Microsoft Corporation
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

package api

const (
	// Feature flag FeatureAllowDevNonStableChannels is the feature in the subscription that
	// allows the usage of non stable channels (i.e. candidate, nightly) for creation
	// of new OpenShift clusters.
	FeatureAllowDevNonStableChannels = "Microsoft.RedHatOpenShift/AllowDevNonStableChannels"

	// FeatureExperimentalReleaseFeatures is the subscription-level AFEC that gates all
	// tag-based experimental features. When registered, per-resource tags in the
	// "aro-hcp.experimental.*" namespace are honored. Without this AFEC, experimental
	// tags are ignored.
	FeatureExperimentalReleaseFeatures = "Microsoft.RedHatOpenShift/ExperimentalReleaseFeatures"

	// TagClusterSingleReplica is the ARM resource tag that enables
	// single-replica control plane components when the
	// ExperimentalReleaseFeatures AFEC is registered on the subscription.
	TagClusterSingleReplica = "aro-hcp.experimental.cluster/single-replica"

	// TagClusterSizeOverride is the ARM resource tag that enables the
	// ClusterSizeOverride annotation for reduced resource requests when the
	// ExperimentalReleaseFeatures AFEC is registered on the subscription.
	TagClusterSizeOverride = "aro-hcp.experimental.cluster/size-override"
)
