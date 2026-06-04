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

package databasemutationhelpers

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// updateMode reports whether the UPDATE=true env var is set, in which case
// cosmosCompare/loadCosmos overwrite their testdata fixtures rather than
// asserting against them.
func updateMode() bool {
	return os.Getenv("UPDATE") == "true"
}

// roundtripCosmosBytes does CosmosToInternal+InternalToCosmos on a single
// document's raw JSON, dispatching on its resourceType to the right concrete
// Cosmos shape (HCPCluster, NodePool, ExternalAuth) or GenericDocument[T] for
// everything else. The result is re-marshaled with indentation, matching the
// existing fixture format.
func roundtripCosmosBytes(content []byte) ([]byte, error) {
	var typed database.TypedDocument
	if err := json.Unmarshal(content, &typed); err != nil {
		return nil, fmt.Errorf("failed to parse TypedDocument: %w", err)
	}
	rt := strings.ToLower(typed.ResourceType)
	switch rt {
	case strings.ToLower(api.ClusterResourceType.String()):
		return roundtripGeneric[api.HCPOpenShiftCluster](content)
	case strings.ToLower(api.NodePoolResourceType.String()):
		return roundtripGeneric[api.HCPOpenShiftClusterNodePool](content)
	case strings.ToLower(api.ExternalAuthResourceType.String()):
		return roundtripGeneric[api.HCPOpenShiftClusterExternalAuth](content)
	case strings.ToLower(api.ServiceProviderClusterResourceType.String()):
		return roundtripGeneric[api.ServiceProviderCluster](content)
	case strings.ToLower(api.ServiceProviderNodePoolResourceType.String()):
		return roundtripGeneric[api.ServiceProviderNodePool](content)
	case strings.ToLower(api.OperationStatusResourceType.String()):
		return roundtripGeneric[api.Operation](content)
	case strings.ToLower(azcorearm.SubscriptionResourceType.String()):
		return roundtripGeneric[arm.Subscription](content)
	case strings.ToLower(api.ClusterControllerResourceType.String()),
		strings.ToLower(api.NodePoolControllerResourceType.String()),
		strings.ToLower(api.ExternalAuthControllerResourceType.String()):
		return roundtripGeneric[api.Controller](content)
	case strings.ToLower(api.ClusterScopedManagementClusterContentResourceType.String()),
		strings.ToLower(api.NodePoolScopedManagementClusterContentResourceType.String()):
		return roundtripGeneric[api.ManagementClusterContent](content)
	default:
		return nil, fmt.Errorf("UPDATE roundtrip: unsupported resourceType %q", typed.ResourceType)
	}
}

func roundtripGeneric[InternalAPIType any](content []byte) ([]byte, error) {
	var cosmosObj database.GenericDocument[InternalAPIType]
	if err := json.Unmarshal(content, &cosmosObj); err != nil {
		return nil, fmt.Errorf("unmarshal GenericDocument: %w", err)
	}
	internal, err := database.CosmosGenericToInternal[InternalAPIType](&cosmosObj)
	if err != nil {
		return nil, fmt.Errorf("CosmosGenericToInternal: %w", err)
	}
	out, err := database.InternalToCosmosGeneric[InternalAPIType](internal)
	if err != nil {
		return nil, fmt.Errorf("InternalToCosmosGeneric: %w", err)
	}
	return marshalCanonical(out)
}

