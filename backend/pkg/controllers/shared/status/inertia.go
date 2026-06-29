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

package status

import (
	"fmt"
	"regexp"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Inertia returns how long an off-default condition (e.g. Degraded=True)
// from a given controller must persist before it is propagated into the
// aggregated parent condition. The controller name is the key so different
// controllers can have different inertia; the metav1.Condition is included
// so callers can additionally vary inertia by reason or message if they
// ever need to.
//
// This mirrors openshift/library-go's status.Inertia, adapted so the keying
// is the producing controller rather than the condition type — every
// controller in this codebase reports under Type="Degraded", so it is the
// controller identity (not the condition type) that carries the signal.
type Inertia func(controllerName string, condition metav1.Condition) time.Duration

// InertiaController configures an inertia duration for a set of source
// controllers whose names match a regular expression. Patterns are checked
// in declaration order and the first match wins, so place more specific
// patterns before more general ones.
type InertiaController struct {
	ControllerNameMatcher *regexp.Regexp
	Duration              time.Duration
}

// InertiaConfig is an Inertia implementation: a default duration plus a list
// of per-controller-name overrides.
type InertiaConfig struct {
	defaultDuration time.Duration
	controllers     []InertiaController
}

// NewInertia builds an InertiaConfig. The default duration is applied to any
// controller whose name does not match an override pattern.
func NewInertia(defaultDuration time.Duration, controllers ...InertiaController) (*InertiaConfig, error) {
	for i, c := range controllers {
		if c.ControllerNameMatcher == nil {
			return nil, fmt.Errorf("controller override %d has a nil ControllerNameMatcher", i)
		}
	}
	return &InertiaConfig{
		defaultDuration: defaultDuration,
		controllers:     controllers,
	}, nil
}

// MustNewInertia is like NewInertia but panics on configuration error. Use
// it at process startup where a misconfigured Inertia is a coding bug.
func MustNewInertia(defaultDuration time.Duration, controllers ...InertiaController) *InertiaConfig {
	cfg, err := NewInertia(defaultDuration, controllers...)
	if err != nil {
		panic(err)
	}
	return cfg
}

// Inertia returns the configured duration for the given source controller.
func (c *InertiaConfig) Inertia(controllerName string, _ metav1.Condition) time.Duration {
	for _, override := range c.controllers {
		if override.ControllerNameMatcher.MatchString(controllerName) {
			return override.Duration
		}
	}
	return c.defaultDuration
}

// DefaultInertia is the default per-controller inertia for the aggregators
// in this package. A controller that flaps a Degraded=True flag for less
// than this duration is not propagated up to its parent.
const DefaultInertia = 30 * time.Second
