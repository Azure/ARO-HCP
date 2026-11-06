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

// Package namespacedeletiongate holds the safety checks a NamespaceDeletion
// request must pass, shared by the admin API (which evaluates them at POST) and
// the backend controller (which re-evaluates them before every mutation).
//
// They live here rather than in either caller because evaluating them in only
// one place would be a bug: POST rejects what it can answer from Cosmos so an
// operator gets an immediate, actionable error, and the controller repeats the
// work because an accepted POST is a recorded request, never a statement that
// the cleanup is still safe when it eventually runs.
package namespacedeletiongate

import "strings"

// protectedNamespaceExact are namespaces this action must never target,
// whatever their state.
var protectedNamespaceExact = map[string]bool{
	"default":           true,
	"kube-system":       true,
	"kube-public":       true,
	"kube-node-lease":   true,
	"hypershift":        true,
	"cert-manager":      true,
	"gatekeeper-system": true,
}

// protectedNamespacePrefixes guard the platform's own namespaces.
//
// This is a denylist and not an attempt to positively identify an HCP
// namespace. A prefix is never accepted as proof that a namespace belongs to a
// cluster — that remains the ownership gate's job. A prefix is perfectly good
// evidence that a namespace is *not* a candidate, which is all this is used
// for.
var protectedNamespacePrefixes = []string{
	"kube-",
	"openshift-",
	"azure-",
	"open-cluster-management",
	"calico-",
	"tigera-",
	"maestro",
}

// IsProtectedNamespace reports whether the namespace is one this action must
// never target.
//
// The ocm-<env>-<id> grammar already excludes kube-system and friends, so this
// is redundant on paper. It is kept and evaluated first anyway because it is
// cheap, independent of the grammar being right, and definitive rather than
// provisional — a bug in the grammar parser should not be able to put a
// platform namespace in reach.
func IsProtectedNamespace(namespaceName string) bool {
	name := strings.ToLower(strings.TrimSpace(namespaceName))
	if protectedNamespaceExact[name] {
		return true
	}
	for _, prefix := range protectedNamespacePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
