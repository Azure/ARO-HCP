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
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/tooling/metricscache"
)

const KeyVaultCertificateCollectorName = "keyvault-certificates"

var (
	keyVaultCertificateNotBeforeTimestampSecondsDesc = prometheus.NewDesc(
		"keyvault_certificate_not_before_timestamp_seconds",
		"Unix timestamp when the Key Vault certificate becomes valid",
		[]string{"certificate_name", "key_vault", "region"},
		nil,
	)
	keyVaultCertificateNotAfterTimestampSecondsDesc = prometheus.NewDesc(
		"keyvault_certificate_not_after_timestamp_seconds",
		"Unix timestamp when the Key Vault certificate expires",
		[]string{"certificate_name", "key_vault", "region"},
		nil,
	)
	keyVaultCertificateEnabledDesc = prometheus.NewDesc(
		"keyvault_certificate_enabled",
		"Whether the Key Vault certificate is enabled",
		[]string{"certificate_name", "key_vault", "region"},
		nil,
	)
	keyVaultCertificateCollectorLastSuccessTimestampSecondsDesc = prometheus.NewDesc(
		"keyvault_certificate_collector_last_success_timestamp_seconds",
		"Unix timestamp of the last collection in which all configured Key Vault certificates were read successfully",
		[]string{"key_vault", "region"},
		nil,
	)
)

type KeyVaultCertificateClient interface {
	GetCertificate(ctx context.Context, name, version string, options *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error)
}

type KeyVaultCertificateCollector struct {
	client           KeyVaultCertificateClient
	cache            *metricscache.Cache
	certificateNames []string
	keyVault         string
	region           string
	errorCounter     prometheus.Counter
	now              func() time.Time
	lastSuccessMu    sync.RWMutex
	lastSuccess      time.Time
}

var _ CachingCollector = &KeyVaultCertificateCollector{}

func NewKeyVaultCertificateCollector(
	client KeyVaultCertificateClient,
	keyVault string,
	region string,
	certificateNames []string,
	cacheTTL time.Duration,
	errorCounter prometheus.Counter,
) *KeyVaultCertificateCollector {
	return &KeyVaultCertificateCollector{
		client:           client,
		cache:            metricscache.NewCache(cacheTTL),
		certificateNames: certificateNames,
		keyVault:         keyVault,
		region:           region,
		errorCounter:     errorCounter,
		now:              time.Now,
	}
}

func (c *KeyVaultCertificateCollector) Name() string {
	return KeyVaultCertificateCollectorName
}

func (c *KeyVaultCertificateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- keyVaultCertificateNotBeforeTimestampSecondsDesc
	ch <- keyVaultCertificateNotAfterTimestampSecondsDesc
	ch <- keyVaultCertificateEnabledDesc
	ch <- keyVaultCertificateCollectorLastSuccessTimestampSecondsDesc
}

func (c *KeyVaultCertificateCollector) Collect(ch chan<- prometheus.Metric) {
	for _, metric := range c.cache.GetAll() {
		ch <- metric
	}

	c.lastSuccessMu.RLock()
	lastSuccess := c.lastSuccess
	c.lastSuccessMu.RUnlock()
	lastSuccessTimestamp := int64(0)
	if !lastSuccess.IsZero() {
		lastSuccessTimestamp = lastSuccess.Unix()
	}
	ch <- prometheus.MustNewConstMetric(
		keyVaultCertificateCollectorLastSuccessTimestampSecondsDesc,
		prometheus.GaugeValue,
		float64(lastSuccessTimestamp),
		c.keyVault,
		c.region,
	)
}

func (c *KeyVaultCertificateCollector) CollectMetricValues(ctx context.Context) {
	logger := logr.FromContextOrDiscard(ctx)
	allSucceeded := true

	for _, certificateName := range c.certificateNames {
		response, err := c.client.GetCertificate(ctx, certificateName, "", nil)
		if err != nil {
			allSucceeded = false
			c.errorCounter.Inc()
			logger.Error(err, "Failed to get Key Vault certificate metadata", "keyVault", c.keyVault, "certificateName", certificateName)
			continue
		}

		if err := c.cacheCertificateMetrics(certificateName, response); err != nil {
			allSucceeded = false
			c.errorCounter.Inc()
			logger.Error(err, "Invalid Key Vault certificate metadata", "keyVault", c.keyVault, "certificateName", certificateName)
		}
	}

	if allSucceeded {
		c.lastSuccessMu.Lock()
		c.lastSuccess = c.now()
		c.lastSuccessMu.Unlock()
	}
}

func (c *KeyVaultCertificateCollector) cacheCertificateMetrics(certificateName string, response azcertificates.GetCertificateResponse) error {
	attributes := response.Attributes
	if attributes == nil {
		return fmt.Errorf("certificate %q attributes are missing", certificateName)
	}
	if attributes.NotBefore == nil {
		return fmt.Errorf("certificate %q not-before timestamp is missing", certificateName)
	}
	if attributes.Expires == nil {
		return fmt.Errorf("certificate %q expiry timestamp is missing", certificateName)
	}
	if attributes.Enabled == nil {
		return fmt.Errorf("certificate %q enabled attribute is missing", certificateName)
	}

	labels := []string{certificateName, c.keyVault, c.region}
	c.cache.Set(certificateName+"/not-before", prometheus.MustNewConstMetric(
		keyVaultCertificateNotBeforeTimestampSecondsDesc,
		prometheus.GaugeValue,
		float64(attributes.NotBefore.Unix()),
		labels...,
	))
	c.cache.Set(certificateName+"/not-after", prometheus.MustNewConstMetric(
		keyVaultCertificateNotAfterTimestampSecondsDesc,
		prometheus.GaugeValue,
		float64(attributes.Expires.Unix()),
		labels...,
	))
	enabled := 0.0
	if *attributes.Enabled {
		enabled = 1.0
	}
	c.cache.Set(certificateName+"/enabled", prometheus.MustNewConstMetric(
		keyVaultCertificateEnabledDesc,
		prometheus.GaugeValue,
		enabled,
		labels...,
	))

	return nil
}