// resolveStepUpdateDir locates the on-disk fixture directory for a step under
// the test's package source dir. `go test` sets the working directory to the
// test package, which in a GOPATH-style layout is $GOPATH/src/<importpath>;
// we walk down from there for any directory named stepSubdir, then narrow to
// the one whose path tail matches the longest possible suffix of t.Name()'s
// slash-separated components. Trimming the prefix lets us shrug off framework
// wrappers ("TestX", "WithMock") and nested table cases keep their full
// hierarchy ("Cluster/read-old-data" stays distinct from
// "ExternalAuth/read-old-data"). Returns "" with a log when zero candidates
// or genuinely ambiguous candidates remain.
func resolveStepUpdateDir(t *testing.T, stepID StepID) string {
	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("UPDATE: could not get working directory: %v", err)
		return ""
	}

	// basic_controller writes its step subdirs as a two-digit prefix
	// (e.g. "00-load-initial-state", "99-cosmosCompare-end-state"); match that
	// exactly so we don't accidentally write to a step dir for a different index.
	stepSubdir := fmt.Sprintf("%02d-%s-%s", stepID.index, stepID.stepType, stepID.stepName)

	var candidates []string
	_ = filepath.WalkDir(packageDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if d.Name() == stepSubdir {
			candidates = append(candidates, p)
		}
		return nil
	})
	if len(candidates) == 0 {
		t.Fatalf("UPDATE: no on-disk fixture dir %q anywhere under %s", stepSubdir, packageDir)
		return ""
	}

	nameParts := strings.Split(t.Name(), "/")
	for trimFront := 0; trimFront < len(nameParts); trimFront++ {
		suffix := nameParts[trimFront:]
		filtered := candidatesEndingWith(candidates, suffix)
		if len(filtered) == 0 {
			continue
		}
		if len(filtered) == 1 {
			return filtered[0]
		}
		// Most-specific suffix still hit several dirs — there is no longer
		// suffix that would narrow further (any longer suffix yielded zero),
		// so the test layout is genuinely ambiguous for this step.
		t.Fatalf("UPDATE: multiple on-disk fixture dirs match suffix %v / %q, refusing to write: %v", suffix, stepSubdir, filtered)
		return ""
	}

	t.Fatalf("UPDATE: no on-disk fixture dir under %s ends with any suffix of %v / %q", packageDir, nameParts, stepSubdir)
	return ""
}

// candidatesEndingWith returns the subset of candidate paths whose immediate
// ancestor components (excluding stepSubdir itself) end with suffix.
func candidatesEndingWith(candidates, suffix []string) []string {
	if len(suffix) == 0 {
		return nil
	}
	var out []string
	for _, p := range candidates {
		parent := filepath.Dir(p)
		ancestors := strings.Split(parent, string(filepath.Separator))
		if len(ancestors) < len(suffix) {
			continue
		}
		tail := ancestors[len(ancestors)-len(suffix):]
		match := true
		for i, want := range suffix {
			if tail[i] != want {
				match = false
				break
			}
		}
		if match {
			out = append(out, p)
		}
	}
	return out
}

// verifyOrUpdateList is the shared body of every list-style verification step:
// match expected fixtures to actuals by name (resourceID), diff only the
// matched pair, and report extras on either side. Under UPDATE=true it instead
// rewrites the fixture directory so it matches the actual result set.
//
// nameOf turns a list element into the identifying string used for matching;
// callers using ARM types pass ResourceName, callers handling raw JSON maps
// (httpList) pass a custom extractor.
//
// expected and expectedFilenames stay index-aligned (filenames produced by
// readResourcesAndFilenamesInDir). actual is whatever the step produced from
// the system under test.
func verifyOrUpdateList(t *testing.T, stepID StepID, expected []any, expectedFilenames []string, actual []any, nameOf func(any) string) {
	t.Helper()
	if updateMode() {
		updateListFixtures(t, stepID, expected, expectedFilenames, actual, nameOf)
		return
	}

	actualByName := map[string]any{}
	for _, a := range actual {
		name := strings.ToLower(nameOf(a))
		if name == "" {
			t.Errorf("actual resource has no resourceID; cannot match:\n%v", stringifyResource(a))
			continue
		}
		actualByName[name] = a
	}
	expectedByName := map[string]struct{}{}
	for _, e := range expected {
		name := strings.ToLower(nameOf(e))
		if name == "" {
			t.Errorf("expected resource has no resourceID; cannot match:\n%v", stringifyResource(e))
			continue
		}
		expectedByName[name] = struct{}{}
	}

	for _, e := range expected {
		name := strings.ToLower(nameOf(e))
		if name == "" {
			continue // already reported above
		}
		a, ok := actualByName[name]
		if !ok {
			t.Errorf("expected resource %s not present in actual:\n%v", nameOf(e), stringifyResource(e))
			continue
		}
		diff, equals := ResourceInstanceEquals(t, e, a)
		if !equals {
			t.Log(diff)
			t.Errorf("resource %s did not match expected", nameOf(e))
		}
	}

	for _, a := range actual {
		name := strings.ToLower(nameOf(a))
		if name == "" {
			continue
		}
		if _, ok := expectedByName[name]; !ok {
			t.Errorf("unexpected resource %s in actual:\n%v", nameOf(a), stringifyResource(a))
		}
	}
}

