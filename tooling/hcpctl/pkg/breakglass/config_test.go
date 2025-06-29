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
	"encoding/json"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/testutil"
)

// TestDefaultConfig tests that the default configuration is valid and contains expected values.
func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	// Validate the configuration
	if err := config.Validate(); err != nil {
		t.Fatalf("Default configuration should be valid: %v", err)
	}

	// Check key default values
	if config.Services.KubeAPIServer != "kube-apiserver" {
		t.Errorf("Expected KubeAPIServer to be 'kube-apiserver', got %s", config.Services.KubeAPIServer)
	}

	if config.Secrets.KASServerCert != "kas-server-crt" {
		t.Errorf("Expected KASServerCert to be 'kas-server-crt', got %s", config.Secrets.KASServerCert)
	}

	if config.Templates.CSRNameTemplate != "sre-breakglass-%s-%s" {
		t.Errorf("Expected CSRNameTemplate to be 'sre-breakglass-%%s-%%s', got %s", config.Templates.CSRNameTemplate)
	}

	if config.CSR.Organization != "sre-group" {
		t.Errorf("Expected CSR Organization to be 'sre-group', got %s", config.CSR.Organization)
	}

	if !config.Naming.AddTimestamp {
		t.Error("Expected AddTimestamp to be true by default")
	}

	if config.Naming.AddRandomSuffix {
		t.Error("Expected AddRandomSuffix to be false by default")
	}
}

// TestConfigValidation tests configuration validation for various scenarios.
func TestConfigValidation(t *testing.T) {
	testCases := []struct {
		name        string
		modifyFunc  func(*Config)
		expectError bool
		errorField  string
	}{
		{
			name:        "valid default config",
			modifyFunc:  func(c *Config) {},
			expectError: false,
		},
		{
			name: "empty KubeAPIServer",
			modifyFunc: func(c *Config) {
				c.Services.KubeAPIServer = ""
			},
			expectError: true,
			errorField:  "kubeAPIServer",
		},
		{
			name: "empty KASServerCert",
			modifyFunc: func(c *Config) {
				c.Secrets.KASServerCert = ""
			},
			expectError: true,
			errorField:  "kasServerCert",
		},
		{
			name: "empty CSRNameTemplate",
			modifyFunc: func(c *Config) {
				c.Templates.CSRNameTemplate = ""
			},
			expectError: true,
			errorField:  "csrNameTemplate",
		},
		{
			name: "empty Organization",
			modifyFunc: func(c *Config) {
				c.CSR.Organization = ""
			},
			expectError: true,
			errorField:  "organization",
		},
		{
			name: "random suffix too long",
			modifyFunc: func(c *Config) {
				c.Naming.RandomSuffixLength = MaxRandomSuffixLength + 1
			},
			expectError: true,
			errorField:  "randomSuffixLength",
		},
		{
			name: "random suffix enabled but length zero",
			modifyFunc: func(c *Config) {
				c.Naming.AddRandomSuffix = true
				c.Naming.RandomSuffixLength = 0
			},
			expectError: true,
			errorField:  "randomSuffixLength",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := DefaultConfig()
			tc.modifyFunc(config)

			err := config.Validate()
			if tc.expectError {
				if err == nil {
					t.Error("Expected validation error, got nil")
				} else if !strings.Contains(err.Error(), tc.errorField) {
					t.Errorf("Expected error to contain '%s', got: %v", tc.errorField, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no validation error, got: %v", err)
				}
			}
		})
	}
}

