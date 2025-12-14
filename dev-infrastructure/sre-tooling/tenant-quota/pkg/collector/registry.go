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

package collector

import (
	"fmt"
	"sort"
	"sync"

	"github.com/Azure/ARO-HCP/dev-infrastructure/sre-tooling/tenant-quota/pkg/config"
)

type CollectorFunc = config.CollectorFunc

type CollectorContext = config.CollectorContext

// registry holds all registered built-in collectors
type registry struct {
	mu         sync.RWMutex
	collectors map[string]CollectorFunc
}

var globalRegistry = &registry{
	collectors: make(map[string]CollectorFunc),
}

func Register(name string, collector CollectorFunc) error {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	if _, exists := globalRegistry.collectors[name]; exists {
		return fmt.Errorf("collector %s already registered", name)
	}

	globalRegistry.collectors[name] = collector
	return nil
}

func Lookup(name string) (CollectorFunc, bool) {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	collector, ok := globalRegistry.collectors[name]
	return collector, ok
}

func List() []string {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	names := make([]string, 0, len(globalRegistry.collectors))
	for name := range globalRegistry.collectors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
