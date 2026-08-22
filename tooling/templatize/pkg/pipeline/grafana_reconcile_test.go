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

package pipeline

import (
	"testing"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/manage"
)

func TestDefaultReconcileOptionsPublicNetworkAccess(t *testing.T) {
	opts := manage.DefaultReconcileOptions()
	if opts.PublicNetworkAccess != "Enabled" {
		t.Fatalf("expected default PublicNetworkAccess 'Enabled' (backward compatible), got %q", opts.PublicNetworkAccess)
	}
}

func TestResolvePublicNetworkAccess(t *testing.T) {
	tests := []struct {
		name     string
		cfg      configtypes.Configuration
		step     types.Value
		expected string
	}{
		{
			name: "config ref resolves to Enabled",
			cfg: configtypes.Configuration{
				"monitoring": map[string]any{
					"grafanaPublicNetworkAccess": "Enabled",
				},
			},
			step:     types.Value{ConfigRef: "monitoring.grafanaPublicNetworkAccess"},
			expected: "Enabled",
		},
		{
			name: "config ref resolves to Disabled",
			cfg: configtypes.Configuration{
				"monitoring": map[string]any{
					"grafanaPublicNetworkAccess": "Disabled",
				},
			},
			step:     types.Value{ConfigRef: "monitoring.grafanaPublicNetworkAccess"},
			expected: "Disabled",
		},
		{
			name:     "empty value returns empty string (uses tool default)",
			cfg:      configtypes.Configuration{},
			step:     types.Value{},
			expected: "",
		},
		{
			name:     "direct value override",
			cfg:      configtypes.Configuration{},
			step:     types.Value{Value: "Disabled"},
			expected: "Disabled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveOptionalValue(tt.step, tt.cfg, Outputs{}, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("resolveOptionalValue() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestPublicNetworkAccessOverridesDefault(t *testing.T) {
	opts := manage.DefaultReconcileOptions()
	if opts.PublicNetworkAccess != "Enabled" {
		t.Fatalf("precondition: expected default 'Enabled', got %q", opts.PublicNetworkAccess)
	}

	cfg := configtypes.Configuration{
		"monitoring": map[string]any{
			"grafanaPublicNetworkAccess": "Disabled",
		},
	}

	resolved, err := resolveOptionalValue(
		types.Value{ConfigRef: "monitoring.grafanaPublicNetworkAccess"},
		cfg, Outputs{}, "",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "" {
		opts.PublicNetworkAccess = resolved
	}

	if opts.PublicNetworkAccess != "Disabled" {
		t.Fatalf("expected config value 'Disabled' to override default, got %q", opts.PublicNetworkAccess)
	}
}
