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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type readonlyCreateCase struct {
	version    coreapi.Version
	operation  string
	resourceID *azcorearm.ResourceID
	schema     *readonlySchema
	body       any
	boundaries []readonlyBoundary
}

func readonlyCreateCases(t testing.TB) []readonlyCreateCase {
	t.Helper()
	reg := prometheus.NewRegistry()
	frontend := NewFrontend(logr.Discard(), nil, nil, reg, reg, nil, nil, nil, "eastus", true, azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev))
	docs := readonlySwagger{}
	var cases []readonlyCreateCase
	for _, versionName := range slices.Sorted(maps.Keys(frontend.apiRegistry.ListVersions())) {
		version, ok := frontend.apiRegistry.Lookup(versionName)
		if !ok {
			t.Fatal(versionName)
		}
		file := filepath.Join("../../../api/redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters/preview", versionName, "openapi.json")
		doc := docs.load(t, file)
		if doc["swagger"] != "2.0" || doc["info"].(map[string]any)["version"] != versionName {
			t.Fatalf("wrong Swagger/version in %s", file)
		}
		paths := doc["paths"].(map[string]any)
		count := 0
		for _, path := range slices.Sorted(maps.Keys(paths)) {
			put, ok := paths[path].(map[string]any)["put"].(map[string]any)
			if !ok {
				continue
			}
			operation := put["operationId"].(string)
			id := "/subscriptions/11111111-2222-4333-8444-555555555555/resourceGroups/readonly-test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"
			switch operation {
			case "HcpOpenShiftClusters_CreateOrUpdate":
			case "NodePools_CreateOrUpdate":
				id += "/nodePools/node"
			case "ExternalAuths_CreateOrUpdate":
				id += "/externalAuths/auth"
			default:
				t.Fatalf("uncovered PUT operation %s", operation)
			}
			resourceID, err := azcorearm.ParseResourceID(id)
			if err != nil {
				t.Fatal(err)
			}
			var schema *readonlySchema
			for _, param := range put["parameters"].([]any) {
				parameter := param.(map[string]any)
				paramFile := file
				if ref, ok := parameter["$ref"].(string); ok {
					paramFile, parameter = docs.ref(t, file, ref)
				}
				if parameter["in"] == "body" {
					if schema != nil {
						t.Fatalf("multiple PUT bodies in %s", operation)
					}
					schema = docs.schema(t, paramFile, parameter["schema"].(map[string]any), 0)
				}
			}
			if schema == nil {
				t.Fatalf("no PUT body for %s", operation)
			}
			example := docs.load(t, filepath.Join(filepath.Dir(file), "examples", operation+"_MaximumSet_Gen.json"))
			body := schema.writable(t, example["parameters"].(map[string]any)["resource"])
			if operation == "HcpOpenShiftClusters_CreateOrUpdate" {
				// Avoid random managed-resource-group defaults without changing global RNGs.
				schema.inject(body, []string{"properties", "platform", "managedResourceGroup"}, "readonly-test-managed")
			}
			boundaries := schema.boundaries(nil)
			if len(boundaries) == 0 {
				t.Fatalf("no readOnly boundaries for %s/%s", versionName, operation)
			}
			cases = append(cases, readonlyCreateCase{version, operation, resourceID, schema, body, boundaries})
			count++
		}
		if count != 3 {
			t.Fatalf("%s: discovered %d PUT kinds, want 3", versionName, count)
		}
	}
	if len(cases) == 0 {
		t.Fatal("no registered API versions")
	}
	return cases
}

func (c readonlyCreateCase) decode(t testing.TB, body any) (any, error) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ctx := utils.ContextWithResourceID(context.Background(), c.resourceID)
	ctx = ContextWithVersion(ctx, c.version)
	ctx = ContextWithBody(ctx, b)
	created, modified := time.Unix(1600000000, 0).UTC(), time.Unix(1600000001, 0).UTC()
	ctx = ContextWithSystemData(ctx, &coreapi.SystemData{CreatedAt: &created, LastModifiedAt: &modified})
	switch c.operation {
	case "HcpOpenShiftClusters_CreateOrUpdate":
		return decodeDesiredClusterCreate(ctx, "eastus", http.Header{coreapi.HeaderNameIdentityURL: []string{"https://identity.example.com"}})
	case "NodePools_CreateOrUpdate":
		return decodeDesiredNodePoolCreate(ctx, "eastus")
	case "ExternalAuths_CreateOrUpdate":
		return decodeDesiredExternalAuthCreate(ctx)
	default:
		t.Fatalf("no decoder for %s", c.operation)
		return nil, nil
	}
}