// TestGenerateCSRName tests CSR name generation with various configurations.
func TestGenerateCSRName(t *testing.T) {
	testCases := []struct {
		name        string
		config      func() *Config
		clusterID   string
		user        string
		expectError bool
	}{
		{
			name: "basic name generation",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Naming.AddTimestamp = false
				cfg.Naming.AddRandomSuffix = false
				return cfg
			},
			clusterID:   "test-cluster",
			user:        "testuser",
			expectError: false,
		},
		{
			name: "with timestamp",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Naming.AddTimestamp = true
				cfg.Naming.AddRandomSuffix = false
				return cfg
			},
			clusterID:   "test-cluster",
			user:        "testuser",
			expectError: false,
		},
		{
			name: "with random suffix",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Naming.AddTimestamp = false
				cfg.Naming.AddRandomSuffix = true
				cfg.Naming.RandomSuffixLength = 6
				return cfg
			},
			clusterID:   "test-cluster",
			user:        "testuser",
			expectError: false,
		},
		{
			name: "with both timestamp and random",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Naming.AddTimestamp = true
				cfg.Naming.AddRandomSuffix = true
				cfg.Naming.RandomSuffixLength = 4
				return cfg
			},
			clusterID:   "test-cluster",
			user:        "testuser",
			expectError: false,
		},
		{
			name: "name too long triggers validation error",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Naming.AddTimestamp = true
				cfg.Naming.AddRandomSuffix = true
				cfg.Naming.RandomSuffixLength = MaxRandomSuffixLength
				cfg.Naming.ValidateLengths = true
				return cfg
			},
			clusterID:   "very-long-cluster-id-that-will-make-the-final-name-exceed-kubernetes-limits-when-combined-with-timestamp-and-random-suffix-making-it-invalid-and-should-trigger-validation-error-because-the-total-length-exceeds-the-max-csr-name-length",
			user:        "very-long-username-that-also-contributes-to-exceeding-limits",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := tc.config()
			name, err := config.GenerateCSRName(tc.clusterID, tc.user)

			if tc.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Check that name contains expected base components
			expectedBase := "sre-breakglass-" + tc.clusterID + "-" + tc.user
			if !strings.HasPrefix(name, expectedBase) {
				t.Errorf("Expected name to start with '%s', got '%s'", expectedBase, name)
			}

			// Validate name length
			if config.Naming.ValidateLengths && len(name) > MaxCSRNameLength {
				t.Errorf("Generated name exceeds max length: %d > %d", len(name), MaxCSRNameLength)
			}
		})
	}
}

// TestGenerateCSRNameGolden tests CSR name generation using golden files.
func TestGenerateCSRNameGolden(t *testing.T) {
	testCases := []struct {
		name      string
		clusterID string
		user      string
		config    func() *Config
	}{
		{
			name:      "basic_name",
			clusterID: "production-cluster",
			user:      "john.doe",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Naming.AddTimestamp = false
				cfg.Naming.AddRandomSuffix = false
				return cfg
			},
		},
		{
			name:      "special_characters",
			clusterID: "test-cluster-123",
			user:      "user_with_underscores",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Naming.AddTimestamp = false
				cfg.Naming.AddRandomSuffix = false
				return cfg
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := tc.config()
			name, err := config.GenerateCSRName(tc.clusterID, tc.user)
			if err != nil {
				t.Fatalf("Failed to generate CSR name: %v", err)
			}

			testutil.CompareWithFixture(t, name, testutil.WithExtension(".txt"), testutil.WithSubDir("pkg/breakglass"))
		})
	}
}

// TestGenerateOtherNames tests other name generation functions.
func TestGenerateOtherNames(t *testing.T) {
	config := DefaultConfig()

	// Test signer name generation
	signerName := config.GenerateSignerName("test-namespace")
	expected := "hypershift.openshift.io/test-namespace.sre-break-glass"
	if signerName != expected {
		t.Errorf("Expected signer name '%s', got '%s'", expected, signerName)
	}

	// Test user name generation
	userName := config.GenerateUserName("test-cluster", "testuser")
	expected = "sre-breakglass-test-cluster-testuser"
	if userName != expected {
		t.Errorf("Expected user name '%s', got '%s'", expected, userName)
	}

	// Test common name generation
	commonName := config.GenerateCommonName("testuser")
	expected = "system:sre-break-glass:testuser"
	if commonName != expected {
		t.Errorf("Expected common name '%s', got '%s'", expected, commonName)
	}
}

// TestGenerateLabels tests label generation functionality.
func TestGenerateLabels(t *testing.T) {
	config := DefaultConfig()
	labels := config.GenerateLabels("test-cluster", "testuser")

	expectedLabels := map[string]string{
		"api.openshift.com/id":   "test-cluster",
		"api.openshift.com/name": "testuser",
		"api.openshift.com/type": "break-glass-credential",
	}

	if len(labels) != len(expectedLabels) {
		t.Errorf("Expected %d labels, got %d", len(expectedLabels), len(labels))
	}

	for key, expectedValue := range expectedLabels {
		if actualValue, exists := labels[key]; !exists {
			t.Errorf("Expected label key '%s' not found", key)
		} else if actualValue != expectedValue {
			t.Errorf("Label '%s': expected '%s', got '%s'", key, expectedValue, actualValue)
		}
	}
}

