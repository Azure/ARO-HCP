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

package breakglass

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	// MaxCSRNameLength is the maximum length for Kubernetes CSR names
	MaxCSRNameLength = 253
	// MaxRandomSuffixLength is the maximum length for random suffixes
	MaxRandomSuffixLength = 8
	// TimestampFormat is the format used for timestamp suffixes
	TimestampFormat = "20060102-150405"
)

// Config holds configurable values for the breakglass workflow.
// This struct centralizes hardcoded strings and makes the system more flexible
// and testable by allowing configuration of service names, secrets, templates, and labels.
type Config struct {
	// Service configuration
	Services ServiceConfig `json:"services" yaml:"services"`

	// Secret configuration
	Secrets SecretConfig `json:"secrets" yaml:"secrets"`

	// Template configuration for dynamic resource naming
	Templates TemplateConfig `json:"templates" yaml:"templates"`

	// Label configuration for resource tagging
	Labels LabelConfig `json:"labels" yaml:"labels"`

	// CSR configuration
	CSR CSRConfig `json:"csr" yaml:"csr"`

	// Resource naming configuration
	Naming ResourceNamingConfig `json:"naming" yaml:"naming"`
}

// ServiceConfig defines configurable service names and endpoints.
type ServiceConfig struct {
	// KubeAPIServer is the name of the Kubernetes API server service
	KubeAPIServer string `json:"kubeAPIServer" yaml:"kubeAPIServer"`
}

// SecretConfig defines configurable secret names and keys.
type SecretConfig struct {
	// KASServerCert is the name of the secret containing the KAS CA certificate
	KASServerCert string `json:"kasServerCert" yaml:"kasServerCert"`
	// KASServerCertKey is the key within the secret containing the CA certificate
	KASServerCertKey string `json:"kasServerCertKey" yaml:"kasServerCertKey"`
}

// TemplateConfig defines configurable templates for dynamic resource naming.
type TemplateConfig struct {
	// CSRNameTemplate is the template for generating CSR names
	// Expected format arguments: clusterID, user
	CSRNameTemplate string `json:"csrNameTemplate" yaml:"csrNameTemplate"`
	// SignerNameTemplate is the template for generating CSR signer names
	// Expected format arguments: namespace
	SignerNameTemplate string `json:"signerNameTemplate" yaml:"signerNameTemplate"`
	// UserNameTemplate is the template for generating kubeconfig user names
	// Expected format arguments: clusterID, user
	UserNameTemplate string `json:"userNameTemplate" yaml:"userNameTemplate"`
}

// LabelConfig defines configurable label keys and values for resource tagging.
type LabelConfig struct {
	// ClusterIDKey is the label key for cluster identification
	ClusterIDKey string `json:"clusterIdKey" yaml:"clusterIdKey"`
	// UserNameKey is the label key for user identification
	UserNameKey string `json:"userNameKey" yaml:"userNameKey"`
	// ResourceTypeKey is the label key for resource type identification
	ResourceTypeKey string `json:"resourceTypeKey" yaml:"resourceTypeKey"`
	// ResourceTypeValue is the label value for breakglass resources
	ResourceTypeValue string `json:"resourceTypeValue" yaml:"resourceTypeValue"`
}

// CSRConfig defines configurable CSR-specific settings.
type CSRConfig struct {
	// Organization is the organization name in the certificate subject
	Organization string `json:"organization" yaml:"organization"`
	// CommonNameTemplate is the template for generating certificate common names
	// Expected format arguments: user
	CommonNameTemplate string `json:"commonNameTemplate" yaml:"commonNameTemplate"`
}

// ResourceNamingConfig defines configurable resource naming strategies.
type ResourceNamingConfig struct {
	// AddTimestamp controls whether to add timestamp suffixes to resource names
	AddTimestamp bool `json:"addTimestamp" yaml:"addTimestamp"`
	// AddRandomSuffix controls whether to add random suffixes to resource names
	AddRandomSuffix bool `json:"addRandomSuffix" yaml:"addRandomSuffix"`
	// RandomSuffixLength is the length of random suffixes (max 8)
	RandomSuffixLength int `json:"randomSuffixLength" yaml:"randomSuffixLength"`
	// ValidateLengths controls whether to validate resource name lengths
	ValidateLengths bool `json:"validateLengths" yaml:"validateLengths"`
}

// DefaultConfig returns a Config struct with default values matching current behavior.
func DefaultConfig() *Config {
	return &Config{
		Services: ServiceConfig{
			KubeAPIServer: "kube-apiserver",
		},
		Secrets: SecretConfig{
			KASServerCert:    "kas-server-crt",
			KASServerCertKey: "tls.crt",
		},
		Templates: TemplateConfig{
			CSRNameTemplate:    "sre-breakglass-%s-%s",
			SignerNameTemplate: "hypershift.openshift.io/%s.sre-break-glass",
			UserNameTemplate:   "sre-breakglass-%s-%s",
		},
		Labels: LabelConfig{
			ClusterIDKey:      "api.openshift.com/id",
			UserNameKey:       "api.openshift.com/name",
			ResourceTypeKey:   "api.openshift.com/type",
			ResourceTypeValue: "break-glass-credential",
		},
		CSR: CSRConfig{
			Organization:       "sre-group",
			CommonNameTemplate: "system:sre-break-glass:%s",
		},
		Naming: ResourceNamingConfig{
			AddTimestamp:       true,
			AddRandomSuffix:    false,
			RandomSuffixLength: 6,
			ValidateLengths:    true,
		},
	}
}

