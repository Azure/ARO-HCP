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

package frontend

import (
	"strings"
	"testing"
)

func TestUserAgentMetricLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ua   string
		want string
	}{
		{
			name: "empty",
			ua:   "",
			want: userAgentOther,
		},
		{
			name: "portal / generic",
			ua:   "Mozilla/5.0 (compatible; MSIE 9.0; Windows NT 6.1; Trident/5.0)",
			want: userAgentOther,
		},
		{
			name: "aso only keeps product token",
			ua:   "aso-controller/v2.13.0-hcpclusters.9",
			want: "aso-controller/v2.13.0-hcpclusters.9",
		},
		{
			name: "capz only keeps product token",
			ua:   "cluster-api-provider-azure/v1.22.1-mce-217",
			want: "cluster-api-provider-azure/v1.22.1-mce-217",
		},
		{
			name: "strips azsdk wrapper, keeps CapZ/ASO tokens with versions",
			ua:   "azsdk-go-generic/v2.13.0-hcpclusters.9 (go1.24.13; linux) aso-controller/v2.13.0-hcpclusters.9 cluster-api-provider-azure/v1.22.1-mce-217",
			want: "aso-controller/v2.13.0-hcpclusters.9 cluster-api-provider-azure/v1.22.1-mce-217",
		},
		{
			name: "does not match substring without slash",
			ua:   "not-aso-controller-or-cluster-api-provider-azure",
			want: userAgentOther,
		},
		{
			name: "embedded token in unrelated product is ignored",
			ua:   "evil-aso-controller/v9 not-cluster-api-provider-azure/v9",
			want: userAgentOther,
		},
		{
			name: "overlong extracted label falls back to other",
			ua:   "aso-controller/" + strings.Repeat("x", maxUserAgentLabelLen),
			want: userAgentOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := userAgentMetricLabel(tt.ua); got != tt.want {
				t.Fatalf("userAgentMetricLabel(%q) = %q, want %q", tt.ua, got, tt.want)
			}
		})
	}
}
