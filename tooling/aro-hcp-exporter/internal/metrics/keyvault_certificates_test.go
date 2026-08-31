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

package metrics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

type fakeKeyVaultCertificateClient struct {
	responses map[string]azcertificates.GetCertificateResponse
	errors    map[string]error
	requests  []string
}

func (f *fakeKeyVaultCertificateClient) GetCertificate(_ context.Context, name, version string, _ *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error) {
	f.requests = append(f.requests, name+"/"+version)
	if err := f.errors[name]; err != nil {
		return azcertificates.GetCertificateResponse{}, err
	}
	return f.responses[name], nil
}

func TestKeyVaultCertificateCollectorCollectsCertificateMetadata(t *testing.T) {
	notBefore := time.Unix(1_700_000_000, 0)
	notAfter := time.Unix(1_707_776_000, 0)
	client := &fakeKeyVaultCertificateClient{
		responses: map[string]azcertificates.GetCertificateResponse{
			"frontend-cert-dev-usw3":  certificateResponse(notBefore, notAfter, true),
			"admin-api-cert-dev-usw3": certificateResponse(notBefore, notAfter, false),
		},
	}
	errorCounter := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_errors_total"})
	collector := NewKeyVaultCertificateCollector(
		client,
		"aro-hcp-dev-svc-kv",
		"westus3",
		[]string{"frontend-cert-dev-usw3", "admin-api-cert-dev-usw3"},
		time.Hour,
		errorCounter,
	)
	collector.now = func() time.Time { return time.Unix(1_700_000_100, 0) }

	collector.CollectMetricValues(context.Background())

	require.Equal(t, []string{"frontend-cert-dev-usw3/", "admin-api-cert-dev-usw3/"}, client.requests)
	assert.Equal(t, float64(0), testutil.ToFloat64(errorCounter))
	require.NoError(t, testutil.CollectAndCompare(collector, strings.NewReader(`# HELP keyvault_certificate_collector_last_success_timestamp_seconds Unix timestamp of the last collection in which all configured Key Vault certificates were read successfully
# TYPE keyvault_certificate_collector_last_success_timestamp_seconds gauge
keyvault_certificate_collector_last_success_timestamp_seconds{key_vault="aro-hcp-dev-svc-kv",region="westus3"} 1.7000001e+09
# HELP keyvault_certificate_enabled Whether the Key Vault certificate is enabled
# TYPE keyvault_certificate_enabled gauge
keyvault_certificate_enabled{certificate_name="admin-api-cert-dev-usw3",key_vault="aro-hcp-dev-svc-kv",region="westus3"} 0
keyvault_certificate_enabled{certificate_name="frontend-cert-dev-usw3",key_vault="aro-hcp-dev-svc-kv",region="westus3"} 1
# HELP keyvault_certificate_not_after_timestamp_seconds Unix timestamp when the Key Vault certificate expires
# TYPE keyvault_certificate_not_after_timestamp_seconds gauge
keyvault_certificate_not_after_timestamp_seconds{certificate_name="admin-api-cert-dev-usw3",key_vault="aro-hcp-dev-svc-kv",region="westus3"} 1.707776e+09
keyvault_certificate_not_after_timestamp_seconds{certificate_name="frontend-cert-dev-usw3",key_vault="aro-hcp-dev-svc-kv",region="westus3"} 1.707776e+09
# HELP keyvault_certificate_not_before_timestamp_seconds Unix timestamp when the Key Vault certificate becomes valid
# TYPE keyvault_certificate_not_before_timestamp_seconds gauge
keyvault_certificate_not_before_timestamp_seconds{certificate_name="admin-api-cert-dev-usw3",key_vault="aro-hcp-dev-svc-kv",region="westus3"} 1.7e+09
keyvault_certificate_not_before_timestamp_seconds{certificate_name="frontend-cert-dev-usw3",key_vault="aro-hcp-dev-svc-kv",region="westus3"} 1.7e+09
`)))
}

func TestKeyVaultCertificateCollectorPartialFailure(t *testing.T) {
	notBefore := time.Unix(1_700_000_000, 0)
	notAfter := time.Unix(1_707_776_000, 0)
	client := &fakeKeyVaultCertificateClient{
		responses: map[string]azcertificates.GetCertificateResponse{
			"frontend-cert-dev-usw3": certificateResponse(notBefore, notAfter, true),
		},
		errors: map[string]error{
			"admin-api-cert-dev-usw3": errors.New("certificate not found"),
		},
	}
	errorCounter := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_errors_total"})
	collector := NewKeyVaultCertificateCollector(
		client,
		"aro-hcp-dev-svc-kv",
		"westus3",
		[]string{"frontend-cert-dev-usw3", "admin-api-cert-dev-usw3"},
		time.Hour,
		errorCounter,
	)

	collector.CollectMetricValues(context.Background())

	assert.Equal(t, float64(1), testutil.ToFloat64(errorCounter))
	require.NoError(t, testutil.CollectAndCompare(
		collector,
		strings.NewReader(`# HELP keyvault_certificate_enabled Whether the Key Vault certificate is enabled
# TYPE keyvault_certificate_enabled gauge
keyvault_certificate_enabled{certificate_name="frontend-cert-dev-usw3",key_vault="aro-hcp-dev-svc-kv",region="westus3"} 1
`),
		"keyvault_certificate_enabled",
		"keyvault_certificate_collector_last_success_timestamp_seconds",
	))
}

