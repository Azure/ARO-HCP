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

package mipool

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// AssignedMIContainersEnvvar is the environment variable the parent sets on
// each child process to communicate which MI containers are assigned to it.
// The value is a space-separated list of container resource group names.
const AssignedMIContainersEnvvar = "ASSIGNED_MI_CONTAINERS"

// Pool manages a fixed set of MI container resource groups. It tracks which
// containers are free and which are assigned to a running test. All methods
// are safe for concurrent use.
//
// Pool is designed to be used by the parent (run-suite) process only. The
// mutex protects the free/assigned bookkeeping but is never held during
// network calls — callers must perform Azure cleanup outside of Allocate/Release.
type Pool struct {
	mu       sync.Mutex
	free     []string
	assigned map[string][]string // testName -> container RG names
}

// New creates a pool from the space-separated container resource group names
// in the LEASED_MSI_CONTAINERS environment variable value. Returns an error
// if containers is empty.
func New(containers string) (*Pool, error) {
	names := strings.Fields(containers)
	if len(names) == 0 {
		return nil, fmt.Errorf("no MI containers provided")
	}
	free := make([]string, len(names))
	copy(free, names)
	return &Pool{
		free:     free,
		assigned: make(map[string][]string),
	}, nil
}

// Allocate reserves n containers for the named test. It returns the container
// resource group names. Returns an error if n containers are not available or
// if the test already has an allocation.
func (p *Pool) Allocate(testName string, n int) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.assigned[testName]; exists {
		return nil, fmt.Errorf("test %q already has an allocation", testName)
	}
	if n <= 0 {
		return nil, nil
	}
	if n > len(p.free) {
		return nil, fmt.Errorf("requested %d containers but only %d free", n, len(p.free))
	}

	allocated := make([]string, n)
	copy(allocated, p.free[:n])
	p.free = p.free[n:]
	p.assigned[testName] = allocated
	return allocated, nil
}

// Release returns the containers assigned to testName back to the free pool.
// Callers must complete any Azure cleanup before calling Release — the
// containers become immediately available for reuse.
// Returns an error if testName has no allocation.
func (p *Pool) Release(testName string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	containers, exists := p.assigned[testName]
	if !exists {
		return fmt.Errorf("test %q has no allocation to release", testName)
	}
	p.free = append(p.free, containers...)
	delete(p.assigned, testName)
	return nil
}

// ReleaseWithCleanup runs cleanupFn on each container assigned to testName,
// then returns all containers to the free pool. Cleanup errors are collected
// but do not prevent the containers from being returned — a best-effort
// approach that avoids permanently leaking pool capacity. The next startup
// sweep (CleanupAllContainers) will catch any residual state left by a
// failed cleanup.
func (p *Pool) ReleaseWithCleanup(testName string, cleanupFn func(resourceGroup string) error) error {
	p.mu.Lock()
	containers, exists := p.assigned[testName]
	p.mu.Unlock()

	if !exists {
		return fmt.Errorf("test %q has no allocation to release", testName)
	}

	var errs []error
	for _, rg := range containers {
		if err := cleanupFn(rg); err != nil {
			errs = append(errs, fmt.Errorf("cleanup %s: %w", rg, err))
		}
	}

	p.mu.Lock()
	p.free = append(p.free, containers...)
	delete(p.assigned, testName)
	p.mu.Unlock()

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors for %s: %w", testName, errors.Join(errs...))
	}
	return nil
}

// Free returns the number of currently available containers.
func (p *Pool) Free() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.free)
}

// Size returns the total number of containers in the pool.
func (p *Pool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.free) + p.assignedCount()
}

func (p *Pool) assignedCount() int {
	n := 0
	for _, v := range p.assigned {
		n += len(v)
	}
	return n
}

// Allocated returns the containers currently assigned to testName, or nil
// if the test has no allocation.
func (p *Pool) Allocated(testName string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.assigned[testName]
}

// AllContainers returns every container resource group name in the pool,
// regardless of assignment state.
func (p *Pool) AllContainers() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	all := make([]string, 0, len(p.free)+p.assignedCount())
	all = append(all, p.free...)
	for _, v := range p.assigned {
		all = append(all, v...)
	}
	return all
}

// FormatEnv formats the allocated containers for testName as a space-separated
// string suitable for setting as the ASSIGNED_MI_CONTAINERS environment variable.
func (p *Pool) FormatEnv(testName string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.assigned[testName], " ")
}
