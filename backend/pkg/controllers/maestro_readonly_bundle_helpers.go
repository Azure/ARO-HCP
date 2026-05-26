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

package controllers

// readonlyBundleManagedByK8sLabelKey is the K8s label key that identifies
// which backend controller manages a readonly Maestro bundle. The
// delete-orphaned-bundles controller filters Maestro bundles by this label
// so the bundle-cleanup migration knows which bundles it is allowed to
// touch.
//
// The bundle-creator controllers that wrote these labels were retired in
// favor of the kube-applier-based ReadDesire flow; the label only persists
// here so the cleanup path (delete_orphaned_maestro_readonly_bundles_controller.go
// and the upcoming one-shot cleanup) keeps working until those bundles are
// gone from every shard.
const (
	readonlyBundleManagedByK8sLabelKey = "aro-hcp.azure.com/readonly-bundle-managed-by"

	// readonlyBundleManagedByK8sLabelValueClusterScoped is the value the
	// cluster-scoped bundle creator used to tag its bundles.
	readonlyBundleManagedByK8sLabelValueClusterScoped = "create-cluster-scoped-maestro-readonly-bundles-controller"

	// readonlyBundleManagedByK8sLabelValueNodePoolScoped is the value the
	// nodepool-scoped bundle creator used to tag its bundles.
	readonlyBundleManagedByK8sLabelValueNodePoolScoped = "create-nodepool-scoped-maestro-readonly-bundles-controller"
)
