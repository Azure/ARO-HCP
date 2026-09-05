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

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kshrk"
)

// KshrkWriter writes gathered cluster state as a .kshrk archive
// (github.com/phenixblue/k8shark's portable capture format, via pkg/kshrk)
// instead of a filesystem tree — used for whole-cluster export rather than
// one namespace's inspection.
//
// Call order matters: WriteResources (the poll baseline) must be called
// before WriteResourceHistory and WriteEvents, since it builds the
// CRDNameResolver those calls consult for exact plural/singular/shortNames.
// Finish must be called last, exactly once, after every Write* call, to seal
// the archive — it is not part of the Writer interface since no other
// implementation needs it; callers that construct a KshrkWriter call it
// directly on the concrete type.
type KshrkWriter struct {
	aw          *kshrk.ArchiveWriter
	clusterName string
	windowStart time.Time
	windowEnd   time.Time

	resolver       *CRDNameResolver
	discovered     map[groupVersion]map[string]CRDNames // group/version -> kind -> names
	kubeletVersion string                               // best-effort, from the first captured Node

	// pollWritten and watchKinds track which (group,version,resource,namespace)
	// collections got a real poll-baseline record and which got at least one
	// watch record, respectively — so Finish can register an empty
	// poll-baseline placeholder for any collection watch-history touched but
	// the poll-baseline query returned nothing for (e.g. an ephemeral mgmt
	// cluster whose resource-watcher started after windowStart). Without an
	// index.json entry, k8shark's AggregateAcrossNamespaces (the "-A" list
	// path) can't discover the collection exists at all, even though
	// ReconstructAt already handles a missing baseline correctly per-path.
	pollWritten map[collectionKey]bool
	watchKinds  map[collectionKey]string
}

type groupVersion struct {
	group, version string
}

var _ Writer = (*KshrkWriter)(nil)

// NewKshrkWriter creates a KshrkWriter that will write to outputPath.
// clusterName is recorded into metadata.json's server_address.
// windowStart/windowEnd are the user-requested export window: windowStart is
// used as the poll-baseline records' captured_at and the archive's
// metadata.json captured_at; windowEnd is captured_until and the timestamp
// used for the events snapshot (events keep accruing count/lastSeen for the
// whole window, unlike the resource-snapshot poll baseline).
func NewKshrkWriter(outputPath, clusterName string, windowStart, windowEnd time.Time) (*KshrkWriter, error) {
	aw, err := kshrk.NewArchiveWriter(outputPath)
	if err != nil {
		return nil, err
	}
	return &KshrkWriter{
		aw:          aw,
		clusterName: clusterName,
		windowStart: windowStart,
		windowEnd:   windowEnd,
		resolver:    NewCRDNameResolver(nil),
		discovered:  make(map[groupVersion]map[string]CRDNames),
		pollWritten: make(map[collectionKey]bool),
		watchKinds:  make(map[collectionKey]string),
	}, nil
}

// NamespaceOutputPath is a no-op for KshrkWriter — an archive has no
// per-namespace filesystem location to report.
func (w *KshrkWriter) NamespaceOutputPath(_ string) string {
	return ""
}

// WriteContainerLog is a no-op: .kshrk has no log concept.
func (w *KshrkWriter) WriteContainerLog(_ context.Context, _, _, _ string, _ []LogLine) error {
	return nil
}

