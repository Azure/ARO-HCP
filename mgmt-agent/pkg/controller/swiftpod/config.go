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

package swiftpod

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/yaml"
)

const (
	ControllerName = "swift-pod-mitigation"
	ConfigMapName  = "mgmt-agent-swift-pod-mitigation"
	ConfigKey      = "config.yaml"
)

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
	Mode                    Mode            `json:"mode"`
	RetryInterval           metav1.Duration `json:"retryInterval"`
	ObservationMaxAge       metav1.Duration `json:"observationMaxAge"`
	EvictionWindow          metav1.Duration `json:"evictionWindow"`
	EvictionCooldown        metav1.Duration `json:"evictionCooldown"`
	MaxEvictionsPerWorkload int             `json:"maxEvictionsPerWorkload"`
	MaxEvictionsPerNode     int             `json:"maxEvictionsPerNode"`
	Workload                WorkloadPolicy  `json:"workload"`
}

func Default() Config { return Config{Mode: Disabled} }

func Parse(data []byte) (Config, error) {
	if len(data) > 64*1024 {
		return Config{}, fmt.Errorf("SWIFT configuration exceeds 64 KiB")
	}
	cfg := Default()
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse SWIFT configuration: %w", err)
	}
	return cfg, cfg.Validate()
}

func (cfg Config) Validate() error {
	if cfg.Mode != Disabled && cfg.Mode != Audit && cfg.Mode != Enforce {
		return fmt.Errorf("unknown SWIFT mitigation mode %q", cfg.Mode)
	}
	if cfg.Mode == Disabled {
		return nil
	}
	if cfg.RetryInterval.Duration <= 0 || cfg.ObservationMaxAge.Duration < cfg.RetryInterval.Duration {
		return fmt.Errorf("require positive retry and retry <= observationMaxAge")
	}
	if cfg.EvictionWindow.Duration <= 0 ||
		cfg.EvictionCooldown.Duration < cfg.RetryInterval.Duration || cfg.EvictionCooldown.Duration > cfg.EvictionWindow.Duration ||
		cfg.MaxEvictionsPerWorkload < 1 || cfg.MaxEvictionsPerNode < 1 {
		return fmt.Errorf("require positive eviction limits and retry <= evictionCooldown <= evictionWindow")
	}
	if cfg.Workload.MinAvailableReplicas == nil || *cfg.Workload.MinAvailableReplicas < 0 {
		return fmt.Errorf("require explicit nonnegative minAvailableReplicas")
	}
	for _, selector := range []metav1.LabelSelector{cfg.Workload.NamespaceSelector, cfg.Workload.PodSelector, cfg.Workload.DeploymentSelector} {
		if len(selector.MatchLabels) == 0 && len(selector.MatchExpressions) == 0 {
			return fmt.Errorf("require nonempty namespace, pod and deployment selectors")
		}
		if _, err := metav1.LabelSelectorAsSelector(&selector); err != nil {
			return fmt.Errorf("invalid workload selector: %w", err)
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
