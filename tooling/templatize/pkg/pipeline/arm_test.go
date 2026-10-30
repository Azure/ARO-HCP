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

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func TestIsDeploymentActiveError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "unrelated error",
			err:      errors.New("something else"),
			expected: false,
		},
		{
			name: "409 DeploymentActive",
			err: &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "DeploymentActive",
			},
			expected: true,
		},
		{
			name: "409 different error code",
			err: &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "SomethingElse",
			},
			expected: false,
		},
		{
			name: "404 DeploymentNotFound",
			err: &azcore.ResponseError{
				StatusCode: http.StatusNotFound,
				ErrorCode:  "DeploymentNotFound",
			},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isDeploymentActiveError(tt.err))
		})
	}
}

func TestComputeResourceGroupTags(t *testing.T) {
	tests := []struct {
		name         string
		existingTags map[string]*string
		persist      bool
		expectedTags map[string]*string
		description  string
	}{
		// Empty existing tags
		{
			name:         "empty_tags_persist_true",
			existingTags: map[string]*string{},
			persist:      true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
			},
			description: "Empty tags with persist=true should add persist tag",
		},
		{
			name:         "empty_tags_persist_false",
			existingTags: map[string]*string{},
			persist:      false,
			expectedTags: map[string]*string{},
			description:  "Empty tags with persist=false should remain empty",
		},

		// Nil existing tags (edge case)
		{
			name:         "nil_tags_persist_true",
			existingTags: nil,
			persist:      true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
			},
			description: "Nil tags with persist=true should add persist tag",
		},
		{
			name:         "nil_tags_persist_false",
			existingTags: nil,
			persist:      false,
			expectedTags: map[string]*string{},
			description:  "Nil tags with persist=false should result in empty map",
		},

		// Existing tags with persist="true"
		{
			name: "existing_persist_true_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=true with persist=true should preserve persist tag",
		},
		{
			name: "existing_persist_true_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"), // Should be preserved (safety rule)
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=true with persist=false should preserve persist tag (safety rule)",
		},

		// Existing tags with persist="false"
		{
			name: "existing_persist_false_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("false"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=false with persist=true should set persist to true",
		},
		{
			name: "existing_persist_false_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("false"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env": to.Ptr("dev"),
			},
			description: "Existing persist=false with persist=false should not set persist tag",
		},

		// Existing tags with persist="something_else"
		{
			name: "existing_persist_invalid_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("maybe"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=invalid with persist=true should set persist to true",
		},
		{
			name: "existing_persist_invalid_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("maybe"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env": to.Ptr("dev"),
			},
			description: "Existing persist=invalid with persist=false should not set persist tag",
		},

		// Existing tags without persist tag
		{
			name: "no_persist_tag_persist_true",
			existingTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
				"persist": to.Ptr("true"),
			},
			description: "No persist tag with persist=true should add persist tag",
		},
		{
			name: "no_persist_tag_persist_false",
			existingTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			description: "No persist tag with persist=false should not add persist tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeResourceGroupTags(tt.existingTags, tt.persist)
			assert.Equal(t, tt.expectedTags, result, tt.description)
		})
	}
}

type staticARMParamsBuilder struct{}

// BuildParams supplies a fixed compiled lookup template for deployment tests.
func (staticARMParamsBuilder) BuildParams(context.Context, string) (string, string, error) {
	return `{"resources":[],"outputs":{"istioVersions":{"type":"string","value":"[reference('cluster').istioVersions]"}}}`, `{"parameters":{}}`, nil
}

// TestDoWaitForDeploymentCaching exercises fresh lookups and ordinary deployment reuse.
func TestDoWaitForDeploymentCaching(t *testing.T) {
	for _, tt := range []struct {
		name       string
		outputOnly bool
		clearCache bool
	}{
		{name: "output-only refreshes with persistent cache", outputOnly: true},
		{name: "output-only refreshes with ARM deployment history", outputOnly: true, clearCache: true},
		{name: "ordinary deployment reuses persistent cache"},
		{name: "ordinary deployment reuses ARM deployment history", clearCache: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := logr.NewContext(context.Background(), logr.Discard())
			workDir, cacheDir := t.TempDir(), t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(workDir, "lookup.bicepparam"), []byte("using './lookup.bicep'\n"), 0600))

			backendVersions := "asm-1-27"
			deployments := map[string]string{}
			var writes, reads int
			transport := &fakeTransport{interceptor: func(req *http.Request) (*http.Response, error) {
				status := http.StatusOK
				var body string
				switch req.Method {
				case http.MethodGet:
					reads++
					var found bool
					body, found = deployments[req.URL.Path]
					if !found {
						status = http.StatusNotFound
						body = `{"error":{"code":"DeploymentNotFound","message":"Deployment not found"}}`
					}
				case http.MethodPut:
					writes++
					body = fmt.Sprintf(`{"id":%q,"name":%q,"properties":{"provisioningState":"Succeeded","outputs":{"istioVersions":{"type":"String","value":%q}}}}`, req.URL.Path, filepath.Base(req.URL.Path), backendVersions)
					deployments[req.URL.Path] = body
				default:
					return nil, fmt.Errorf("unexpected deployment request: %s %s", req.Method, req.URL)
				}
				return &http.Response{
					StatusCode: status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			}}
			client, err := armresources.NewDeploymentsClient("subscription", &fakeCredential{}, &azcorearm.ClientOptions{
				ClientOptions: azcore.ClientOptions{
					Transport: transport,
					Retry:     policy.RetryOptions{MaxRetries: -1},
				},
			})
			require.NoError(t, err)
			step := &types.ARMStep{
				StepMeta:        types.StepMeta{Name: "lookup", Action: "ARM"},
				Parameters:      "lookup.bicepparam",
				DeploymentLevel: "ResourceGroup",
				OutputOnly:      tt.outputOnly,
			}
			var stamp graph.Stamp
			run := func() *OutPutValue {
				output, _, err := doWaitForDeployment(ctx, staticARMParamsBuilder{}, client, nil, "subscription", "service", stamp, "service-rg", step, workDir, cacheDir, configtypes.Configuration{}, 30, 0, false, NewExecutionState())
				require.NoError(t, err)
				value, err := output.GetValue("istioVersions")
				require.NoError(t, err)
				return value
			}
			first := run()
			require.Equal(t, &OutPutValue{Type: "String", Value: "asm-1-27"}, first)
			require.Equal(t, 1, writes)
			readsAfterFirst := reads

			if tt.clearCache {
				entries, err := os.ReadDir(cacheDir)
				require.NoError(t, err)
				for _, entry := range entries {
					require.NoError(t, os.Remove(filepath.Join(cacheDir, entry.Name())))
				}
			}
			// An existing cluster or canary must no longer receive bootstrap revisions.
			backendVersions = ""
			second := run()
			if tt.outputOnly {
				assert.Equal(t, &OutPutValue{Type: "String", Value: ""}, second)
				assert.Equal(t, 2, writes)
				assert.Len(t, deployments, 2, "fresh lookups must not reuse a content-addressed deployment")
			} else {
				assert.Equal(t, first, second)
				assert.Equal(t, 1, writes)
				if tt.clearCache {
					assert.Greater(t, reads, readsAfterFirst, "ARM deployment history remains reusable")
				} else {
					assert.Equal(t, readsAfterFirst, reads, "persistent cache avoids another ARM request")
				}
			}
		})
	}
}