// GenerateCSRName creates a CSR name using the configured template with optional suffixes.
func (cfg *Config) GenerateCSRName(clusterID, user string) (string, error) {
	baseName := fmt.Sprintf(cfg.Templates.CSRNameTemplate, clusterID, user)

	// Add suffixes if configured
	var suffixes []string

	if cfg.Naming.AddTimestamp {
		timestamp := time.Now().Format(TimestampFormat)
		suffixes = append(suffixes, timestamp)
	}

	if cfg.Naming.AddRandomSuffix {
		randomSuffix, err := generateRandomSuffix(cfg.Naming.RandomSuffixLength)
		if err != nil {
			return "", fmt.Errorf("failed to generate random suffix: %w", err)
		}
		suffixes = append(suffixes, randomSuffix)
	}

	// Combine base name with suffixes
	var finalName string
	if len(suffixes) > 0 {
		finalName = baseName + "-" + strings.Join(suffixes, "-")
	} else {
		finalName = baseName
	}

	// Validate length if configured
	if cfg.Naming.ValidateLengths && len(finalName) > MaxCSRNameLength {
		return "", NewValidationError("csrName", finalName,
			fmt.Sprintf("generated name exceeds maximum length of %d characters", MaxCSRNameLength), nil)
	}

	return finalName, nil
}

// GenerateSignerName creates a signer name using the configured template.
func (cfg *Config) GenerateSignerName(namespace string) string {
	return fmt.Sprintf(cfg.Templates.SignerNameTemplate, namespace)
}

// GenerateUserName creates a kubeconfig user name using the configured template.
func (cfg *Config) GenerateUserName(clusterID, user string) string {
	return fmt.Sprintf(cfg.Templates.UserNameTemplate, clusterID, user)
}

// GenerateCommonName creates a certificate common name using the configured template.
func (cfg *Config) GenerateCommonName(user string) string {
	return fmt.Sprintf(cfg.CSR.CommonNameTemplate, user)
}

// GenerateLabels creates a standard set of labels for breakglass resources.
func (cfg *Config) GenerateLabels(clusterID, user string) map[string]string {
	return map[string]string{
		cfg.Labels.ClusterIDKey:    clusterID,
		cfg.Labels.UserNameKey:     user,
		cfg.Labels.ResourceTypeKey: cfg.Labels.ResourceTypeValue,
	}
}

// Validate checks that all required configuration values are set and valid.
func (cfg *Config) Validate() error {
	if cfg.Services.KubeAPIServer == "" {
		return NewConfigurationError("services", "kubeAPIServer", "cannot be empty", nil)
	}

	if cfg.Secrets.KASServerCert == "" {
		return NewConfigurationError("secrets", "kasServerCert", "cannot be empty", nil)
	}

	if cfg.Secrets.KASServerCertKey == "" {
		return NewConfigurationError("secrets", "kasServerCertKey", "cannot be empty", nil)
	}

	if cfg.Templates.CSRNameTemplate == "" {
		return NewConfigurationError("templates", "csrNameTemplate", "cannot be empty", nil)
	}

	if cfg.Templates.SignerNameTemplate == "" {
		return NewConfigurationError("templates", "signerNameTemplate", "cannot be empty", nil)
	}

	if cfg.Templates.UserNameTemplate == "" {
		return NewConfigurationError("templates", "userNameTemplate", "cannot be empty", nil)
	}

	if cfg.Labels.ClusterIDKey == "" {
		return NewConfigurationError("labels", "clusterIdKey", "cannot be empty", nil)
	}

	if cfg.Labels.UserNameKey == "" {
		return NewConfigurationError("labels", "userNameKey", "cannot be empty", nil)
	}

	if cfg.Labels.ResourceTypeKey == "" {
		return NewConfigurationError("labels", "resourceTypeKey", "cannot be empty", nil)
	}

	if cfg.Labels.ResourceTypeValue == "" {
		return NewConfigurationError("labels", "resourceTypeValue", "cannot be empty", nil)
	}

	if cfg.CSR.Organization == "" {
		return NewConfigurationError("csr", "organization", "cannot be empty", nil)
	}

	if cfg.CSR.CommonNameTemplate == "" {
		return NewConfigurationError("csr", "commonNameTemplate", "cannot be empty", nil)
	}

	// Validate resource naming configuration
	if cfg.Naming.RandomSuffixLength > MaxRandomSuffixLength {
		return NewConfigurationError("naming", "randomSuffixLength",
			fmt.Sprintf("cannot exceed %d characters", MaxRandomSuffixLength), nil)
	}

	if cfg.Naming.RandomSuffixLength < 1 && cfg.Naming.AddRandomSuffix {
		return NewConfigurationError("naming", "randomSuffixLength",
			"must be at least 1 when addRandomSuffix is enabled", nil)
	}

	return nil
}

// generateRandomSuffix creates a random alphanumeric suffix of the specified length.
func generateRandomSuffix(length int) (string, error) {
	if length <= 0 {
		return "", nil
	}

	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)

	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[num.Int64()]
	}

	return string(result), nil
}
