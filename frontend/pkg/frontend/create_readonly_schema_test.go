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

package frontend

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const readonlyIdentityID = "/subscriptions/11111111-2222-4333-8444-555555555555/resourceGroups/readonly-test/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test"

// This intentionally supports only the Swagger schema vocabulary used by PUT
// bodies. New structural keywords must fail discovery, not silently lose coverage.
type readonlySchema struct {
	kind, format string
	readOnly     bool
	enum         []any
	properties   map[string]*readonlySchema
	items, extra *readonlySchema
}

type readonlySwagger map[string]map[string]any

func (docs readonlySwagger) load(t testing.TB, file string) map[string]any {
	t.Helper()
	if doc, ok := docs[file]; ok {
		return doc
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	docs[file] = doc
	return doc
}

func (docs readonlySwagger) ref(t testing.TB, file, ref string) (string, map[string]any) {
	t.Helper()
	part, fragment, ok := strings.Cut(ref, "#/")
	if !ok || strings.Contains(part, ":") || filepath.IsAbs(part) {
		t.Fatalf("unsupported Swagger ref %q in %s", ref, file)
	}
	if part != "" {
		file = filepath.Clean(filepath.Join(filepath.Dir(file), part))
	}
	var value any = docs.load(t, file)
	for _, token := range strings.Split(fragment, "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		value = value.(map[string]any)[token]
	}
	obj, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("unresolved Swagger ref %q in %s", ref, file)
	}
	return file, obj
}

func (docs readonlySwagger) schema(t testing.TB, file string, raw map[string]any, depth int) *readonlySchema {
	t.Helper()
	if depth > 80 {
		t.Fatalf("recursive/too deep PUT schema in %s", file)
	}
	s := &readonlySchema{properties: map[string]*readonlySchema{}}
	merge := func(other *readonlySchema) {
		if s.kind != "" && other.kind != "" && s.kind != other.kind {
			t.Fatalf("conflicting allOf types in %s", file)
		}
		if other.kind != "" {
			s.kind = other.kind
		}
		if other.format != "" {
			if s.format != "" && s.format != other.format {
				t.Fatalf("conflicting formats in %s", file)
			}
			s.format = other.format
		}
		s.readOnly = s.readOnly || other.readOnly
		if other.enum != nil {
			if s.enum != nil && !reflect.DeepEqual(s.enum, other.enum) {
				t.Fatalf("conflicting enums in %s", file)
			}
			s.enum = other.enum
		}
		for name, prop := range other.properties {
			if old, ok := s.properties[name]; ok && !reflect.DeepEqual(old, prop) {
				t.Fatalf("unsupported overlapping allOf property %s in %s", name, file)
			}
			s.properties[name] = prop
		}
		if other.items != nil {
			if s.items != nil && !reflect.DeepEqual(s.items, other.items) {
				t.Fatalf("conflicting items in %s", file)
			}
			s.items = other.items
		}
		if other.extra != nil {
			if s.extra != nil && !reflect.DeepEqual(s.extra, other.extra) {
				t.Fatalf("conflicting additionalProperties in %s", file)
			}
			s.extra = other.extra
		}
	}
	if ref, ok := raw["$ref"].(string); ok {
		refFile, target := docs.ref(t, file, ref)
		merge(docs.schema(t, refFile, target, depth+1))
	}
	if all, ok := raw["allOf"].([]any); ok {
		for _, member := range all {
			merge(docs.schema(t, file, member.(map[string]any), depth+1))
		}
	}
	local := &readonlySchema{properties: map[string]*readonlySchema{}}
	for key, value := range raw {
		switch key {
		case "$ref", "allOf":
		case "type":
			local.kind = value.(string)
		case "format":
			local.format = value.(string)
		case "readOnly":
			local.readOnly = value.(bool)
		case "enum":
			local.enum = value.([]any)
		case "properties":
			for name, prop := range value.(map[string]any) {
				local.properties[name] = docs.schema(t, file, prop.(map[string]any), depth+1)
			}
		case "items":
			local.items = docs.schema(t, file, value.(map[string]any), depth+1)
		case "additionalProperties":
			if value == false {
				break
			}
			obj, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("untyped additionalProperties in %s", file)
			}
			local.extra = docs.schema(t, file, obj, depth+1)
		case "description", "title", "required", "default", "pattern", "minLength", "maxLength", "minimum", "maximum", "minItems", "maxItems", "uniqueItems":
		default:
			if !strings.HasPrefix(key, "x-") {
				t.Fatalf("unsupported schema keyword %q in %s", key, file)
			}
		}
	}
	merge(local) // $ref siblings can add readOnly even when the target is writable.
	switch s.kind {
	case "object":
		if len(s.properties) == 0 && s.extra == nil {
			t.Fatalf("untyped object in PUT schema in %s", file)
		}
	case "array":
		if s.items == nil {
			t.Fatalf("array without items in %s", file)
		}
	case "string", "integer", "number", "boolean":
	default:
		t.Fatalf("unsupported schema type %q in %s", s.kind, file)
	}
	return s
}

type readonlyBoundary struct {
	path   []string
	schema *readonlySchema
}

