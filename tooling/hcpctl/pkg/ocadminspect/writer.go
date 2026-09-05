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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// FilesystemWriter writes gathered namespace state to disk in a layout that
// mirrors `oc adm inspect`:
//
//	<root>/namespaces/<ns>/<ns>.yaml                                    (the Namespace object)
//	<root>/namespaces/<ns>/<group>/<resource>.yaml                      (one List per resource kind, e.g. core/pods.yaml)
//	<root>/namespaces/<ns>/pods/<pod>/<pod>.yaml                        (each pod object; pods also appear in core/pods.yaml)
//	<root>/namespaces/<ns>/pods/<pod>/<container>/<container>/logs/current.log   (container logs)
//	<root>/namespaces/<ns>/core/events.yaml                             (kubernetes events)
//
// where <group> is the API group ("core" for the core group) and <resource> is
// the pluralized kind. Resource files are v1 List objects and every YAML file is
// prefixed with a "---" document separator, matching oc adm inspect. The doubled
// <container>/<container> segment and the pods/<pod>/<pod>.yaml pod placement
// also match oc adm inspect's on-disk layout.
type FilesystemWriter struct {
	root string
}

var _ Writer = (*FilesystemWriter)(nil)

// NewFilesystemWriter returns a writer rooted at root. All paths are created
// under root/namespaces/<ns>/...
func NewFilesystemWriter(root string) *FilesystemWriter {
	return &FilesystemWriter{root: root}
}

func (w *FilesystemWriter) namespaceDir(namespace string) string {
	return filepath.Join(w.root, "namespaces", namespace)
}

// NamespaceOutputPath returns the directory a namespace's content is written to.
func (w *FilesystemWriter) NamespaceOutputPath(namespace string) string {
	return w.namespaceDir(namespace)
}

// ensureWithinRoot rejects a target path that resolves outside the writer's root.
// Namespace/pod/container names ultimately come from user input or telemetry, so
// this is a defense-in-depth guard against path traversal (e.g. a "../" segment)
// escaping the output directory, in addition to the input validation done by the
// caller.
func (w *FilesystemWriter) ensureWithinRoot(path string) error {
	rel, err := filepath.Rel(w.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to write outside output root %q: %s", w.root, path)
	}
	return nil
}

// WriteResources writes the Namespace object to <ns>/<ns>.yaml, each Pod object
// individually to pods/<pod>/<pod>.yaml, and every kind (pods included) into a v1
// List file at <group>/<resource>.yaml, mirroring oc adm inspect.
func (w *FilesystemWriter) WriteResources(_ context.Context, namespace string, resources []Resource) error {
	var errs []error
	byFile := make(map[string][]Resource)
	for _, resource := range resources {
		// The namespace object itself is written at <ns>/<ns>.yaml, not into a list.
		if resource.Kind == "Namespace" && resource.Name == namespace {
			path := filepath.Join(w.namespaceDir(namespace), namespace+".yaml")
			if err := w.writeObjectFile(path, resource.Object); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		// Pods are written individually and also collected into core/pods.yaml.
		if resource.Kind == "Pod" && resource.Name != "" {
			path := filepath.Join(w.namespaceDir(namespace), "pods", resource.Name, resource.Name+".yaml")
			if err := w.writeObjectFile(path, resource.Object); err != nil {
				errs = append(errs, err)
			}
		}
		group := apiGroup(resource.APIVersion)
		fileKey := filepath.Join(group, ResourcePlural(resource.Kind)+".yaml")
		byFile[fileKey] = append(byFile[fileKey], resource)
	}

	for fileKey, group := range byFile {
		sort.Slice(group, func(a, b int) bool { return group[a].Name < group[b].Name })
		if err := w.writeListFile(filepath.Join(w.namespaceDir(namespace), fileKey), group); err != nil {
			errs = append(errs, err)
		}
	}
	return joinErrors(errs)
}

// writeListFile writes the resources as a single v1 List object.
func (w *FilesystemWriter) writeListFile(path string, resources []Resource) error {
	items := make([]any, 0, len(resources))
	for _, resource := range resources {
		items = append(items, resource.Object)
	}
	return w.writeObjectFile(path, map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      items,
	})
}

// writeObjectFile marshals obj to YAML and writes it with a leading "---"
// document separator, creating parent directories as needed.
func (w *FilesystemWriter) writeObjectFile(path string, obj any) error {
	if err := w.ensureWithinRoot(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", path, err)
	}
	data, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal object for %s: %w", path, err)
	}
	if err := os.WriteFile(path, append([]byte("---\n"), data...), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// WriteResourceHistory is a no-op: FilesystemWriter mirrors oc adm inspect's
// point-in-time layout, which has no place for a per-event changelog.
func (w *FilesystemWriter) WriteResourceHistory(_ context.Context, _ []ResourceEvent) error {
	return nil
}

// WriteEvents writes the namespace's events to core/events.yaml as a YAML
// sequence of the projected event fields. Like the resource files it is written
// through writeObjectFile, so it carries the same leading "---" separator.
func (w *FilesystemWriter) WriteEvents(_ context.Context, namespace string, events []map[string]any) error {
	if len(events) == 0 {
		return nil
	}
	path := filepath.Join(w.namespaceDir(namespace), "core", "events.yaml")
	return w.writeObjectFile(path, events)
}

// WriteContainerLog writes one container's log lines to
// pods/<pod>/<container>/<container>/logs/current.log (the doubled container
// segment matches oc adm inspect).
func (w *FilesystemWriter) WriteContainerLog(_ context.Context, namespace, pod, container string, lines []LogLine) error {
	if pod == "" || container == "" || len(lines) == 0 {
		return nil
	}
	path := filepath.Join(w.namespaceDir(namespace), "pods", pod, container, container, "logs", "current.log")
	if err := w.ensureWithinRoot(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", path, err)
	}
	var buf strings.Builder
	for _, line := range lines {
		buf.WriteString(renderLogLine(line.Log))
		buf.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// apiGroup returns the API group from an apiVersion string ("apps/v1" -> "apps",
// "v1" -> "core").
func apiGroup(apiVersion string) string {
	if idx := strings.Index(apiVersion, "/"); idx > 0 {
		return apiVersion[:idx]
	}
	return "core"
}

// renderLogLine renders a container log payload: a plain string is emitted as-is
// (the common case for containerLogs), anything structured is rendered compactly.
func renderLogLine(log any) string {
	switch v := log.(type) {
	case string:
		return strings.TrimRight(v, "\n")
	case nil:
		return ""
	default:
		data, err := yaml.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return strings.TrimRight(string(data), "\n")
	}
}
