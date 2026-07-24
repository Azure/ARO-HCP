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

package framework

import (
	"context"
	"fmt"
	"testing"

	"github.com/blang/semver/v4"
)

func overrideGetAllVersionsInMinor(t *testing.T, fn func(ctx context.Context, channelGroup string, version string) ([]semver.Version, error)) {
	t.Helper()
	orig := getAllVersionsInMinor
	getAllVersionsInMinor = fn
	t.Cleanup(func() { getAllVersionsInMinor = orig })
}

func overrideGetUpgradeCandidates(t *testing.T, fn func(ctx context.Context, channelGroup string, maxVersion string, fromVersion string) ([]semver.Version, error)) {
	t.Helper()
	orig := getUpgradeCandidates
	getUpgradeCandidates = fn
	t.Cleanup(func() { getUpgradeCandidates = orig })
}

func v(s string) semver.Version {
	return semver.MustParse(s)
}

func TestGetInstallVersionForZStreamUpgrade_ReturnsOlderVersionForUpgrade(t *testing.T) {
	// candidates: 4.19.39, 4.19.38, 4.19.37, 4.19.36 (descending)
	// candidates[1] (4.19.38) has upgrade targets → install candidates[2] (4.19.37)
	overrideGetAllVersionsInMinor(t, func(_ context.Context, _ string, _ string) ([]semver.Version, error) {
		return []semver.Version{v("4.19.39"), v("4.19.38"), v("4.19.37"), v("4.19.36")}, nil
	})
	overrideGetUpgradeCandidates(t, func(_ context.Context, _ string, maxVersion string, fromVersion string) ([]semver.Version, error) {
		if maxVersion != "4.19.39" {
			t.Errorf("maxVersion should be same-minor latest 4.19.39, got %s", maxVersion)
		}
		if fromVersion == "4.19.39" {
			t.Error("should never query candidates[0] (4.19.39) — loop must start at i=1")
		}
		if fromVersion == "4.19.38" {
			return []semver.Version{v("4.19.39")}, nil
		}
		return nil, nil
	})

	install, hasUpgrade, err := GetInstallVersionForZStreamUpgrade(context.Background(), "candidate", "4.19.25")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if install != "4.19.37" {
		t.Errorf("expected install=4.19.37 (candidates[i+1]), got %s", install)
	}
	if !hasUpgrade {
		t.Error("expected hasUpgradePath=true")
	}
}

func TestGetInstallVersionForZStreamUpgrade_SkipsLatestCandidate(t *testing.T) {
	// Verify candidates[0] is never queried for upgrade targets
	overrideGetAllVersionsInMinor(t, func(_ context.Context, _ string, _ string) ([]semver.Version, error) {
		return []semver.Version{v("4.20.30"), v("4.20.29"), v("4.20.28")}, nil
	})

	queriedVersions := make(map[string]bool)
	overrideGetUpgradeCandidates(t, func(_ context.Context, _ string, _ string, fromVersion string) ([]semver.Version, error) {
		queriedVersions[fromVersion] = true
		if fromVersion == "4.20.29" {
			return []semver.Version{v("4.20.30")}, nil
		}
		return nil, nil
	})

	_, _, err := GetInstallVersionForZStreamUpgrade(context.Background(), "candidate", "4.20")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if queriedVersions["4.20.30"] {
		t.Error("must not query candidates[0] (4.20.30) for upgrade targets")
	}
	if !queriedVersions["4.20.29"] {
		t.Error("should have queried candidates[1] (4.20.29)")
	}
}

func TestGetInstallVersionForZStreamUpgrade_UsesSameMinorMaxVersion(t *testing.T) {
	overrideGetAllVersionsInMinor(t, func(_ context.Context, _ string, _ string) ([]semver.Version, error) {
		return []semver.Version{v("4.21.10"), v("4.21.9"), v("4.21.8")}, nil
	})

	called := false
	overrideGetUpgradeCandidates(t, func(_ context.Context, _ string, maxVersion string, _ string) ([]semver.Version, error) {
		called = true
		if maxVersion != "4.21.10" {
			t.Errorf("maxVersion must be same-minor latest (4.21.10), got %s", maxVersion)
		}
		return nil, nil
	})

	_, _, err := GetInstallVersionForZStreamUpgrade(context.Background(), "candidate", "4.21")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected upgrade candidates lookup to be invoked")
	}
}

