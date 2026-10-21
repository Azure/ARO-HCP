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

package apihelpers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestIsPlatformImageContentSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name:   "OpenShift 4 development images",
			source: "quay.io/openshift-release-dev/ocp-v4.0-art-dev",
			want:   true,
		},
		{
			name:   "OpenShift 5 development images",
			source: "quay.io/openshift-release-dev/ocp-v5.0-art-dev",
			want:   true,
		},
		{
			name:   "OpenShift release images",
			source: "quay.io/openshift-release-dev/ocp-release",
			want:   true,
		},
		{
			name:   "OpenShift nightly release images",
			source: "quay.io/openshift-release-dev/ocp-release-nightly",
			want:   true,
		},
		{
			name:   "customer images",
			source: "quay.io/customer/images",
			want:   false,
		},
		{
			name:   "empty source",
			source: "",
			want:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, IsPlatformImageContentSource(test.source))
		})
	}
}

func TestImageContentSourcesHaveSource(test *testing.T) {
	test.Parallel()

	tests := []struct {
		name    string
		sources []hsv1beta1.ImageContentSource
		want    bool
	}{
		{name: "nil"},
		{name: "empty", sources: []hsv1beta1.ImageContentSource{}},
		{name: "missing", sources: []hsv1beta1.ImageContentSource{{Source: "quay.io/customer/images"}}},
		{name: "present", sources: []hsv1beta1.ImageContentSource{{Source: "quay.io/customer/images"}, {Source: OcpV5ArtDevMirrorSource}}, want: true},
		{name: "mirror is not source", sources: []hsv1beta1.ImageContentSource{{Source: "quay.io/customer/images", Mirrors: []string{OcpV5ArtDevMirrorSource}}}},
	}
	for _, testcase := range tests {
		test.Run(testcase.name, func(test *testing.T) {
			test.Parallel()
			assert.Equal(test, testcase.want, ImageContentSourcesHaveSource(testcase.sources, OcpV5ArtDevMirrorSource))
		})
	}
}