func (s *readonlySchema) boundaries(path []string) []readonlyBoundary {
	if s.readOnly {
		// Seed the whole boundary, including every descendant, rather than
		// keeping an otherwise read-only parent in the writable baseline.
		return []readonlyBoundary{{slices.Clone(path), s}}
	}
	var result []readonlyBoundary
	for _, name := range slices.Sorted(maps.Keys(s.properties)) {
		result = append(result, s.properties[name].boundaries(append(slices.Clone(path), name))...)
	}
	if s.extra != nil {
		result = append(result, s.extra.boundaries(append(slices.Clone(path), readonlyIdentityID))...)
	}
	if s.items != nil {
		result = append(result, s.items.boundaries(append(slices.Clone(path), "0"))...)
	}
	return result
}

func (s *readonlySchema) sample(t testing.TB, entropy uint64) any {
	t.Helper()
	if len(s.enum) > 0 {
		return s.enum[entropy%uint64(len(s.enum))]
	}
	switch s.kind {
	case "object":
		obj := map[string]any{}
		for name, prop := range s.properties {
			obj[name] = prop.sample(t, entropy)
		}
		if s.extra != nil {
			obj[readonlyIdentityID] = s.extra.sample(t, entropy)
		}
		return obj
	case "array":
		return []any{s.items.sample(t, entropy)}
	case "string":
		switch s.format {
		case "":
			return fmt.Sprintf("Injected%x", entropy)
		case "uri":
			return fmt.Sprintf("https://injected-%x.example.com", entropy)
		case "uuid":
			return fmt.Sprintf("11111111-2222-4333-8444-%012x", entropy&0xffffffffffff)
		case "arm-id":
			return readonlyIdentityID
		case "date-time":
			return time.Unix(1700000000+int64(entropy%1000000), 0).UTC().Format(time.RFC3339)
		default:
			t.Fatalf("unsupported injected string format %q", s.format)
		}
	case "integer", "number":
		return float64(1 + entropy%100)
	case "boolean":
		return true
	}
	t.Fatalf("cannot sample %q", s.kind)
	return nil
}

// Only remove read-only boundaries. In particular, keep writable identity map
// keys and their empty object values, and keep writable array elements.
func (s *readonlySchema) writable(t testing.TB, value any) any {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for name, child := range value {
			prop := s.properties[name]
			if prop == nil {
				prop = s.extra
			}
			if prop == nil {
				t.Fatalf("example contains unknown property %q", name)
				return nil
			}
			if !prop.readOnly {
				out[name] = prop.writable(t, child)
			}
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			out[i] = s.items.writable(t, child)
		}
		return out
	default:
		return value
	}
}

func (s *readonlySchema) inject(value any, path []string, injected any) any {
	if len(path) == 0 {
		return injected
	}
	if s.kind == "array" {
		array, _ := value.([]any)
		if len(array) == 0 {
			array = []any{nil}
		}
		array[0] = s.items.inject(array[0], path[1:], injected)
		return array
	}
	obj, _ := value.(map[string]any)
	if obj == nil {
		obj = map[string]any{}
	}
	prop := s.properties[path[0]]
	if prop == nil {
		prop = s.extra
	}
	obj[path[0]] = prop.inject(obj[path[0]], path[1:], injected)
	return obj
}

func TestCreateReadOnlySchema(t *testing.T) {
	// In-memory documents exercise combinations not present in every API version.
	// Resolution and the oracle do not depend on generated Go types or converters.
	docs := readonlySwagger{}
	for file, text := range map[string]string{
		"root.json": `{"definitions":{"Root":{"type":"object","allOf":[{"$ref":"nested/types.json#/definitions/Base"}],"properties":{
			"sibling":{"$ref":"nested/types.json#/definitions/Writable","readOnly":true},
			"definition":{"$ref":"nested/types.json#/definitions/ReadOnly","readOnly":false},
			"map":{"type":"object","additionalProperties":{"$ref":"nested/types.json#/definitions/Base"}},
			"array":{"type":"array","items":{"$ref":"nested/types.json#/definitions/Base"}}
		}}}}`,
		"nested/types.json": `{"definitions":{
			"Writable":{"type":"string"},
			"ReadOnly":{"type":"string","readOnly":true},
			"Base":{"type":"object","properties":{"output":{"$ref":"#/definitions/ReadOnly"},"input":{"type":"string"}}}
		}}`,
	} {
		var doc map[string]any
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			t.Fatal(err)
		}
		docs[file] = doc
	}
	file, raw := docs.ref(t, "root.json", "#/definitions/Root")
	schema := docs.schema(t, file, raw, 0)
	var paths []string
	for _, boundary := range schema.boundaries(nil) {
		paths = append(paths, strings.Join(boundary.path, "."))
	}
	wantPaths := []string{"array.0.output", "definition", "map." + readonlyIdentityID + ".output", "output", "sibling"}
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("discovered boundaries %v, want %v", paths, wantPaths)
	}
	input := map[string]any{
		"input": "keep", "output": "remove", "definition": "remove", "sibling": "remove",
		"map":   map[string]any{"keep-key": map[string]any{"output": "remove"}},
		"array": []any{map[string]any{"input": "keep", "output": "remove"}},
	}
	want := map[string]any{
		"input": "keep",
		"map":   map[string]any{"keep-key": map[string]any{}},
		"array": []any{map[string]any{"input": "keep"}},
	}
	if got := schema.writable(t, input); !reflect.DeepEqual(got, want) {
		t.Fatalf("writable containers/map keys changed: got %#v, want %#v", got, want)
	}
}
