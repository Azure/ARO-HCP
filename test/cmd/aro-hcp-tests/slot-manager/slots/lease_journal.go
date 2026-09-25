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

package slots

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// LeaseJournal centralizes durable acquisition and best-effort release. Handlers
// resolve resource meaning; they cannot hide acquired names from cleanup.
type LeaseJournal struct {
	State     *AcquiredSlotState
	Persist   func() error
	Acquire   func(context.Context, string, time.Duration) (string, error)
	Return    func(context.Context, string, time.Duration) error
	Timeout   time.Duration
	attempted map[string]bool
}

func (j *LeaseJournal) AcquireAsset(ctx context.Context, kind AssetKind, inventory AssetInventory) (Lease, error) {
	if j.Timeout <= 0 {
		return Lease{}, errors.New("independent lease timeout must be positive")
	}
	bounded, cancel := context.WithTimeout(ctx, j.Timeout)
	defer cancel()
	name, err := j.Acquire(bounded, inventory.Pool.ResourceType, j.Timeout)
	if err != nil {
		return Lease{}, err
	}
	if err := ValidateLeasedResourceName(name); err != nil {
		return Lease{}, fmt.Errorf("asset lease acquisition returned an invalid name for type %q: %w", inventory.Pool.ResourceType, err)
	}
	if name == j.State.Leases.Primary.ResourceName {
		return Lease{}, fmt.Errorf("asset lease acquisition returned already journaled resource name %q", name)
	}
	for _, leases := range j.State.Leases.Assets {
		for _, existing := range leases {
			if existing.ResourceName == name {
				return Lease{}, fmt.Errorf("asset lease acquisition returned already journaled resource name %q", name)
			}
		}
	}
	lease := Lease{ResourceType: inventory.Pool.ResourceType, ResourceName: name}
	if j.State.Leases.Assets == nil {
		j.State.Leases.Assets = map[AssetKind][]Lease{}
	}
	j.State.Leases.Assets[kind] = append(j.State.Leases.Assets[kind], lease)
	// Persist even an unexpected name before resolving it: it is still a lease.
	if err := j.Persist(); err != nil {
		return lease, err
	}
	if !inventory.Contains(name) {
		return lease, fmt.Errorf("leased resource %q is not in asset pool %q", name, inventory.Pool.Name)
	}
	return lease, nil
}

func (j *LeaseJournal) ReleaseAsset(ctx context.Context, kind AssetKind) error {
	var errs []error
	leases := j.State.Leases.Assets[kind]
	for i := range leases {
		if err := j.release(ctx, &leases[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (j *LeaseJournal) ReleaseAll(ctx context.Context) error {
	var errs []error
	kinds := make([]string, 0, len(j.State.Leases.Assets))
	for kind := range j.State.Leases.Assets {
		kinds = append(kinds, string(kind))
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		errs = append(errs, j.ReleaseAsset(ctx, AssetKind(kind)))
	}
	errs = append(errs, j.release(ctx, &j.State.Leases.Primary))
	return errors.Join(errs...)
}

func (j *LeaseJournal) release(ctx context.Context, lease *Lease) error {
	if lease.ReturnState == "returned" {
		return nil
	}
	if j.attempted == nil {
		j.attempted = map[string]bool{}
	}
	if j.attempted[lease.ResourceName] {
		return nil
	}
	j.attempted[lease.ResourceName] = true
	if lease.ReturnState == "returning" {
		return fmt.Errorf("lease %q return outcome is uncertain; let Test Platform reconcile ownership", lease.ResourceName)
	}
	// An interrupted return must not cause a later invocation to return a name
	// which has already been handed to another consumer.
	lease.ReturnState = "returning"
	if err := j.Persist(); err != nil {
		lease.ReturnState = ""
		return fmt.Errorf("cannot journal return of lease %q; leave ownership to Test Platform: %w", lease.ResourceName, err)
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), j.Timeout)
	defer cancel()
	err := j.Return(bounded, lease.ResourceName, j.Timeout)
	if err == nil {
		lease.ReturnState = "returned"
	} else {
		var retryable *retryableLeaseProxyError
		if !errors.As(err, &retryable) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			lease.ReturnState = ""
		}
	}
	persistErr := j.Persist()
	if err != nil || persistErr != nil {
		return fmt.Errorf("returning lease %q: %w", lease.ResourceName, errors.Join(err, persistErr))
	}
	return nil
}
