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

package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/providers"
)

type providerFunc func(context.Context, providers.CheckRequest) (providers.Result, error)

type validatedProvider struct {
	providerFunc
	providers.UpdateValidator
}

func (f providerFunc) Check(ctx context.Context, req providers.CheckRequest) (providers.Result, error) {
	return f(ctx, req)
}

func fixed(p providers.VersionProvider) ProviderFactory {
	return func(string) (providers.VersionProvider, error) {
		return p, nil
	}
}

func setup(t *testing.T) (string, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "versions.yaml"), []byte("istio: asm-1-29\naks: '1.35'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, &config.Config{Components: map[string]config.ComponentConfig{
		"istio": {
			Provider: "azure-istio", Locations: []string{"all"}, KubernetesVersionPath: "aks",
			Targets: []config.Target{{FilePath: "versions.yaml", JsonPath: "istio"}},
		},
	}}
}

func eligible(_ context.Context, _ providers.CheckRequest) (providers.Result, error) {
	return providers.Result{Next: "asm-1-30", Detail: "compatible"}, nil
}

func TestCheckIsReadOnlyAndUpdateIsLocal(t *testing.T) {
	dir, cfg := setup(t)
	path := filepath.Join(dir, "versions.yaml")
	provider := validatedProvider{providerFunc: eligible, UpdateValidator: &providers.IstioProvider{}}
	if _, err := Check(context.Background(), cfg, dir, fixed(provider), nil); err != nil {
		t.Fatal(err)
	}
	current, err := config.ReadCurrentVersion(path, "istio")
	if err != nil || current != "asm-1-29" {
		t.Fatalf("check changed configuration: %q, %v", current, err)
	}
	results, err := Update(context.Background(), cfg, dir, fixed(provider), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].Updated {
		t.Fatal("successful update must report a persistent source edit")
	}
	current, err = config.ReadCurrentVersion(path, "istio")
	if err != nil || current != "asm-1-30" {
		t.Fatalf("update did not change configuration: %q, %v", current, err)
	}
}

func TestFailedDiscoveryPreservesReportAndSource(t *testing.T) {
	dir, cfg := setup(t)
	failure := errors.New("configured revision is incompatible")
	provider := providerFunc(func(context.Context, providers.CheckRequest) (providers.Result, error) {
		return providers.Result{
			Candidate:     "asm-1-30",
			Compatibility: "AKS 1.35",
			Regions:       []providers.RegionResult{{Location: "uksouth", Current: "INVALID", Candidate: "BLOCKED"}},
		}, failure
	})
	results, err := Update(context.Background(), cfg, dir, fixed(provider), nil)
	if !errors.Is(err, failure) || len(results) != 1 {
		t.Fatalf("discovery failure was lost: %+v, %v", results, err)
	}
	if len(results[0].Regions) != 1 || results[0].Candidate != "asm-1-30" || results[0].Compatibility != "AKS 1.35" || results[0].Updated {
		t.Fatalf("failure diagnostics were lost: %+v, %v", results, err)
	}
	current, err := config.ReadCurrentVersion(filepath.Join(dir, "versions.yaml"), "istio")
	if err != nil || current != "asm-1-29" {
		t.Fatalf("invalid region caused a write: %q, %v", current, err)
	}
}

