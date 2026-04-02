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

package controllerutils

import (
	"fmt"
	"regexp"
	"time"
)

// InertiaCondition pairs a regex pattern with a duration for matching controller names.
// Controllers whose names match the regex must have been degraded for at least the
// specified duration before they are included in the degraded summary.
type InertiaCondition struct {
	// ControllerNameMatcher is a regex pattern that matches controller names.
	ControllerNameMatcher *regexp.Regexp
	// Duration is how long the controller must be degraded before it shows up in the summary.
	Duration time.Duration
}

// InertiaConfig holds the configuration for condition inertia.
// It allows different controllers to have different grace periods before their
// degraded state is reported in the summary condition.
type InertiaConfig struct {
	defaultDuration time.Duration
	conditions      []InertiaCondition
}

// NewInertia creates a new InertiaConfig with a default duration and optional conditions.
// Conditions are evaluated in order; the first match wins.
func NewInertia(defaultDuration time.Duration, conditions ...InertiaCondition) (*InertiaConfig, error) {
	for i, c := range conditions {
		if c.ControllerNameMatcher == nil {
			return nil, fmt.Errorf("condition %d has a nil ControllerNameMatcher", i)
		}
	}
	return &InertiaConfig{
		defaultDuration: defaultDuration,
		conditions:      conditions,
	}, nil
}

// MustNewInertia creates a new InertiaConfig, panicking on error.
func MustNewInertia(defaultDuration time.Duration, conditions ...InertiaCondition) *InertiaConfig {
	config, err := NewInertia(defaultDuration, conditions...)
	if err != nil {
		panic(err)
	}
	return config
}

// Inertia returns the inertia duration for the given controller name.
// Conditions are evaluated in order; the first matching regex wins.
// If no conditions match, the default duration is returned.
func (c *InertiaConfig) Inertia(controllerName string) time.Duration {
	for _, condition := range c.conditions {
		if condition.ControllerNameMatcher.MatchString(controllerName) {
			return condition.Duration
		}
	}
	return c.defaultDuration
}