func updateListFixtures(t *testing.T, stepID StepID, expected []any, expectedFilenames []string, actual []any, nameOf func(any) string) {
	t.Helper()
	updateDir := resolveStepUpdateDir(t, stepID)
	if updateDir == "" {
		return // resolveStepUpdateDir already t.Fatalf'd
	}

	actualByName := map[string]any{}
	for _, a := range actual {
		name := strings.ToLower(nameOf(a))
		if name == "" {
			t.Fatalf("UPDATE: actual resource has no resourceID; cannot place fixture:\n%v", stringifyResource(a))
		}
		actualByName[name] = a
	}

	usedNames := map[string]struct{}{}
	for i, e := range expected {
		var filename string
		if i < len(expectedFilenames) {
			filename = expectedFilenames[i]
		}
		if filename == "" {
			continue
		}
		fullPath := filepath.Join(updateDir, filename)
		name := strings.ToLower(nameOf(e))
		a, ok := actualByName[name]
		if !ok {
			t.Logf("UPDATE: removing fixture not present in actual: %s", fullPath)
			if err := os.Remove(fullPath); err != nil {
				t.Fatalf("UPDATE: failed to remove %s: %v", fullPath, err)
			}
			continue
		}
		usedNames[name] = struct{}{}
		// Skip the rewrite when the comparator already considers them equal
		// — only stripped fields differ, so the on-disk fixture is fine.
		if _, equals := ResourceInstanceEquals(t, e, a); equals {
			continue
		}
		out, err := marshalCanonical(a)
		if err != nil {
			t.Fatalf("UPDATE: marshal actual for %s: %v", fullPath, err)
		}
		if err := os.WriteFile(fullPath, append(out, '\n'), 0644); err != nil {
			t.Fatalf("UPDATE: write %s: %v", fullPath, err)
		}
	}

	for name, a := range actualByName {
		if _, used := usedNames[name]; used {
			continue
		}
		filename := sanitizeForFilename(nameOf(a)) + ".json"
		fullPath := filepath.Join(updateDir, filename)
		out, err := marshalCanonical(a)
		if err != nil {
			t.Fatalf("UPDATE: marshal new actual for %s: %v", fullPath, err)
		}
		t.Logf("UPDATE: creating fixture for %s: %s", nameOf(a), fullPath)
		if err := os.WriteFile(fullPath, append(out, '\n'), 0644); err != nil {
			t.Fatalf("UPDATE: write %s: %v", fullPath, err)
		}
	}
}

// typedDocumentKey returns the matching key for a *database.TypedDocument. For
// most resource types this is just the resourceID, but operation-status
// documents get a UUID-generated resourceID that varies per run (and is
// stripped by the comparator), so for those we key on
// (externalId, request, status) from the inner Operation properties — a triple
// that's stable across runs and distinguishes the multiple operations a single
// resource may accumulate (e.g. an Accepted Create and the later Succeeded
// Create for the same node pool).
func typedDocumentKey(v any) string {
	td, ok := v.(*database.TypedDocument)
	if !ok || td == nil || td.ResourceID == nil {
		return ""
	}
	if strings.EqualFold(td.ResourceType, api.OperationStatusResourceType.String()) {
		var props struct {
			Request    string `json:"request"`
			ExternalID string `json:"externalId"`
			Status     string `json:"status"`
		}
		if err := json.Unmarshal(td.Properties, &props); err == nil && props.ExternalID != "" {
			return strings.ToLower(props.ExternalID) + "|" + props.Request + "|" + props.Status
		}
	}
	return td.ResourceID.String()
}