// WriteResources writes resources as poll-baseline List records, one per
// (group, version, resource, namespace) collection actually present in
// resources. It also (re)builds the CRDNameResolver later Write* calls and
// Finish consult, from any captured CustomResourceDefinition objects here.
func (w *KshrkWriter) WriteResources(_ context.Context, _ string, resources []Resource) error {
	w.resolver = NewCRDNameResolver(resources)

	for _, res := range resources {
		if res.Kind == "Node" && w.kubeletVersion == "" {
			w.kubeletVersion = nodeKubeletVersion(res.Object)
		}
	}

	groups := make(map[collectionKey][]Resource)
	for _, res := range resources {
		group, version := splitAPIVersion(res.APIVersion)
		plural := w.resolver.Plural(group, res.Kind)
		key := collectionKey{group: group, version: version, plural: plural, namespace: res.Namespace}
		groups[key] = append(groups[key], res)
		w.recordDiscovery(group, version, res.Kind, res.Namespace != "")
	}

	var errs []error
	for key, group := range groups {
		apiPath := apiPathFor(key.group, key.version, key.plural, key.namespace)
		items := make([]any, 0, len(group))
		for _, res := range group {
			items = append(items, res.Object)
		}
		body := map[string]any{
			"apiVersion": groupVersionString(key.group, key.version),
			"kind":       group[0].Kind + "List",
			"metadata":   map[string]any{},
			"items":      items,
		}
		if err := w.aw.WritePollRecord(apiPath, w.windowStart, body, len(items)); err != nil {
			errs = append(errs, fmt.Errorf("writing poll record for %s: %w", apiPath, err))
		}
		w.pollWritten[key] = true
	}
	return joinErrors(errs)
}

// collectionKey identifies one archive api_path: everything sharing a
// (group, version, resource, namespace) collapses into one List/watch-index
// entry, matching k8shark's per-namespace capture shape.
type collectionKey struct {
	group, version, plural, namespace string
}

// WriteResourceHistory writes the raw Add/Update/Delete changelog as
// watch-index records, one per event, in the order given — the caller is
// expected to pass events ordered by timestamp ascending, matching the
// underlying Kusto query's own ORDER BY.
func (w *KshrkWriter) WriteResourceHistory(_ context.Context, events []ResourceEvent) error {
	var errs []error
	for _, ev := range events {
		eventType, err := watchEventType(ev.Event)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s/%s (%s): %w", ev.Resource.Namespace, ev.Resource.Name, ev.Resource.Kind, err))
			continue
		}
		group, version := splitAPIVersion(ev.Resource.APIVersion)
		plural := w.resolver.Plural(group, ev.Resource.Kind)
		w.recordDiscovery(group, version, ev.Resource.Kind, ev.Resource.Namespace != "")
		key := collectionKey{group: group, version: version, plural: plural, namespace: ev.Resource.Namespace}
		w.watchKinds[key] = ev.Resource.Kind
		apiPath := apiPathFor(group, version, plural, ev.Resource.Namespace)
		if err := w.aw.WriteWatchRecord(apiPath, eventType, ev.Timestamp, ev.Resource.Object); err != nil {
			errs = append(errs, fmt.Errorf("writing watch record for %s: %w", apiPath, err))
		}
	}
	return joinErrors(errs)
}

// WriteEvents synthesizes a core/v1 Event object per row — the Kusto
// kubernetesEvents table stores a distilled view (reason/message/count/
// firstSeen/lastSeen), not a raw Event object — and writes one poll-baseline
// List per namespace, captured as of windowEnd.
func (w *KshrkWriter) WriteEvents(_ context.Context, _ string, events []map[string]any) error {
	byNamespace := make(map[string][]any)
	for _, row := range events {
		namespace := asString(row["eventNamespace"])
		if namespace == "" {
			// Real clusters record cluster-scoped-object events (e.g. for a
			// Node) under "default" — Event objects are always namespaced.
			namespace = "default"
		}
		byNamespace[namespace] = append(byNamespace[namespace], synthesizeEvent(row, namespace))
	}
	w.recordDiscovery("", "v1", "Event", true)

	var errs []error
	for namespace, items := range byNamespace {
		apiPath := apiPathFor("", "v1", "events", namespace)
		body := map[string]any{
			"apiVersion": "v1",
			"kind":       "EventList",
			"metadata":   map[string]any{},
			"items":      items,
		}
		if err := w.aw.WritePollRecord(apiPath, w.windowEnd, body, len(items)); err != nil {
			errs = append(errs, fmt.Errorf("writing events record for namespace %q: %w", namespace, err))
		}
	}
	return joinErrors(errs)
}