// TestGenerateLabelsGolden tests label generation using golden files.
func TestGenerateLabelsGolden(t *testing.T) {
	config := DefaultConfig()
	labels := config.GenerateLabels("production-cluster", "sre-admin")

	testutil.CompareWithFixture(t, labels, testutil.WithExtension(".json"), testutil.WithSubDir("pkg/breakglass"))
}

// TestRandomSuffixGeneration tests the random suffix generation functionality.
func TestRandomSuffixGeneration(t *testing.T) {
	testCases := []struct {
		name        string
		length      int
		expectError bool
		expectedLen int
	}{
		{
			name:        "zero length",
			length:      0,
			expectError: false,
			expectedLen: 0,
		},
		{
			name:        "normal length",
			length:      6,
			expectError: false,
			expectedLen: 6,
		},
		{
			name:        "max length",
			length:      MaxRandomSuffixLength,
			expectError: false,
			expectedLen: MaxRandomSuffixLength,
		},
		{
			name:        "negative length",
			length:      -1,
			expectError: false,
			expectedLen: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			suffix, err := generateRandomSuffix(tc.length)

			if tc.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(suffix) != tc.expectedLen {
				t.Errorf("Expected suffix length %d, got %d", tc.expectedLen, len(suffix))
			}

			// Check that suffix contains only valid characters
			if tc.expectedLen > 0 {
				validChars := "abcdefghijklmnopqrstuvwxyz0123456789"
				for _, char := range suffix {
					if !strings.ContainsRune(validChars, char) {
						t.Errorf("Invalid character in suffix: %c", char)
					}
				}
			}
		})
	}
}

// TestRandomSuffixUniqueness tests that random suffixes are actually random.
func TestRandomSuffixUniqueness(t *testing.T) {
	const iterations = 100
	const suffixLength = 6

	suffixes := make(map[string]bool)
	for i := 0; i < iterations; i++ {
		suffix, err := generateRandomSuffix(suffixLength)
		if err != nil {
			t.Fatalf("Failed to generate random suffix: %v", err)
		}

		if suffixes[suffix] {
			t.Errorf("Duplicate suffix generated: %s", suffix)
		}
		suffixes[suffix] = true
	}

	// We should have close to 100 unique suffixes (allowing for small chance of collision)
	if len(suffixes) < 95 {
		t.Errorf("Expected at least 95 unique suffixes, got %d", len(suffixes))
	}
}

// TestConfigSerialization tests that configuration can be serialized to/from JSON.
func TestConfigSerialization(t *testing.T) {
	original := DefaultConfig()

	// Serialize to JSON
	jsonData, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal config to JSON: %v", err)
	}

	// Deserialize from JSON
	var deserialized Config
	if err := json.Unmarshal(jsonData, &deserialized); err != nil {
		t.Fatalf("Failed to unmarshal config from JSON: %v", err)
	}

	// Compare key values
	if original.Services.KubeAPIServer != deserialized.Services.KubeAPIServer {
		t.Errorf("KubeAPIServer mismatch: original=%s, deserialized=%s",
			original.Services.KubeAPIServer, deserialized.Services.KubeAPIServer)
	}

	if original.CSR.Organization != deserialized.CSR.Organization {
		t.Errorf("Organization mismatch: original=%s, deserialized=%s",
			original.CSR.Organization, deserialized.CSR.Organization)
	}

	if original.Naming.AddTimestamp != deserialized.Naming.AddTimestamp {
		t.Errorf("AddTimestamp mismatch: original=%t, deserialized=%t",
			original.Naming.AddTimestamp, deserialized.Naming.AddTimestamp)
	}
}

// TestConfigSerializationGolden tests config serialization using golden files.
func TestConfigSerializationGolden(t *testing.T) {
	config := DefaultConfig()
	testutil.CompareWithFixture(t, config, testutil.WithExtension(".json"), testutil.WithSubDir("pkg/breakglass"))
}