func TestKeyVaultCertificateCollectorLastSuccessRecoversAfterFailure(t *testing.T) {
	notBefore := time.Unix(1_700_000_000, 0)
	notAfter := time.Unix(1_707_776_000, 0)
	client := &fakeKeyVaultCertificateClient{
		responses: map[string]azcertificates.GetCertificateResponse{
			"certificate": certificateResponse(notBefore, notAfter, true),
		},
		errors: map[string]error{},
	}
	collector := NewKeyVaultCertificateCollector(
		client,
		"vault",
		"westus3",
		[]string{"certificate"},
		10*time.Millisecond,
		prometheus.NewCounter(prometheus.CounterOpts{Name: "test_errors_total"}),
	)
	currentTime := time.Unix(100, 0)
	collector.now = func() time.Time { return currentTime }

	collector.CollectMetricValues(context.Background())
	requireLastSuccessTimestamp(t, collector, 100)
	require.Eventually(t, func() bool {
		return testutil.CollectAndCount(collector, "keyvault_certificate_enabled") == 0
	}, time.Second, 10*time.Millisecond)

	currentTime = time.Unix(200, 0)
	client.errors["certificate"] = errors.New("temporarily unavailable")
	collector.CollectMetricValues(context.Background())
	requireLastSuccessTimestamp(t, collector, 100)

	currentTime = time.Unix(300, 0)
	delete(client.errors, "certificate")
	collector.CollectMetricValues(context.Background())
	requireLastSuccessTimestamp(t, collector, 300)
}

func TestKeyVaultCertificateCollectorRejectsIncompleteMetadata(t *testing.T) {
	tests := []struct {
		name       string
		attributes *azcertificates.CertificateAttributes
	}{
		{name: "missing attributes"},
		{name: "missing not-before", attributes: &azcertificates.CertificateAttributes{Expires: to.Ptr(time.Unix(2, 0)), Enabled: to.Ptr(true)}},
		{name: "missing expiry", attributes: &azcertificates.CertificateAttributes{NotBefore: to.Ptr(time.Unix(1, 0)), Enabled: to.Ptr(true)}},
		{name: "missing enabled", attributes: &azcertificates.CertificateAttributes{NotBefore: to.Ptr(time.Unix(1, 0)), Expires: to.Ptr(time.Unix(2, 0))}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeKeyVaultCertificateClient{
				responses: map[string]azcertificates.GetCertificateResponse{
					"certificate": {Certificate: azcertificates.Certificate{Attributes: tt.attributes}},
				},
			}
			errorCounter := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_errors_total"})
			collector := NewKeyVaultCertificateCollector(client, "vault", "westus3", []string{"certificate"}, time.Hour, errorCounter)

			collector.CollectMetricValues(context.Background())

			assert.Equal(t, float64(1), testutil.ToFloat64(errorCounter))
			require.NoError(t, testutil.CollectAndCompare(collector, strings.NewReader("")))
		})
	}
}

func TestKeyVaultCertificateCollectorCacheExpires(t *testing.T) {
	notBefore := time.Unix(1_700_000_000, 0)
	notAfter := time.Unix(1_707_776_000, 0)
	client := &fakeKeyVaultCertificateClient{
		responses: map[string]azcertificates.GetCertificateResponse{
			"certificate": certificateResponse(notBefore, notAfter, true),
		},
	}
	collector := NewKeyVaultCertificateCollector(
		client,
		"vault",
		"westus3",
		[]string{"certificate"},
		10*time.Millisecond,
		prometheus.NewCounter(prometheus.CounterOpts{Name: "test_errors_total"}),
	)
	collector.now = func() time.Time { return time.Unix(100, 0) }
	collector.CollectMetricValues(context.Background())
	require.Eventually(t, func() bool {
		return testutil.CollectAndCount(collector) == 1
	}, time.Second, 10*time.Millisecond)
	requireLastSuccessTimestamp(t, collector, 100)
}

func certificateResponse(notBefore, notAfter time.Time, enabled bool) azcertificates.GetCertificateResponse {
	return azcertificates.GetCertificateResponse{
		Certificate: azcertificates.Certificate{
			Attributes: &azcertificates.CertificateAttributes{
				NotBefore: to.Ptr(notBefore),
				Expires:   to.Ptr(notAfter),
				Enabled:   to.Ptr(enabled),
			},
		},
	}
}

func requireLastSuccessTimestamp(t *testing.T, collector *KeyVaultCertificateCollector, timestamp int64) {
	t.Helper()
	expected := strings.NewReader(`# HELP keyvault_certificate_collector_last_success_timestamp_seconds Unix timestamp of the last collection in which all configured Key Vault certificates were read successfully
# TYPE keyvault_certificate_collector_last_success_timestamp_seconds gauge
keyvault_certificate_collector_last_success_timestamp_seconds{key_vault="vault",region="westus3"} ` + fmt.Sprintf("%d\n", timestamp))
	require.NoError(t, testutil.CollectAndCompare(
		collector,
		expected,
		"keyvault_certificate_collector_last_success_timestamp_seconds",
	))
}
