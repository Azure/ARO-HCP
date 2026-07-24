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

import "testing"

func TestClientClassFromUserAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ua   string
		want string
	}{
		{
			name: "empty",
			ua:   "",
			want: clientClassOther,
		},
		{
			name: "portal / generic",
			ua:   "Mozilla/5.0 (compatible; MSIE 9.0; Windows NT 6.1; Trident/5.0)",
			want: clientClassOther,
		},
		{
			name: "aso only",
			ua:   "aso-controller/v2.13.0-hcpclusters.9",
			want: clientClassASO,
		},
		{
			name: "capz only",
			ua:   "cluster-api-provider-azure/v1.22.1-mce-217",
			want: clientClassCAPZ,
		},
		{
			name: "aso+capz combined (observed ARM UA)",
			ua:   "aso-controller/v2.13.0-hcpclusters.9 cluster-api-provider-azure/v1.22.1-mce-217",
			want: clientClassASOCAPZ,
		},
		{
			name: "aso+capz with extra tokens",
			ua:   "Go-http-client/2.0 aso-controller/v2.12.0 cluster-api-provider-azure/v1.19.0 azsdk-go-...",
			want: clientClassASOCAPZ,
		},
		{
			name: "does not match substring without slash",
			ua:   "not-aso-controller-or-cluster-api-provider-azure",
			want: clientClassOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := clientClassFromUserAgent(tt.ua); got != tt.want {
				t.Fatalf("clientClassFromUserAgent(%q) = %q, want %q", tt.ua, got, tt.want)
			}
		})
	}
}
