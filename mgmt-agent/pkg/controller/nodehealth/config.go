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

// Package nodehealth implements a management-cluster controller that detects
// "Ready but broken" nodes from kubelet Events and labels them so they can be
// acted on. A separate mitigation controller acts on the labeled nodes and owns
// any disruptive action (cordon, taint, evict, delete).
//
// Detection is hard-coded and modular: each fault family is a Go detector that
// reuses a shared toolkit (event-signature match, a sustained-storm floor, and
// the load-bearing zero-successful-start check) and is a pure function of the
// node's current state, so it is exhaustively unit-tested with no API server.
// The only runtime configuration is the single operational switch the rollout
// needs, enabled; there is no config-driven detection engine.
package nodehealth

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// Config holds the node-health controller's operational switch, delivered
// via a watched ConfigMap so it can be flipped without a redeploy. Detection
// logic, thresholds, and the SWIFT-v2 node scoping are hard-coded, not
// configured.
type Config struct {
	// Enabled is a hard off switch. When false the controller still runs its
	// informers but records no state, enqueues nothing, and takes no action.
	Enabled bool `json:"enabled"`
}

// Default returns the built-in configuration: the controller disabled. Enabling
// the controller is an explicit, per-environment decision.
func Default() Config {
	return Config{
		Enabled: false,
	}
}

// Parse unmarshals a YAML config document over the defaults and validates it.
func Parse(data []byte) (Config, error) {
	cfg := Default()
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("failed to unmarshal node-health config: %w", err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks the configuration. The only field is a plain boolean, so the
// only invalid state is unrepresentable; Validate exists so callers have a single,
// stable contract and so future switches can add checks here.
func (c *Config) Validate() error {
	return nil
}