// Finish writes minimal discovery records (one /api/v1 and one
// /apis/<group>/<version> APIResourceList per group-version actually
// present) and metadata.json, then seals the archive. Must be called
// exactly once, after every Write* call. On error the partially-written
// archive is removed.
func (w *KshrkWriter) Finish() error {
	if err := w.writeEmptyBaselinePlaceholders(); err != nil {
		_ = w.aw.Abort()
		return err
	}

	if err := w.writeDiscovery(); err != nil {
		_ = w.aw.Abort()
		return err
	}

	kubernetesVersion := w.kubeletVersion
	if kubernetesVersion == "" {
		kubernetesVersion = "unknown"
	}
	meta := kshrk.CaptureMetadata{
		CaptureID:         uuid.NewString(),
		CapturedAt:        w.windowStart,
		CapturedUntil:     w.windowEnd,
		KubernetesVersion: kubernetesVersion,
		ServerAddress:     "kusto-reconstructed://" + w.clusterName,
	}
	if err := w.aw.Finish(meta); err != nil {
		_ = w.aw.Abort()
		return fmt.Errorf("sealing archive: %w", err)
	}
	return nil
}

// writeEmptyBaselinePlaceholders registers an empty poll-baseline record (at
// windowStart, zero items) for every collection watch-history touched but the
// poll-baseline query returned nothing for. ReconstructAt already reconstructs
// state correctly for a path with no poll record at all (it synthesizes an
// empty baseline internally and replays every watch event on top — the exact
// mechanism this placeholder also produces), so this changes nothing about
// what any point-in-time query returns; it only makes the collection
// discoverable to k8shark's cross-namespace "-A" aggregation, which enumerates
// candidate paths from index.json alone.
func (w *KshrkWriter) writeEmptyBaselinePlaceholders() error {
	var errs []error
	for key, kind := range w.watchKinds {
		if w.pollWritten[key] {
			continue
		}
		apiPath := apiPathFor(key.group, key.version, key.plural, key.namespace)
		body := map[string]any{
			"apiVersion": groupVersionString(key.group, key.version),
			"kind":       kind + "List",
			"metadata":   map[string]any{},
			"items":      []any{},
		}
		if err := w.aw.WritePollRecord(apiPath, w.windowStart, body, 0); err != nil {
			errs = append(errs, fmt.Errorf("writing empty baseline placeholder for %s: %w", apiPath, err))
			continue
		}
		w.pollWritten[key] = true
	}
	return joinErrors(errs)
}

// recordDiscovery notes that kind (in group/version) was seen, keeping the
// first name resolution found for it (a captured CustomResourceDefinition
// when available via CRDNameResolver, otherwise the heuristic plural with
// namespaced reflecting the actual resource instance this call came from).
func (w *KshrkWriter) recordDiscovery(group, version, kind string, namespaced bool) {
	gv := groupVersion{group: group, version: version}
	if w.discovered[gv] == nil {
		w.discovered[gv] = make(map[string]CRDNames)
	}
	if _, ok := w.discovered[gv][kind]; ok {
		return
	}
	names, ok := w.resolver.Resolve(group, kind)
	if !ok {
		plural := w.resolver.Plural(group, kind)
		names = CRDNames{
			Plural:     plural,
			Singular:   strings.ToLower(kind),
			ShortNames: builtinShortNames[plural],
			Namespaced: namespaced,
		}
	}
	w.discovered[gv][kind] = names
}