func TestPauseSkipsUpdatesWithoutBlockingOtherComponents(t *testing.T) {
	dir, cfg := setup(t)
	paused := cfg.Components["istio"]
	paused.Paused = true
	paused.Locations = []string{"uksouth"}
	cfg.Components["istio"] = paused
	active := paused
	active.Paused = false
	active.Locations = []string{"all"}
	active.Targets = []config.Target{{FilePath: "other.yaml", JsonPath: "istio"}}
	cfg.Components["other"] = active
	if err := os.WriteFile(filepath.Join(dir, "other.yaml"), []byte("istio: asm-1-29\naks: '1.35'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	provider := validatedProvider{
		providerFunc: func(ctx context.Context, req providers.CheckRequest) (providers.Result, error) {
			calls++
			return eligible(ctx, req)
		},
		UpdateValidator: &providers.IstioProvider{},
	}
	results, err := Update(context.Background(), cfg, dir, fixed(provider), nil)
	if err != nil || len(results) != 2 || calls != 1 {
		t.Fatalf("paused component must bypass update validation and discovery: calls=%d, results=%+v, err=%v", calls, results, err)
	}
	if !results[0].Paused || results[0].Updated || results[0].Next != "" || results[0].Detail == "" || !results[1].Updated {
		t.Fatalf("pause must prevent only the selected component's write: %+v", results)
	}
	for file, want := range map[string]string{"versions.yaml": "asm-1-29", "other.yaml": "asm-1-30"} {
		current, err := config.ReadCurrentVersion(filepath.Join(dir, file), "istio")
		if err != nil || current != want {
			t.Fatalf("unexpected version in %s: %q, %v; want %q", file, current, err, want)
		}
	}
	results, err = Check(context.Background(), cfg, dir, fixed(provider), []string{"istio"})
	if err != nil || len(results) != 1 || calls != 2 || results[0].Paused || results[0].Next != "asm-1-30" || results[0].Updated {
		t.Fatalf("read-only diagnostics must remain available while paused: %+v, %v", results, err)
	}
	paused.Paused = false
	paused.Locations = []string{"all"}
	cfg.Components["istio"] = paused
	results, err = Update(context.Background(), cfg, dir, fixed(provider), []string{"istio"})
	if err != nil || len(results) != 1 || calls != 3 || results[0].Paused || !results[0].Updated {
		t.Fatalf("unpausing must resume normal discovery and updating: %+v, %v", results, err)
	}
}

func TestRegionalSubsetIsCheckOnly(t *testing.T) {
	dir, cfg := setup(t)
	component := cfg.Components["istio"]
	component.Locations = []string{"uksouth"}
	cfg.Components["istio"] = component
	calls := 0
	provider := validatedProvider{
		providerFunc: func(ctx context.Context, req providers.CheckRequest) (providers.Result, error) {
			calls++
			return eligible(ctx, req)
		},
		UpdateValidator: &providers.IstioProvider{},
	}
	if _, err := Check(context.Background(), cfg, dir, fixed(provider), nil); err != nil {
		t.Fatalf("regional diagnostics should remain available: %v", err)
	}
	results, err := Update(context.Background(), cfg, dir, fixed(provider), nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to update a shared default") || calls != 1 {
		t.Fatalf("regional update must fail before discovery: calls=%d, results=%+v, err=%v", calls, results, err)
	}
	current, err := config.ReadCurrentVersion(filepath.Join(dir, "versions.yaml"), "istio")
	if err != nil || current != "asm-1-29" {
		t.Fatalf("regional check changed shared configuration: %q, %v", current, err)
	}
}

func TestOtherProvidersDoNotInheritIstioRegionalPolicy(t *testing.T) {
	dir, cfg := setup(t)
	component := cfg.Components["istio"]
	component.Provider = "test-provider"
	component.Locations = []string{"uksouth"}
	cfg.Components["istio"] = component
	results, err := Update(context.Background(), cfg, dir, fixed(providerFunc(eligible)), nil)
	if err != nil || !results[0].Updated {
		t.Fatalf("Istio-specific policy must not be imposed on providers without it: %+v, %v", results, err)
	}
}

func TestUnknownComponentFailsBeforeDiscovery(t *testing.T) {
	dir, cfg := setup(t)
	if _, err := Check(context.Background(), cfg, dir, fixed(nil), []string{"istio-typo"}); err == nil {
		t.Fatal("unknown component must fail")
	}
}

func TestMultipleTargetsFailWithoutChanges(t *testing.T) {
	dir, cfg := setup(t)
	component := cfg.Components["istio"]
	component.Targets = append(component.Targets, config.Target{FilePath: "versions.yaml", JsonPath: "missing"})
	cfg.Components["istio"] = component
	if _, err := Update(context.Background(), cfg, dir, fixed(providerFunc(eligible)), nil); err == nil {
		t.Fatal("unsupported multi-target component must fail before writing")
	}
	current, err := config.ReadCurrentVersion(filepath.Join(dir, "versions.yaml"), "istio")
	if err != nil || current != "asm-1-29" {
		t.Fatalf("invalid targets changed configuration: %q, %v", current, err)
	}
}

func TestWriteFailureIsReturned(t *testing.T) {
	dir, cfg := setup(t)
	provider := providerFunc(func(context.Context, providers.CheckRequest) (providers.Result, error) {
		if err := os.Remove(filepath.Join(dir, "versions.yaml")); err != nil {
			t.Fatal(err)
		}
		return providers.Result{Next: "asm-1-30"}, nil
	})
	results, err := Update(context.Background(), cfg, dir, fixed(provider), nil)
	if err == nil || results[0].Error == nil {
		t.Fatalf("write failure was swallowed: %+v, %v", results, err)
	}
}

func TestCheckRequestPassesOnlyConfiguredFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "versions.yaml"), []byte("grafana: 10.0.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Components: map[string]config.ComponentConfig{
		"grafana": {
			Provider: "test-provider",
			Targets:  []config.Target{{FilePath: "versions.yaml", JsonPath: "grafana"}},
		},
	}}
	var got providers.CheckRequest
	provider := providerFunc(func(_ context.Context, req providers.CheckRequest) (providers.Result, error) {
		got = req
		return providers.Result{Next: "10.1.0"}, nil
	})
	if _, err := Check(context.Background(), cfg, dir, fixed(provider), nil); err != nil {
		t.Fatal(err)
	}
	if got.Current != "10.0.0" || len(got.Locations) != 0 || got.ConstraintVersion != "" {
		t.Fatalf("non-Istio components must not invent locations or constraints: %+v", got)
	}
}
