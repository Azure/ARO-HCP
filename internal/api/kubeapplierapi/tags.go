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

package kubeapplierapi

const (
	// TagControllerName is the well-known key under ApplyDesire.Tags and
	// ReadDesire.Tags that records which controller authored the desire. Every
	// desire persisted through the shared kubeapplierhelpers ensure helpers must
	// carry this tag so an operator (or a cleanup path) can attribute each stored
	// desire to the controller responsible for it.
	TagControllerName = "ControllerName"

	// ClusterResourcesControllerName is the TagControllerName value stamped on
	// ApplyDesires authored by the ClusterResourcesController. It mirrors the
	// constant PR #6070 introduces in its
	// backend/pkg/controllers/clusterresources package; it is defined here so the
	// cluster-deletion gates can reference the value on main before that package
	// exists. When #6070 lands it should consume this constant rather than
	// redefine its own, so the tag value stays single-sourced.
	ClusterResourcesControllerName = "ClusterResourcesController"
)
