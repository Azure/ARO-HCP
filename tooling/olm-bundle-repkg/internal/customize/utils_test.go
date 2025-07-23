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

package customize

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParameterizeImageComponents(t *testing.T) {
	testCases := []struct {
		name           string
		imageRef       string
		config         *BundleConfig
		suffix         string
		expectedImg    string
		expectedParams map[string]string
	}{
		// Core functionality tests
		{
			name:     "all parameters configured",
			imageRef: "registry.io/myrepo/myimage:v1.0.0",
			config: &BundleConfig{
				ImageRegistryParam:   "imageRegistry",
				ImageRepositoryParam: "imageRepository",
				ImageTagParam:        "imageTag",
			},
			expectedImg: "{{ .Values.imageRegistry }}/{{ .Values.imageRepository }}:{{ .Values.imageTag }}",
			expectedParams: map[string]string{
				"imageRegistry":   "",
				"imageRepository": "",
				"imageTag":        "",
			},
		},
		{
			name:           "no parameterization configured",
			imageRef:       "registry.io/myrepo/myimage:v1.0.0",
			config:         &BundleConfig{},
			expectedImg:    "registry.io/myrepo/myimage:v1.0.0",
			expectedParams: map[string]string{},
		},
		{
			name:     "only registry configured",
			imageRef: "registry.io/myrepo/myimage:v1.0.0",
			config: &BundleConfig{
				ImageRegistryParam: "imageRegistry",
			},
			expectedImg: "{{ .Values.imageRegistry }}/myrepo/myimage:v1.0.0",
			expectedParams: map[string]string{
				"imageRegistry": "",
			},
		},
		{
			name:     "only repository configured",
			imageRef: "registry.io/myrepo/myimage:v1.0.0",
			config: &BundleConfig{
				ImageRepositoryParam: "imageRepository",
			},
			expectedImg: "registry.io/{{ .Values.imageRepository }}:v1.0.0",
			expectedParams: map[string]string{
				"imageRepository": "",
			},
		},
		{
			name:     "only tag configured",
			imageRef: "registry.io/myrepo/myimage:v1.0.0",
			config: &BundleConfig{
				ImageTagParam: "imageTag",
			},
			expectedImg: "registry.io/myrepo/myimage:{{ .Values.imageTag }}",
			expectedParams: map[string]string{
				"imageTag": "",
			},
		},
		{
			name:     "only digest configured",
			imageRef: "registry.io/repo/image@sha256:abc123def456",
			config: &BundleConfig{
				ImageDigestParam: "imageDigest",
			},
			expectedImg: "registry.io/repo/image@sha256:{{ .Values.imageDigest }}",
			expectedParams: map[string]string{
				"imageDigest": "",
			},
		},
		{
			name:     "registry and repository configured",
			imageRef: "registry.io/myrepo/myimage:v1.0.0",
			config: &BundleConfig{
				ImageRegistryParam:   "imageRegistry",
				ImageRepositoryParam: "imageRepository",
			},
			expectedImg: "{{ .Values.imageRegistry }}/{{ .Values.imageRepository }}:v1.0.0",
			expectedParams: map[string]string{
				"imageRegistry":   "",
				"imageRepository": "",
			},
		},

		// Format conversion tests
		{
			name:     "convert tag to digest format",
			imageRef: "registry.io/repo/image:v1.0.0",
			config: &BundleConfig{
				ImageDigestParam: "imageDigest",
			},
			expectedImg: "registry.io/repo/image@sha256:{{ .Values.imageDigest }}",
			expectedParams: map[string]string{
				"imageDigest": "",
			},
		},
		{
			name:     "convert digest to tag format",
			imageRef: "registry.io/repo/image@sha256:abc123def456",
			config: &BundleConfig{
				ImageTagParam: "imageTag",
			},
			expectedImg: "registry.io/repo/image:{{ .Values.imageTag }}",
			expectedParams: map[string]string{
				"imageTag": "",
			},
		},

		// Edge cases and special formats
		{
			name:     "registry with port",
			imageRef: "localhost:5000/repo/image:tag",
			config: &BundleConfig{
				ImageRegistryParam: "imageRegistry",
			},
			expectedImg: "{{ .Values.imageRegistry }}/repo/image:tag",
			expectedParams: map[string]string{
				"imageRegistry": "",
			},
		},
		{
			name:     "image without tag - add tag param",
			imageRef: "registry.io/repo/image",
			config: &BundleConfig{
				ImageTagParam: "imageTag",
			},
			expectedImg: "registry.io/repo/image:{{ .Values.imageTag }}",
			expectedParams: map[string]string{
				"imageTag": "",
			},
		},
		{
			name:     "all parameters with suffix",
			imageRef: "registry.io/myrepo/myimage:v1.0.0",
			config: &BundleConfig{
				ImageRegistryParam:   "imageRegistry",
				ImageRepositoryParam: "imageRepository",
				ImageTagParam:        "imageTag",
			},
			suffix:      "Manager",
			expectedImg: "{{ .Values.imageRegistryManager }}/{{ .Values.imageRepositoryManager }}:{{ .Values.imageTagManager }}",
			expectedParams: map[string]string{
				"imageRegistryManager":   "",
				"imageRepositoryManager": "",
				"imageTagManager":        "",
			},
		},
		{
			name:     "only registry with suffix",
			imageRef: "registry.io/myrepo/myimage:v1.0.0",
			config: &BundleConfig{
				ImageRegistryParam: "imageRegistry",
			},
			suffix:      "Controller",
			expectedImg: "{{ .Values.imageRegistryController }}/myrepo/myimage:v1.0.0",
			expectedParams: map[string]string{
				"imageRegistryController": "",
			},
		},
		{
			name:     "digest with suffix",
			imageRef: "registry.io/repo/image@sha256:abc123def456",
			config: &BundleConfig{
				ImageDigestParam: "imageDigest",
			},
			suffix:      "Worker",
			expectedImg: "registry.io/repo/image@sha256:{{ .Values.imageDigestWorker }}",
			expectedParams: map[string]string{
				"imageDigestWorker": "",
			},
		},
		{
			name:     "empty suffix behaves like normal",
			imageRef: "registry.io/myrepo/myimage:v1.0.0",
			config: &BundleConfig{
				ImageRegistryParam: "imageRegistry",
			},
			suffix:      "",
			expectedImg: "{{ .Values.imageRegistry }}/myrepo/myimage:v1.0.0",
			expectedParams: map[string]string{
				"imageRegistry": "",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, params := parameterizeImageComponents(tc.imageRef, tc.config, tc.suffix)
			assert.Equal(t, tc.expectedImg, result)
			assert.Equal(t, tc.expectedParams, params)
		})
	}
}
