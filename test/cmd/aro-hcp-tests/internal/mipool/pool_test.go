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
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name       string
		containers string
		wantSize   int
		wantErr    bool
	}{
		{
			name:       "single container",
			containers: "rg-00",
			wantSize:   1,
		},
		{
			name:       "multiple containers",
			containers: "rg-00 rg-01 rg-02",
			wantSize:   3,
		},
		{
			name:       "extra whitespace",
			containers: "  rg-00  rg-01  ",
			wantSize:   2,
		},
		{
			name:       "empty string",
			containers: "",
			wantErr:    true,
		},
		{
			name:       "whitespace only",
			containers: "   ",
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(tt.containers)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := p.Size(); got != tt.wantSize {
				t.Fatalf("Size() = %d, want %d", got, tt.wantSize)
			}
			if got := p.Free(); got != tt.wantSize {
				t.Fatalf("Free() = %d, want %d (all should be free initially)", got, tt.wantSize)
			}
		})
	}
}

func TestAllocateAndRelease(t *testing.T) {
	p, err := New("rg-00 rg-01 rg-02 rg-03 rg-04")
	if err != nil {
		t.Fatal(err)
	}

	got, err := p.Allocate("test-a", 2)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Allocate returned %d containers, want 2", len(got))
	}
	if p.Free() != 3 {
		t.Fatalf("Free() = %d after allocating 2 from 5, want 3", p.Free())
	}

	got2, err := p.Allocate("test-b", 3)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if len(got2) != 3 {
		t.Fatalf("Allocate returned %d containers, want 3", len(got2))
	}
	if p.Free() != 0 {
		t.Fatalf("Free() = %d after allocating all, want 0", p.Free())
	}

	if err := p.Release("test-a"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if p.Free() != 2 {
		t.Fatalf("Free() = %d after releasing test-a (2 containers), want 2", p.Free())
	}

	if err := p.Release("test-b"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if p.Free() != 5 {
		t.Fatalf("Free() = %d after releasing all, want 5", p.Free())
	}
}

func TestAllocateZero(t *testing.T) {
	p, err := New("rg-00 rg-01")
	if err != nil {
		t.Fatal(err)
	}

	got, err := p.Allocate("test-zero", 0)
	if err != nil {
		t.Fatalf("Allocate(0): %v", err)
	}
	if got != nil {
		t.Fatalf("Allocate(0) = %v, want nil", got)
	}
	if p.Free() != 2 {
		t.Fatalf("Free() = %d, want 2 (zero alloc should not consume)", p.Free())
	}
}

func TestAllocateExceedsCapacity(t *testing.T) {
	p, err := New("rg-00 rg-01")
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.Allocate("test-big", 3)
	if err == nil {
		t.Fatal("expected error when allocating more than available, got nil")
	}
}

func TestAllocateDuplicate(t *testing.T) {
	p, err := New("rg-00 rg-01 rg-02")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Allocate("test-dup", 1); err != nil {
		t.Fatal(err)
	}

	_, err = p.Allocate("test-dup", 1)
	if err == nil {
		t.Fatal("expected error on duplicate allocation, got nil")
	}
}

func TestReleaseUnknownTest(t *testing.T) {
	p, err := New("rg-00")
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Release("nonexistent"); err == nil {
		t.Fatal("expected error releasing unknown test, got nil")
	}
}

func TestAllocated(t *testing.T) {
	p, err := New("rg-00 rg-01 rg-02")
	if err != nil {
		t.Fatal(err)
	}

	if got := p.Allocated("test-x"); got != nil {
		t.Fatalf("Allocated for unknown test = %v, want nil", got)
	}

	allocated, _ := p.Allocate("test-x", 2)
	got := p.Allocated("test-x")
	if len(got) != len(allocated) {
		t.Fatalf("Allocated() = %v, want %v", got, allocated)
	}
}

func TestAllocateReturnIsIndependent(t *testing.T) {
	p, err := New("rg-00 rg-01")
	if err != nil {
		t.Fatal(err)
	}

	got, err := p.Allocate("test-a", 2)
	if err != nil {
		t.Fatal(err)
	}
	got[0] = "mutated"

	allocated := p.Allocated("test-a")
	if len(allocated) != 2 || allocated[0] != "rg-00" || allocated[1] != "rg-01" {
		t.Fatalf("internal allocation mutated via Allocate return: %v", allocated)
	}
	if env := p.FormatEnv("test-a"); env != "rg-00 rg-01" {
		t.Fatalf("FormatEnv() = %q after mutating Allocate return, want %q", env, "rg-00 rg-01")
	}
	if err := p.Release("test-a"); err != nil {
		t.Fatal(err)
	}
	if p.Free() != 2 {
		t.Fatalf("Free() = %d after release, want 2", p.Free())
	}
}

func TestAllocatedReturnIsIndependent(t *testing.T) {
	p, err := New("rg-00 rg-01")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Allocate("test-a", 2); err != nil {
		t.Fatal(err)
	}

	got := p.Allocated("test-a")
	got[0] = "mutated"

	allocated := p.Allocated("test-a")
	if len(allocated) != 2 || allocated[0] != "rg-00" || allocated[1] != "rg-01" {
		t.Fatalf("internal allocation mutated via Allocated return: %v", allocated)
	}
}

func TestFormatEnv(t *testing.T) {
	p, err := New("rg-00 rg-01 rg-02")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Allocate("test-fmt", 2); err != nil {
		t.Fatal(err)
	}

	env := p.FormatEnv("test-fmt")
	parts := strings.Fields(env)
	if len(parts) != 2 {
		t.Fatalf("FormatEnv() = %q, want 2 space-separated entries", env)
	}

	if got := p.FormatEnv("unknown"); got != "" {
		t.Fatalf("FormatEnv for unknown test = %q, want empty", got)
	}
}

func TestAllContainers(t *testing.T) {
	p, err := New("rg-00 rg-01 rg-02")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Allocate("test-a", 1); err != nil {
		t.Fatal(err)
	}

	all := p.AllContainers()
	sort.Strings(all)
	want := []string{"rg-00", "rg-01", "rg-02"}
	sort.Strings(want)
	if len(all) != len(want) {
		t.Fatalf("AllContainers() = %v, want %v", all, want)
	}
	for i := range want {
		if all[i] != want[i] {
			t.Fatalf("AllContainers()[%d] = %q, want %q", i, all[i], want[i])
		}
	}
}

func TestReleaseWithCleanup(t *testing.T) {
	p, err := New("rg-00 rg-01 rg-02")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Allocate("test-cleanup", 2); err != nil {
		t.Fatal(err)
	}

	var cleaned []string
	err = p.ReleaseWithCleanup("test-cleanup", func(rg string) error {
		cleaned = append(cleaned, rg)
		return nil
	})
	if err != nil {
		t.Fatalf("ReleaseWithCleanup: %v", err)
	}
	if len(cleaned) != 2 {
		t.Fatalf("cleanup called %d times, want 2", len(cleaned))
	}
	if p.Free() != 3 {
		t.Fatalf("Free() = %d after release, want 3", p.Free())
	}
}

func TestReleaseWithCleanupError(t *testing.T) {
	p, err := New("rg-00 rg-01")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Allocate("test-err", 2); err != nil {
		t.Fatal(err)
	}

	err = p.ReleaseWithCleanup("test-err", func(rg string) error {
		if rg == "rg-00" {
			return fmt.Errorf("simulated failure")
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected error from cleanup failure, got nil")
	}
	if p.Free() != 2 {
		t.Fatalf("Free() = %d, want 2 (containers returned despite cleanup error)", p.Free())
	}
}

func TestReleaseWithCleanupUnknown(t *testing.T) {
	p, err := New("rg-00")
	if err != nil {
		t.Fatal(err)
	}

	err = p.ReleaseWithCleanup("nonexistent", func(string) error { return nil })
	if err == nil {
		t.Fatal("expected error for unknown test, got nil")
	}
}

func TestReleaseWithCleanupConcurrentWithRelease(t *testing.T) {
	p, err := New("rg-00 rg-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Allocate("test-a", 2); err != nil {
		t.Fatal(err)
	}

	inCleanup := make(chan struct{})
	proceed := make(chan struct{})
	closeProceed := sync.OnceFunc(func() { close(proceed) })
	t.Cleanup(closeProceed)
	done := make(chan error, 1)

	go func() {
		done <- p.ReleaseWithCleanup("test-a", func(string) error {
			select {
			case <-inCleanup:
			default:
				close(inCleanup)
			}
			<-proceed
			return nil
		})
	}()

	<-inCleanup
	if got := p.Size(); got != 2 {
		t.Fatalf("Size() = %d during cleanup, want 2", got)
	}
	if got := p.Free(); got != 0 {
		t.Fatalf("Free() = %d during cleanup, want 0", got)
	}
	if all := p.AllContainers(); len(all) != 2 {
		t.Fatalf("AllContainers() = %v during cleanup, want 2 entries", all)
	}
	if got := p.Allocated("test-a"); got != nil {
		t.Fatalf("Allocated() = %v during cleanup, want nil", got)
	}
	releaseErr := p.Release("test-a")
	closeProceed()
	if cleanupErr := <-done; cleanupErr != nil {
		t.Fatalf("ReleaseWithCleanup: %v", cleanupErr)
	}
	if releaseErr == nil {
		t.Fatal("expected Release to fail while ReleaseWithCleanup owns the allocation")
	}
	if got := p.Free(); got != 2 {
		t.Fatalf("Free() = %d, want 2 (double-release would yield 4)", got)
	}
	if got := p.Size(); got != 2 {
		t.Fatalf("Size() = %d, want 2", got)
	}
}

func TestConcurrentAllocateRelease(t *testing.T) {
	p, err := New("rg-00 rg-01 rg-02 rg-03 rg-04 rg-05 rg-06 rg-07 rg-08 rg-09")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := "test-" + string(rune('a'+idx))
			containers, err := p.Allocate(name, 1)
			if err != nil {
				errs <- err
				return
			}
			if len(containers) != 1 {
				errs <- fmt.Errorf("test %s: got %d containers, want 1", name, len(containers))
				return
			}
			if err := p.Release(name); err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}

	if got := p.Free(); got != 10 {
		t.Fatalf("Free() = %d after concurrent alloc/release of 10, want 10", got)
	}
}
