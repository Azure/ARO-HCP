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
		expectedImg    string
		expectedParams map[string]string
	}{
		{
			name:     "all parameters configured",
			imageRef: "registry.io/myrepo/myimage:v1.0.0",
			config: &BundleConfig{
				ImageRegistryParam:   "imageRegistry",
				ImageRepositoryParam: "imageRepository",
				ImageNameParam:       "imageName",
				ImageTagParam:        "imageTag",
			},
			expectedImg: "{{ .Values.imageRegistry }}/{{ .Values.imageRepository }}/{{ .Values.imageName }}:{{ .Values.imageTag }}",
			expectedParams: map[string]string{
				"imageRegistry":   "",
				"imageRepository": "",
				"imageName":       "",
				"imageTag":        "",
			},
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
			name:     "only image name configured",
			imageRef: "registry.io/myrepo/myimage:v1.0.0",
			config: &BundleConfig{
				ImageNameParam: "imageName",
			},
			expectedImg: "registry.io/myrepo/{{ .Values.imageName }}:v1.0.0",
			expectedParams: map[string]string{
				"imageName": "",
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
			name:     "registry and image name configured",
			imageRef: "registry.io/myrepo/myimage:v1.0.0",
			config: &BundleConfig{
				ImageRegistryParam: "imageRegistry",
				ImageNameParam:     "imageName",
			},
			expectedImg: "{{ .Values.imageRegistry }}/myrepo/{{ .Values.imageName }}:v1.0.0",
			expectedParams: map[string]string{
				"imageRegistry": "",
				"imageName":     "",
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
			name:     "basic repository parameterization",
			imageRef: "registry.io/myrepo/image:tag",
			config: &BundleConfig{
				ImageRepositoryParam: "imageRepository",
			},
			expectedImg: "registry.io/{{ .Values.imageRepository }}/image:tag",
			expectedParams: map[string]string{
				"imageRepository": "",
			},
		},
		{
			name:     "nested repository path",
			imageRef: "registry.io/org/team/image:tag",
			config: &BundleConfig{
				ImageRepositoryParam: "imageRepository",
			},
			expectedImg: "registry.io/{{ .Values.imageRepository }}/image:tag",
			expectedParams: map[string]string{
				"imageRepository": "",
			},
		},
		{
			name:     "two-part image - no repository to parameterize",
			imageRef: "registry.io/image:tag",
			config: &BundleConfig{
				ImageRepositoryParam: "imageRepository",
			},
			expectedImg:    "registry.io/image:tag", // No change - no repository part
			expectedParams: map[string]string{},     // No params added since no repository found
		},
		{
			name:     "image name without tag",
			imageRef: "registry.io/repo/myimage",
			config: &BundleConfig{
				ImageNameParam: "imageName",
			},
			expectedImg: "registry.io/repo/{{ .Values.imageName }}",
			expectedParams: map[string]string{
				"imageName": "",
			},
		},
		{
			name:     "image name with nested repository path",
			imageRef: "registry.io/org/team/myimage:v1.0.0",
			config: &BundleConfig{
				ImageNameParam: "imageName",
			},
			expectedImg: "registry.io/org/team/{{ .Values.imageName }}:v1.0.0",
			expectedParams: map[string]string{
				"imageName": "",
			},
		},
		{
			name:     "tag parameterization - image without original tag",
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
			name:     "tag parameterization with registry port",
			imageRef: "registry.io:5000/repo/image",
			config: &BundleConfig{
				ImageTagParam: "imageTag",
			},
			expectedImg: "registry.io:5000/repo/image:{{ .Values.imageTag }}",
			expectedParams: map[string]string{
				"imageTag": "",
			},
		},
		// Digest test cases
		{
			name:     "basic digest parameterization",
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
		{
			name:     "digest with nested repository path",
			imageRef: "registry.io/org/team/image@sha256:abc123def456",
			config: &BundleConfig{
				ImageDigestParam: "imageDigest",
			},
			expectedImg: "registry.io/org/team/image@sha256:{{ .Values.imageDigest }}",
			expectedParams: map[string]string{
				"imageDigest": "",
			},
		},
		{
			name:     "digest with registry and repository parameterization",
			imageRef: "registry.io/myrepo/image@sha256:abc123def456",
			config: &BundleConfig{
				ImageRegistryParam:   "imageRegistry",
				ImageRepositoryParam: "imageRepository",
				ImageDigestParam:     "imageDigest",
			},
			expectedImg: "{{ .Values.imageRegistry }}/{{ .Values.imageRepository }}/image@sha256:{{ .Values.imageDigest }}",
			expectedParams: map[string]string{
				"imageRegistry":   "",
				"imageRepository": "",
				"imageDigest":     "",
			},
		},
		{
			name:     "tag with registry parameterization override digest",
			imageRef: "registry.io/myrepo/image@sha256:abc123def456",
			config: &BundleConfig{
				ImageRegistryParam: "imageRegistry",
				ImageTagParam:      "imageTag",
			},
			expectedImg: "{{ .Values.imageRegistry }}/myrepo/image:{{ .Values.imageTag }}",
			expectedParams: map[string]string{
				"imageRegistry": "",
				"imageTag":      "",
			},
		},
		{
			name:     "image without tag or digest - add digest param",
			imageRef: "registry.io/repo/image",
			config: &BundleConfig{
				ImageDigestParam: "imageDigest",
			},
			expectedImg: "registry.io/repo/image@sha256:{{ .Values.imageDigest }}",
			expectedParams: map[string]string{
				"imageDigest": "",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, params := parameterizeImageComponents(tc.imageRef, tc.config)
			assert.Equal(t, tc.expectedImg, result)
			assert.Equal(t, tc.expectedParams, params)
		})
	}
}