func FuzzCreateReadOnly(f *testing.F) {
	cases := readonlyCreateCases(f)
	type seed struct {
		resource readonlyCreateCase
		boundary readonlyBoundary
	}
	var seeds []seed
	for _, c := range cases {
		for _, boundary := range c.boundaries {
			seeds = append(seeds, seed{c, boundary})
			// One deterministic seed per discovered boundary, never a sampled subset.
			f.Add(uint64(len(seeds)-1), uint64(1))
		}
	}
	f.Logf("discovered %d readOnly boundaries across %d version/resource pairs", len(seeds), len(cases))
	boundaryCount := len(seeds)
	for _, c := range cases {
		seeds = append(seeds, seed{resource: c})
		f.Add(uint64(len(seeds)-1), uint64(1))
	}
	f.Fuzz(func(t *testing.T, selector, entropy uint64) {
		seed := seeds[selector%uint64(len(seeds))]
		c, boundary := seed.resource, seed.boundary
		label := fmt.Sprintf("%s/%s/%s", c.version.String(), c.operation, strings.Join(boundary.path, "."))
		boundaries := []readonlyBoundary{boundary}
		if selector%uint64(len(seeds)) >= uint64(boundaryCount) {
			boundaries = c.boundaries
		}
		injected := c.schema.writable(t, c.body)
		for _, boundary := range boundaries {
			value := boundary.schema.sample(t, entropy)
			if entropy%4 == 0 {
				value = nil
			}
			if slices.Equal(boundary.path, []string{"name"}) {
				// ARM allows an echoed name only when it matches the request path.
				value = c.resourceID.Name
			}
			injected = c.schema.inject(injected, boundary.path, value)
		}
		baseline := c.schema.writable(t, injected)
		want, err := c.decode(t, baseline)
		if err != nil {
			t.Fatalf("%s: writable baseline did not decode: %v", label, err)
		}
		got, err := c.decode(t, injected)
		if err != nil {
			t.Fatalf("%s: typed readOnly injection did not decode: %v", label, err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: readOnly input changed desired create result (-baseline +injected):\n%s", label, cmp.Diff(want, got, coreapi.CmpDiffOptions...))
		}
	})
}

func TestCreateReadOnlyInvalidBodyID(t *testing.T) {
	for _, c := range readonlyCreateCases(t) {
		t.Run(c.version.String()+"/"+c.operation, func(t *testing.T) {
			baseline, err := c.decode(t, c.body)
			if err != nil {
				t.Fatal(err)
			}
			body := c.schema.inject(c.schema.writable(t, c.body), []string{"id"}, "not-an-arm-resource-id")
			got, err := c.decode(t, body)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(baseline, got) {
				t.Fatal("body ID changed desired create result")
			}
		})
	}
}

func TestCreateReadOnlyNameMismatch(t *testing.T) {
	for _, c := range readonlyCreateCases(t) {
		t.Run(c.version.String()+"/"+c.operation, func(t *testing.T) {
			body := c.schema.inject(c.schema.writable(t, c.body), []string{"name"}, "different-name")
			_, err := c.decode(t, body)
			var cloudError *coreapi.CloudError
			if !errors.As(err, &cloudError) || cloudError.StatusCode != http.StatusBadRequest || cloudError.Code != coreapi.CloudErrorCodeInvalidRequestContent || cloudError.Target != "name" {
				t.Fatalf("expected name mismatch 400, got %v", err)
			}
		})
	}
}

func TestCreateReadOnlyBaseline(t *testing.T) {
	for _, c := range readonlyCreateCases(t) {
		t.Run(c.version.String()+"/"+c.operation, func(t *testing.T) {
			first, err := c.decode(t, c.body)
			if err != nil {
				t.Fatal(err)
			}
			second, err := c.decode(t, c.body)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("baseline is nondeterministic: %s", cmp.Diff(first, second, coreapi.CmpDiffOptions...))
			}
		})
	}
}

func TestCreateReadOnlyWrongType(t *testing.T) {
	for _, c := range readonlyCreateCases(t) {
		for _, boundary := range c.boundaries {
			t.Run(c.version.String()+"/"+c.operation+"/"+strings.Join(boundary.path, "."), func(t *testing.T) {
				var wrong any = []any{"wrong-type"}
				if boundary.schema.kind == "array" {
					wrong = map[string]any{"wrong": "type"}
				}
				body := c.schema.inject(c.schema.writable(t, c.body), boundary.path, wrong)
				_, err := c.decode(t, body)
				var cloudError *coreapi.CloudError
				if !errors.As(err, &cloudError) || cloudError.StatusCode != http.StatusBadRequest || cloudError.Code != coreapi.CloudErrorCodeInvalidRequestContent {
					t.Fatalf("expected invalid-content 400 for wrong JSON type, got %v", err)
				}
			})
		}
	}
}
