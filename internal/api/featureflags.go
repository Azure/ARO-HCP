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
	// TOMBSTONE - intent replaced by FeatureExperimentalReleaseFeatures
	// Feature flag FeatureAllowDevNonStableChannels is the feature in the subscription that
	// allows the usage of non stable channels (i.e. candidate, nightly) for creation
	// of new OpenShift clusters.
	//FeatureAllowDevNonStableChannels = "Microsoft.RedHatOpenShift/AllowDevNonStableChannels"

	// FeatureExperimentalReleaseFeatures is the subscription-level AFEC that gates all
	// tag-based experimental features. When registered, per-resource tags in the
	// "aro-hcp.experimental.*" namespace are honored. Without this AFEC, experimental
	// tags are ignored.
	FeatureExperimentalReleaseFeatures = "Microsoft.RedHatOpenShift/ExperimentalReleaseFeatures"

	// ExperimentalClusterTagPrefix is the prefix for all experimental cluster
	// tags. Tags with this prefix are only honored when the
	// ExperimentalReleaseFeatures AFEC is registered. Unrecognized tags
	// with this prefix are rejected.
	//
	// Azure ARM tag names must not contain: < > % & \ ? /
	// The Azure Portal additionally rejects: * : +
	// Tags starting with "microsoft", "azure", "windows", or "hidden-"
	// are reserved. Names are limited to 512 characters (128 for storage
	// accounts). See https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/tag-resources
	ExperimentalClusterTagPrefix = "aro-hcp.experimental.cluster."

	// TagClusterSingleReplica is the ARM resource tag that enables
	// single-replica control plane components when the
	// ExperimentalReleaseFeatures AFEC is registered on the subscription.
	TagClusterSingleReplica = ExperimentalClusterTagPrefix + "single-replica"

	// TagClusterSizeOverride is the ARM resource tag that enables the
	// ClusterSizeOverride annotation for reduced resource requests when the
	// ExperimentalReleaseFeatures AFEC is registered on the subscription.
	TagClusterSizeOverride = ExperimentalClusterTagPrefix + "size-override"
)
