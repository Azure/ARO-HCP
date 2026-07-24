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

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Azure/ARO-HCP/dev-infrastructure/scripts/internal/akslog"
)

const (
	defaultMinCount = 1
	defaultDrainMin = 10
	defaultReadyMin = 5
)

type config struct {
	clusterName       string
	resourceGroup     string
	subscriptionID    string
	poolTag           string
	poolBaseName      string
	poolCount         int
	zoneRedundantMode string
	poolZones         []string
	poolMinCount      int
	dryRun            bool
	drainTimeoutMin   int
	readyTimeoutMin   int
}

func parseEnvConfig(env func(string) string) (*config, error) {
	c := &config{
		poolMinCount:    defaultMinCount,
		drainTimeoutMin: defaultDrainMin,
		readyTimeoutMin: defaultReadyMin,
		dryRun:          true, // safe default: never write without explicit opt-in
	}

	required := []struct {
		key  string
		dest *string
	}{
		{"CLUSTER_NAME", &c.clusterName},
		{"RESOURCE_GROUP", &c.resourceGroup},
		{"SUBSCRIPTION_ID", &c.subscriptionID},
		{"POOL_TAG", &c.poolTag},
		{"POOL_BASE_NAME", &c.poolBaseName},
	}
	for _, r := range required {
		v := strings.TrimSpace(env(r.key))
		if v == "" {
			return nil, fmt.Errorf("%s is required", r.key)
		}
		*r.dest = v
	}

	poolCountStr := strings.TrimSpace(env("POOL_COUNT"))
	if poolCountStr == "" {
		return nil, fmt.Errorf("POOL_COUNT is required")
	}
	n, err := strconv.Atoi(poolCountStr)
	if err != nil {
		return nil, fmt.Errorf("POOL_COUNT: %w", err)
	}
	if n <= 0 {
		return nil, fmt.Errorf("POOL_COUNT must be > 0, got %d", n)
	}
	c.poolCount = n

	zrm := strings.TrimSpace(env("ZONE_REDUNDANT_MODE"))
	if zrm == "" {
		return nil, fmt.Errorf("ZONE_REDUNDANT_MODE is required")
	}
	switch zrm {
	case "Enabled", "Auto", "Disabled":
	default:
		return nil, fmt.Errorf("ZONE_REDUNDANT_MODE must be Enabled, Auto, or Disabled; got %q", zrm)
	}
	c.zoneRedundantMode = zrm

	if v := strings.TrimSpace(env("POOL_ZONES")); v != "" {
		for _, z := range strings.Split(v, ",") {
			z = strings.TrimSpace(z)
			if z != "" {
				c.poolZones = append(c.poolZones, z)
			}
		}
	}

	if v := strings.TrimSpace(env("POOL_MIN_COUNT")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("POOL_MIN_COUNT: %w", err)
		}
		if n <= 0 {
			return nil, fmt.Errorf("POOL_MIN_COUNT must be > 0, got %d", n)
		}
		c.poolMinCount = n
	}

	if v := strings.TrimSpace(env("DRAIN_TIMEOUT_MIN")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("DRAIN_TIMEOUT_MIN: %w", err)
		}
		if n <= 0 {
			return nil, fmt.Errorf("DRAIN_TIMEOUT_MIN must be > 0, got %d", n)
		}
		c.drainTimeoutMin = n
	}

	if v := strings.TrimSpace(env("READY_TIMEOUT_MIN")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("READY_TIMEOUT_MIN: %w", err)
		}
		if n <= 0 {
			return nil, fmt.Errorf("READY_TIMEOUT_MIN must be > 0, got %d", n)
		}
		c.readyTimeoutMin = n
	}

	// DRY_RUN: only opt into live mode when explicitly set to false/0/no.
	// Everything else (including unset) keeps the safe default of true.
	if v := strings.ToLower(strings.TrimSpace(env("DRY_RUN"))); v == "false" || v == "0" || v == "no" {
		c.dryRun = false
	}

	return c, nil
}

func loadConfig() (*config, error) {
	return parseEnvConfig(os.Getenv)
}

func (c *config) logEnv() {
	akslog.Logf("CLUSTER_NAME=%s", c.clusterName)
	akslog.Logf("RESOURCE_GROUP=%s", c.resourceGroup)
	akslog.Logf("SUBSCRIPTION_ID=%s", c.subscriptionID)
	akslog.Logf("POOL_TAG=%s", c.poolTag)
	akslog.Logf("POOL_BASE_NAME=%s", c.poolBaseName)
	akslog.Logf("POOL_COUNT=%d", c.poolCount)
	akslog.Logf("ZONE_REDUNDANT_MODE=%s", c.zoneRedundantMode)
	akslog.Logf("POOL_ZONES=%v", c.poolZones)
	akslog.Logf("POOL_MIN_COUNT=%d", c.poolMinCount)
	akslog.Logf("DRY_RUN=%t", c.dryRun)
	akslog.Logf("DRAIN_TIMEOUT_MIN=%d", c.drainTimeoutMin)
	akslog.Logf("READY_TIMEOUT_MIN=%d", c.readyTimeoutMin)
}