func (w *KshrkWriter) writeDiscovery() error {
	var errs []error
	for gv, kinds := range w.discovered {
		resourceEntries := make([]any, 0, len(kinds))
		for kind, names := range kinds {
			entry := map[string]any{
				"name":       names.Plural,
				"namespaced": names.Namespaced,
				"kind":       kind,
				"verbs":      []string{"get", "list", "watch"},
			}
			if names.Singular != "" {
				entry["singularName"] = names.Singular
			}
			if len(names.ShortNames) > 0 {
				entry["shortNames"] = names.ShortNames
			}
			resourceEntries = append(resourceEntries, entry)
		}
		body := map[string]any{
			"kind":         "APIResourceList",
			"apiVersion":   "v1",
			"groupVersion": groupVersionString(gv.group, gv.version),
			"resources":    resourceEntries,
		}
		var apiPath string
		if gv.group == "" {
			apiPath = "/api/" + gv.version
		} else {
			apiPath = "/apis/" + gv.group + "/" + gv.version
		}
		if err := w.aw.WritePollRecord(apiPath, w.windowStart, body, len(resourceEntries)); err != nil {
			errs = append(errs, fmt.Errorf("writing discovery record for %s: %w", apiPath, err))
		}
	}
	return joinErrors(errs)
}

// splitAPIVersion splits an apiVersion string into its group and version
// ("hypershift.openshift.io/v1beta1" -> "hypershift.openshift.io",
// "v1beta1"; "v1" -> "", "v1").
func splitAPIVersion(apiVersion string) (group, version string) {
	if g, v, ok := strings.Cut(apiVersion, "/"); ok {
		return g, v
	}
	return "", apiVersion
}

// apiPathFor builds the REST collection path a group/version/plural/namespace
// would be listed at ("" namespace omits the /namespaces/<ns> segment, for
// cluster-scoped resources).
func apiPathFor(group, version, plural, namespace string) string {
	var base string
	if group == "" {
		base = "/api/" + version
	} else {
		base = "/apis/" + group + "/" + version
	}
	if namespace != "" {
		base += "/namespaces/" + namespace
	}
	return base + "/" + plural
}

// groupVersionString renders a group/version pair the way apiVersion/
// groupVersion fields expect ("" group -> just the version).
func groupVersionString(group, version string) string {
	if group == "" {
		return version
	}
	return group + "/" + version
}

// watchEventType maps a kubernetesResourceSnapshots.event value to the
// watch-index event_type k8shark expects.
func watchEventType(event string) (string, error) {
	switch event {
	case "Add":
		return "ADDED", nil
	case "Update":
		return "MODIFIED", nil
	case "Delete":
		return "DELETED", nil
	default:
		return "", fmt.Errorf("unrecognized resource-snapshot event %q", event)
	}
}

// nodeKubeletVersion extracts status.nodeInfo.kubeletVersion from a captured
// Node object, for a best-effort metadata.json kubernetes_version.
func nodeKubeletVersion(object map[string]any) string {
	status, _ := object["status"].(map[string]any)
	if status == nil {
		return ""
	}
	nodeInfo, _ := status["nodeInfo"].(map[string]any)
	if nodeInfo == nil {
		return ""
	}
	version, _ := nodeInfo["kubeletVersion"].(string)
	return version
}

// synthesizeEvent builds a minimal, valid core/v1 Event object from one
// summarized kubernetesEvents row (see events.kql.gotmpl's projection).
func synthesizeEvent(row map[string]any, namespace string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Event",
		"metadata": map[string]any{
			"name":      uuid.NewString(),
			"namespace": namespace,
		},
		"involvedObject": map[string]any{
			"apiVersion": asString(row["objectApiVersion"]),
			"kind":       asString(row["objectKind"]),
			"name":       asString(row["objectName"]),
			"namespace":  namespace,
		},
		"reason":         asString(row["reason"]),
		"message":        asString(row["message"]),
		"type":           asString(row["kubeEventType"]),
		"source":         map[string]any{"component": asString(row["sourceComponent"])},
		"firstTimestamp": asString(row["firstSeen"]),
		"lastTimestamp":  asString(row["lastSeen"]),
		"count":          asInt(row["count"]),
	}
}

// asInt best-effort parses a Kusto row value (rendered as a string by
// rowToMap) into an int, defaulting to 0 when empty or unparseable.
func asInt(v any) int {
	s := asString(v)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
