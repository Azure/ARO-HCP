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

package nodemitigation

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"sigs.k8s.io/yaml"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const ControllerName = "node-mitigation"

type Mode string

const (
	Disabled Mode = "disabled"
	Audit    Mode = "audit"
	Enforce  Mode = "enforce"
)

type WorkloadPolicy struct {
	NamespaceSelector    metav1.LabelSelector `json:"namespaceSelector"`
	PodSelector          metav1.LabelSelector `json:"podSelector"`
	DeploymentSelector   metav1.LabelSelector `json:"deploymentSelector"`
	AllowEmptyDir        bool                 `json:"allowEmptyDir"`
	MinAvailableReplicas *int32               `json:"minAvailableReplicas"`
}

type Config struct {
	Mode                    Mode              `json:"mode"`
	ClusterResourceID       string            `json:"clusterResourceID"`
	Mitigators              []string          `json:"mitigators"`
	Window                  metav1.Duration   `json:"window"`
	RetryInterval           metav1.Duration   `json:"retryInterval"`
	ObservationMaxAge       metav1.Duration   `json:"observationMaxAge"`
	EvictionWindow          metav1.Duration   `json:"evictionWindow"`
	EvictionCooldown        metav1.Duration   `json:"evictionCooldown"`
	MaxEvictionsPerWorkload int               `json:"maxEvictionsPerWorkload"`
	MaxEvictionsPerNode     int               `json:"maxEvictionsPerNode"`
	MaxUnavailableCluster   int               `json:"maxUnavailableCluster"`
	MaxUnavailablePool      int               `json:"maxUnavailablePool"`
	MaxUnavailableZone      int               `json:"maxUnavailableZone"`
	MinHealthyPool          int               `json:"minHealthyPool"`
	MinHealthyZone          int               `json:"minHealthyZone"`
	Workloads               []WorkloadPolicy  `json:"workloads"`
	DisposableDaemonSets    []DaemonSetPolicy `json:"disposableDaemonSets"`
}

func Default() Config { return Config{Mode: Disabled} }

func Parse(data []byte) (Config, error) {
	if len(data) > 64*1024 {
		return Config{}, fmt.Errorf("mitigation configuration exceeds 64 KiB")
	}
	cfg := Default()
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse node-mitigation config: %w", err)
	}
	return cfg, cfg.Validate()
}

func (cfg Config) Validate() error {
	if cfg.Mode != Disabled && cfg.Mode != Audit && cfg.Mode != Enforce {
		return fmt.Errorf("unknown mitigation mode %q", cfg.Mode)
	}
	if cfg.Mode == Disabled {
		return nil
	}
	id, err := azcorearm.ParseResourceID(cfg.ClusterResourceID)
	if err != nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.ContainerService/managedClusters") || id.SubscriptionID == "" {
		return fmt.Errorf("clusterResourceID must identify an AKS managed cluster")
	}
	if len(cfg.Mitigators) == 0 {
		return fmt.Errorf("at least one mitigator must be explicitly enabled")
	}
	seen := map[string]bool{}
	for _, name := range cfg.Mitigators {
		if !slices.Contains([]string{"swift", "never-ready"}, name) || seen[name] {
			return fmt.Errorf("unknown or duplicate mitigator %q", name)
		}
		seen[name] = true
	}
	if cfg.Window.Duration <= 0 || cfg.RetryInterval.Duration <= 0 ||
		cfg.ObservationMaxAge.Duration < cfg.RetryInterval.Duration ||
		cfg.ObservationMaxAge.Duration >= cfg.Window.Duration {
		return fmt.Errorf("require positive window/retry and retry <= observationMaxAge < window")
	}
	if slices.Contains(cfg.Mitigators, "swift") && (cfg.EvictionWindow.Duration <= 0 ||
		cfg.EvictionCooldown.Duration < cfg.RetryInterval.Duration || cfg.EvictionCooldown.Duration > cfg.EvictionWindow.Duration ||
		cfg.MaxEvictionsPerWorkload < 1 || cfg.MaxEvictionsPerNode < 1) {
		return fmt.Errorf("SWIFT requires positive eviction limits and retry <= evictionCooldown <= evictionWindow")
	}
	if cfg.MaxUnavailableCluster < 1 || cfg.MaxUnavailablePool < 1 ||
		cfg.MaxUnavailableZone < 1 || cfg.MinHealthyPool < 1 || cfg.MinHealthyZone < 1 {
		return fmt.Errorf("unavailable limits and healthy floors must be explicitly positive")
	}
	if len(cfg.Workloads) > 64 {
		return fmt.Errorf("workload policy count exceeds the supported bound")
	}
	for _, policy := range cfg.Workloads {
		if policy.MinAvailableReplicas == nil || *policy.MinAvailableReplicas < 0 {
			return fmt.Errorf("workload policies require an explicit nonnegative minAvailableReplicas")
		}
		for _, selector := range []metav1.LabelSelector{policy.NamespaceSelector, policy.PodSelector, policy.DeploymentSelector} {
			if len(selector.MatchLabels) == 0 && len(selector.MatchExpressions) == 0 {
				return fmt.Errorf("workload policies require nonempty namespace, pod and deployment selectors")
			}
			if _, err := metav1.LabelSelectorAsSelector(&selector); err != nil {
				return fmt.Errorf("invalid workload selector: %w", err)
			}
		}
	}
	if len(cfg.DisposableDaemonSets) > 64 {
		return fmt.Errorf("disposable DaemonSet policy count exceeds the supported bound")
	}
	approved := map[string]bool{}
	for _, policy := range cfg.DisposableDaemonSets {
		key := policy.Namespace + "/" + policy.Name
		if len(validation.IsDNS1123Label(policy.Namespace)) != 0 ||
			len(validation.IsDNS1123Subdomain(policy.Name)) != 0 || policy.UID == "" || approved[key] {
			return fmt.Errorf("disposable DaemonSet policies require unique namespace/name and a UID")
		}
		approved[key] = true
		if !slices.Contains([]string{"networking", "logging", "monitoring"}, policy.Role) {
			return fmt.Errorf("disposable DaemonSet role must be networking, logging or monitoring")
		}
		for _, digest := range []string{policy.TemplateSHA256, policy.PodSpecSHA256} {
			decoded, err := hex.DecodeString(digest)
			if err != nil || len(decoded) != 32 || digest != strings.ToLower(digest) {
				return fmt.Errorf("disposable DaemonSet policies require lowercase template and Pod spec SHA256 digests")
			}
		}
	}
	return nil
}

func (cfg Config) retryInterval() time.Duration {
	if cfg.RetryInterval.Duration > 0 {
		return cfg.RetryInterval.Duration
	}
	return 30 * time.Second
}