// verifyOrUpdateGet is the shared body of every get-style verification step
// (typed get, getByID, untypedGet, httpGet). In normal mode it does the usual
// expected-vs-actual comparison; under UPDATE=true it rewrites the single
// on-disk fixture with the actual payload. expectedFilename is the file the
// expected resource was loaded from (empty when the step's only assertion is
// an expected error, in which case UPDATE is a no-op).
//
// UPDATE skips the file write when ResourceInstanceEquals already considers
// expected and actual equal — i.e. the only differences are fields the
// comparator strips (etags, generated UUIDs, timestamps, …). That avoids
// rewriting fixtures on every run for cosmetic churn.
func verifyOrUpdateGet(t *testing.T, stepID StepID, expected, actual any, expectedFilename string) {
	t.Helper()
	if updateMode() {
		if expectedFilename == "" {
			// Error-only steps have no fixture to rewrite, and updating
			// expected-error.txt automatically would mask real regressions.
			return
		}
		if _, equals := ResourceInstanceEquals(t, expected, actual); equals {
			return
		}
		updateDir := resolveStepUpdateDir(t, stepID)
		if updateDir == "" {
			return // resolveStepUpdateDir already t.Fatalf'd
		}
		fullPath := filepath.Join(updateDir, expectedFilename)
		out, err := marshalCanonical(actual)
		if err != nil {
			t.Fatalf("UPDATE: marshal actual for %s: %v", fullPath, err)
		}
		if err := os.WriteFile(fullPath, append(out, '\n'), 0644); err != nil {
			t.Fatalf("UPDATE: write %s: %v", fullPath, err)
		}
		return
	}

	if reason, equals := ResourceInstanceEquals(t, expected, actual); !equals {
		t.Logf("actual:\n%v", stringifyResource(actual))
		t.Log(reason)
		// cmpdiff doesn't handle private fields gracefully
		require.Equal(t, expected, actual)
	}
}

// resourceIDFromMap pulls the ARM "id" field out of a map[string]any (or its
// pointer). httpList responses come back as untyped JSON maps and don't satisfy
// arm.CosmosMetadataAccessor, so the list helper uses this to key by id.
func resourceIDFromMap(v any) string {
	var m map[string]any
	switch cast := v.(type) {
	case map[string]any:
		m = cast
	case *map[string]any:
		if cast == nil {
			return ""
		}
		m = *cast
	default:
		return ""
	}
	for _, k := range []string{"id", "ID", "resourceID", "resourceId"} {
		if s, ok := m[k].(string); ok && len(s) > 0 {
			return s
		}
	}
	return ""
}

// toAnySlice converts a typed pointer slice ([]*T) into []any so the
// list-verification helper can consume any list step's expected/actual without
// generic gymnastics at each call site.
func toAnySlice[T any](in []*T) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}

// marshalCanonical produces tab-indented JSON with alphabetically sorted keys,
// matching what `hack/update-json-format.sh` (jq --tab with sort_keys) applies
// to every tracked .json fixture. We Marshal the typed value to bytes, parse
// it into a generic any (which drops Go struct field order), then MarshalIndent
// — Go's encoder sorts map keys alphabetically, so the output matches jq.
func marshalCanonical(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("first marshal: %w", err)
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, fmt.Errorf("unmarshal into generic: %w", err)
	}
	return json.MarshalIndent(generic, "", "\t")
}

// sanitizeForFilename converts a resourceID into a filesystem-safe identifier.
// The result is lowercased, slashes become dashes, and anything outside the
// usual safe set collapses to a single underscore. Used to name newly-created
// fixture files in UPDATE mode when an actual document has no existing file.
func sanitizeForFilename(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, "/")
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
			prevUnderscore = false
		case r == '/':
			b.WriteRune('-')
			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteRune('_')
				prevUnderscore = true
			}
		}
	}
	return b.String()
}