func TestGetInstallVersionForZStreamUpgrade_NoUpgradePath(t *testing.T) {
	overrideGetAllVersionsInMinor(t, func(_ context.Context, _ string, _ string) ([]semver.Version, error) {
		return []semver.Version{v("4.22.5"), v("4.22.4"), v("4.22.3")}, nil
	})
	overrideGetUpgradeCandidates(t, func(_ context.Context, _ string, _ string, _ string) ([]semver.Version, error) {
		return nil, nil
	})

	install, hasUpgrade, err := GetInstallVersionForZStreamUpgrade(context.Background(), "candidate", "4.22")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if install != "4.22.5" {
		t.Errorf("expected latest version 4.22.5 when no upgrade path, got %s", install)
	}
	if hasUpgrade {
		t.Error("expected hasUpgradePath=false")
	}
}

func TestGetInstallVersionForZStreamUpgrade_SingleCandidate(t *testing.T) {
	overrideGetAllVersionsInMinor(t, func(_ context.Context, _ string, _ string) ([]semver.Version, error) {
		return []semver.Version{v("4.23.1")}, nil
	})

	install, hasUpgrade, err := GetInstallVersionForZStreamUpgrade(context.Background(), "candidate", "4.23")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if install != "4.23.1" {
		t.Errorf("expected 4.23.1, got %s", install)
	}
	if hasUpgrade {
		t.Error("expected hasUpgradePath=false with single candidate")
	}
}

func TestGetInstallVersionForZStreamUpgrade_TwoCandidates(t *testing.T) {
	// With only 2 candidates, the only upgrade target would be candidates[0]
	// (the freshest), so hasUpgradePath should be false.
	overrideGetAllVersionsInMinor(t, func(_ context.Context, _ string, _ string) ([]semver.Version, error) {
		return []semver.Version{v("4.19.39"), v("4.19.38")}, nil
	})
	overrideGetUpgradeCandidates(t, func(_ context.Context, _ string, _ string, fromVersion string) ([]semver.Version, error) {
		t.Errorf("should not query upgrade targets with only 2 candidates, but queried %s", fromVersion)
		return nil, nil
	})

	install, hasUpgrade, err := GetInstallVersionForZStreamUpgrade(context.Background(), "candidate", "4.19.25")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if install != "4.19.39" {
		t.Errorf("expected latest 4.19.39, got %s", install)
	}
	if hasUpgrade {
		t.Error("expected hasUpgradePath=false with only 2 candidates")
	}
}

func TestGetInstallVersionForZStreamUpgrade_NoCandidates(t *testing.T) {
	overrideGetAllVersionsInMinor(t, func(_ context.Context, _ string, _ string) ([]semver.Version, error) {
		return nil, nil
	})

	_, _, err := GetInstallVersionForZStreamUpgrade(context.Background(), "candidate", "4.99")
	if err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

func TestGetInstallVersionForZStreamUpgrade_UpgradeErrorPropagated(t *testing.T) {
	overrideGetAllVersionsInMinor(t, func(_ context.Context, _ string, _ string) ([]semver.Version, error) {
		return []semver.Version{v("4.19.5"), v("4.19.4"), v("4.19.3")}, nil
	})
	overrideGetUpgradeCandidates(t, func(_ context.Context, _ string, _ string, _ string) ([]semver.Version, error) {
		return nil, fmt.Errorf("cincinnati unavailable")
	})

	_, _, err := GetInstallVersionForZStreamUpgrade(context.Background(), "candidate", "4.19")
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestGetInstallVersionForZStreamUpgrade_FirstWithUpgradeTargetsWins(t *testing.T) {
	// candidates: 4.19.5, 4.19.4, 4.19.3, 4.19.2 (descending)
	// candidates[1] (4.19.4) has no targets, candidates[2] (4.19.3) has targets
	// → install candidates[3] (4.19.2)
	overrideGetAllVersionsInMinor(t, func(_ context.Context, _ string, _ string) ([]semver.Version, error) {
		return []semver.Version{v("4.19.5"), v("4.19.4"), v("4.19.3"), v("4.19.2")}, nil
	})
	overrideGetUpgradeCandidates(t, func(_ context.Context, _ string, _ string, fromVersion string) ([]semver.Version, error) {
		if fromVersion == "4.19.3" {
			return []semver.Version{v("4.19.4")}, nil
		}
		return nil, nil
	})

	install, hasUpgrade, err := GetInstallVersionForZStreamUpgrade(context.Background(), "candidate", "4.19")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if install != "4.19.2" {
		t.Errorf("expected 4.19.2 (candidates[i+1] for first with targets), got %s", install)
	}
	if !hasUpgrade {
		t.Error("expected hasUpgradePath=true")
	}
}
