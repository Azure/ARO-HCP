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

package ocadminspect

import "strings"

// irregularResourcePlurals maps a lowercased Kind to its resource (plural)
// name where the naive rules below would be wrong. Real discovery derives
// this exactly; snapshot telemetry only carries the Kind for built-in types,
// so this approximates it.
var irregularResourcePlurals = map[string]string{
	"endpoints": "endpoints", // already plural
}

// ResourcePlural approximates the plural resource name for a Kind (e.g. "Pod"
// -> "pods", "NetworkPolicy" -> "networkpolicies", "Ingress" -> "ingresses").
// It is an approximation of the discovery-derived resource name a real API
// server uses — good enough for file/path naming when no exact name is known.
// See CRDNameResolver for the exact alternative when a CustomResourceDefinition
// was captured.
func ResourcePlural(kind string) string {
	lower := strings.ToLower(kind)
	if lower == "" {
		return ""
	}
	if plural, ok := irregularResourcePlurals[lower]; ok {
		return plural
	}
	switch {
	case strings.HasSuffix(lower, "s"), strings.HasSuffix(lower, "x"),
		strings.HasSuffix(lower, "ch"), strings.HasSuffix(lower, "sh"):
		return lower + "es"
	case strings.HasSuffix(lower, "y") && len(lower) > 1 && !isVowel(lower[len(lower)-2]):
		return lower[:len(lower)-1] + "ies"
	default:
		return lower + "s"
	}
}

// builtinShortNames maps a plural resource name to its well-known kubectl
// short alias, for the built-in Kinds this tool ever captures. There is no
// CustomResourceDefinition for a built-in type, so CRDNameResolver can never
// supply ShortNames for one — and unlike a real API server, k8shark serves a
// captured discovery record verbatim rather than falling back to its own
// built-in short-name table once we've captured one ourselves, so omitting
// these here means "kubectl get ns"/"po"/... simply don't resolve.
var builtinShortNames = map[string][]string{
	"pods":         {"po"},
	"nodes":        {"no"},
	"namespaces":   {"ns"},
	"events":       {"ev"},
	"deployments":  {"deploy"},
	"daemonsets":   {"ds"},
	"replicasets":  {"rs"},
	"statefulsets": {"sts"},
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}

// CRDNames holds the exact naming/scope facts a CustomResourceDefinition
// declares for one Kind.
type CRDNames struct {
	Plural     string
	Singular   string
	ShortNames []string
	Namespaced bool
}

type groupKind struct {
	group string
	kind  string
}

// CRDNameResolver resolves a group/Kind's exact plural/singular/shortNames/
// scope from captured CustomResourceDefinition objects (sourced from
// mgmt-agent's resource watcher, see resourcewatcher.go), falling back to the
// ResourcePlural heuristic when no matching CRD was captured in the query
// window — an archive predating the mgmt-agent CRD-logging change, or a
// quiescent CRD with no row in the lookback window.
type CRDNameResolver struct {
	byGroupKind map[groupKind]CRDNames
}

// NewCRDNameResolver builds a resolver from any captured
// CustomResourceDefinition resources found in resources. Non-CRD resources
// and CRDs with malformed/missing spec fields are skipped.
func NewCRDNameResolver(resources []Resource) *CRDNameResolver {
	r := &CRDNameResolver{byGroupKind: make(map[groupKind]CRDNames)}
	for _, res := range resources {
		if res.Kind != "CustomResourceDefinition" {
			continue
		}
		group, kind, names, ok := parseCRDSpec(res.Object)
		if !ok {
			continue
		}
		r.byGroupKind[groupKind{group: group, kind: kind}] = names
	}
	return r
}

// Resolve returns the exact CRDNames captured for group/kind, or false if no
// matching CustomResourceDefinition was captured.
func (r *CRDNameResolver) Resolve(group, kind string) (CRDNames, bool) {
	if r == nil {
		return CRDNames{}, false
	}
	names, ok := r.byGroupKind[groupKind{group: group, kind: kind}]
	return names, ok
}

// Plural returns the exact plural from a captured CustomResourceDefinition
// when available, otherwise the ResourcePlural heuristic.
func (r *CRDNameResolver) Plural(group, kind string) string {
	if names, ok := r.Resolve(group, kind); ok && names.Plural != "" {
		return names.Plural
	}
	return ResourcePlural(kind)
}

// parseCRDSpec extracts the group/kind/names/scope facts from a captured
// CustomResourceDefinition object. Best-effort: any missing/malformed field
// causes ok=false rather than a partially-populated, possibly-misleading
// result.
func parseCRDSpec(object map[string]any) (group, kind string, names CRDNames, ok bool) {
	spec, _ := object["spec"].(map[string]any)
	if spec == nil {
		return "", "", CRDNames{}, false
	}
	group, _ = spec["group"].(string)
	namesSpec, _ := spec["names"].(map[string]any)
	if group == "" || namesSpec == nil {
		return "", "", CRDNames{}, false
	}
	kind, _ = namesSpec["kind"].(string)
	plural, _ := namesSpec["plural"].(string)
	if kind == "" || plural == "" {
		return "", "", CRDNames{}, false
	}
	singular, _ := namesSpec["singular"].(string)
	var shortNames []string
	if raw, ok := namesSpec["shortNames"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				shortNames = append(shortNames, s)
			}
		}
	}

	return group, kind, CRDNames{
		Plural:     plural,
		Singular:   singular,
		ShortNames: shortNames,
		Namespaced: spec["scope"] == "Namespaced",
	}, true
}
