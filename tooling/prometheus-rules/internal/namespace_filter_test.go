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

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectNamespaceFilter(t *testing.T) {
	tests := []struct {
		name       string
		expr       string
		namespaces []string
		expected   string
	}{
		{
			name:       "simple metric",
			expr:       `up == 0`,
			namespaces: []string{"aro-hcp", "clusters-service"},
			expected:   `up{namespace=~"aro-hcp|clusters-service"} == 0`,
		},
		{
			name:       "metric with existing labels",
			expr:       `kube_resourcequota{job="kube-state-metrics", type="used"}`,
			namespaces: []string{"aro-hcp"},
			expected:   `kube_resourcequota{job="kube-state-metrics",namespace=~"aro-hcp",type="used"}`,
		},
		{
			name:       "metric with existing namespace matcher is not modified",
			expr:       `foo{namespace="bar"}`,
			namespaces: []string{"aro-hcp", "clusters-service"},
			expected:   `foo{namespace="bar"}`,
		},
		{
			name:       "complex expression with multiple selectors",
			expr:       `sum(rate(rest_client_requests_total{job="apiserver",code=~"5.."}[5m])) by (cluster) / sum(rate(rest_client_requests_total{job="apiserver"}[5m])) by (cluster)`,
			namespaces: []string{"aro-hcp"},
			expected:   `sum by (cluster) (rate(rest_client_requests_total{code=~"5..",job="apiserver",namespace=~"aro-hcp"}[5m])) / sum by (cluster) (rate(rest_client_requests_total{job="apiserver",namespace=~"aro-hcp"}[5m]))`,
		},
		{
			name:       "binary expression with and",
			expr:       `kube_resourcequota{type="used"} > 0.9 and kube_resourcequota{type="hard"} > 0`,
			namespaces: []string{"maestro", "aro-hcp"},
			expected:   `kube_resourcequota{namespace=~"maestro|aro-hcp",type="used"} > 0.9 and kube_resourcequota{namespace=~"maestro|aro-hcp",type="hard"} > 0`,
		},
		{
			name:       "expression with ignoring clause",
			expr:       `kube_resourcequota{type="used"} / ignoring(instance, job, type) (kube_resourcequota{type="hard"} > 0)`,
			namespaces: []string{"aro-hcp"},
			expected:   `kube_resourcequota{namespace=~"aro-hcp",type="used"} / ignoring (instance, job, type) (kube_resourcequota{namespace=~"aro-hcp",type="hard"} > 0)`,
		},
		{
			name:       "single namespace",
			expr:       `up`,
			namespaces: []string{"aro-hcp"},
			expected:   `up{namespace=~"aro-hcp"}`,
		},
		{
			name:       "empty entries are filtered out",
			expr:       `up`,
			namespaces: []string{"aro-hcp", "", "maestro"},
			expected:   `up{namespace=~"aro-hcp|maestro"}`,
		},
		{
			name:       "whitespace-only entries are filtered out",
			expr:       `up`,
			namespaces: []string{"aro-hcp", "  ", "maestro"},
			expected:   `up{namespace=~"aro-hcp|maestro"}`,
		},
		{
			name:       "all empty namespaces returns expression unmodified",
			expr:       `up`,
			namespaces: []string{"", "  ", "	"},
			expected:   `up`,
		},
		{
			name:       "aggregation with by clause",
			expr:       `sum by(name, namespace, cluster)(increase(aggregator_unavailable_apiservice_total{job="apiserver"}[10m]))`,
			namespaces: []string{"aro-hcp"},
			expected:   `sum by (name, namespace, cluster) (increase(aggregator_unavailable_apiservice_total{job="apiserver",namespace=~"aro-hcp"}[10m]))`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := injectNamespaceFilter(tt.expr, tt.namespaces)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInjectNamespaceFilterErrors(t *testing.T) {
	tests := []struct {
		name       string
		expr       string
		namespaces []string
		errorMsg   string
	}{
		{
			name:       "invalid PromQL expression",
			expr:       `invalid{{{`,
			namespaces: []string{"aro-hcp"},
			errorMsg:   "failed to parse PromQL expression",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := injectNamespaceFilter(tt.expr, tt.namespaces)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errorMsg)
		})
	}
}
