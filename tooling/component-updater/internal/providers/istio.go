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

package providers

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

var asmRevisionPattern = regexp.MustCompile(`^asm-(\d+)-(\d+)$`)
var kubernetesVersionPattern = regexp.MustCompile(`^(\d+\.\d+)(?:\.\d+)?$`)
var errNoMeshCatalog = errors.New("no Istio mesh catalog")

const discoveryParallelism = 8

type IstioProvider struct {
	client         *armcontainerservice.ManagedClustersClient
	subscriptions  *armsubscriptions.Client
	resources      *armresources.ProvidersClient
	subscriptionID string
}

func NewIstioProvider(subscriptionID string, credential azcore.TokenCredential) (*IstioProvider, error) {
	return newIstioProvider(subscriptionID, credential, azureDiscoveryOptions())
}

func newIstioProvider(subscriptionID string, credential azcore.TokenCredential, options *azcorearm.ClientOptions) (*IstioProvider, error) {
	client, err := armcontainerservice.NewManagedClustersClient(subscriptionID, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating AKS discovery client: %w", err)
	}
	subscriptions, err := armsubscriptions.NewClient(credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating subscription discovery client: %w", err)
	}
	resources, err := armresources.NewProvidersClient(subscriptionID, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating resource-provider discovery client: %w", err)
	}
	return &IstioProvider{client: client, subscriptions: subscriptions, resources: resources, subscriptionID: subscriptionID}, nil
}

func (p *IstioProvider) ValidateUpdate(req UpdateRequest) error {
	if !slices.Contains(req.Locations, "all") {
		return errors.New("refusing to update a shared default using a regional subset; locations must include all for automatic mesh-supported region discovery; use check for regional diagnostics")
	}
	return nil
}

func (p *IstioProvider) Check(ctx context.Context, req CheckRequest) (Result, error) {
	major, minor, ok := parseASMRevision(req.Current)
	if !ok {
		return Result{}, fmt.Errorf("invalid configured Istio revision %q", req.Current)
	}
	kubernetes := kubernetesVersionPattern.FindStringSubmatch(req.ConstraintVersion)
	if kubernetes == nil {
		return Result{}, fmt.Errorf("invalid configured Kubernetes version %q", req.ConstraintVersion)
	}
	locations := append([]string(nil), req.Locations...)
	if len(locations) == 0 {
		return Result{}, fmt.Errorf("at least one discovery location is required")
	}
	result := Result{Compatibility: "AKS " + req.ConstraintVersion}
	discover := slices.Contains(locations, "all")
	if discover {
		discovered, err := p.locations(ctx)
		if err != nil {
			return result, fmt.Errorf("discovering mesh-supported locations: %w", err)
		}
		for _, location := range locations {
			if location != "all" && !slices.Contains(discovered, location) {
				discovered = append(discovered, location)
			}
		}
		locations = discovered
		sort.Strings(locations)
	}
	var common map[string]bool
	blocked := false
	var failures []error
	type catalog struct {
		revisions map[string]*armcontainerservice.MeshRevision
		err       error
	}
	catalogs := make([]catalog, len(locations))
	var group errgroup.Group
	group.SetLimit(discoveryParallelism)
	for i, location := range locations {
		group.Go(func() error {
			defer utilruntime.HandleCrash()
			catalogs[i].revisions, catalogs[i].err = p.revisions(ctx, location)
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return result, err
	}
	discoveredCandidates := map[string]bool{}
	addCandidate := func(revision string) {
		candidateMajor, candidateMinor, valid := parseASMRevision(revision)
		if valid && candidateMajor == major && candidateMinor > minor && candidateMinor-minor <= 2 {
			discoveredCandidates[revision] = true
		}
	}
	for _, catalog := range catalogs {
		for revision := range catalog.revisions {
			addCandidate(revision)
		}
		if source := catalog.revisions[req.Current]; source != nil {
			for _, upgrade := range source.Upgrades {
				if upgrade != nil {
					addCandidate(*upgrade)
				}
			}
		}
	}
	var candidates []string
	for candidate := range discoveredCandidates {
		candidates = append(candidates, candidate)
	}
	sortASMRevisions(candidates)
	next := ""
	if len(candidates) > 0 {
		next = candidates[0]
	}
	for _, candidate := range candidates {
		ready := true
		for _, catalog := range catalogs {
			if catalog.err != nil || istioUpgradeBlocker(catalog.revisions, req.Current, candidate, kubernetes[1]) != "" {
				ready = false
				break
			}
		}
		if ready {
			next = candidate
			break
		}
	}
	result.Candidate = next
	for i, location := range locations {
		region := RegionResult{Location: location}
		revisions, err := catalogs[i].revisions, catalogs[i].err
		if err != nil {
			region.Current, region.Candidate, region.Detail = "ERROR", "BLOCKED", err.Error()
			failures = append(failures, fmt.Errorf("discovering Istio in %s: %w", location, err))
			result.Regions = append(result.Regions, region)
			continue
		}
		compatible := map[string]bool{}
		for revision, profile := range revisions {
			if supportsKubernetes(profile, kubernetes[1]) {
				compatible[revision] = true
				region.Compatible = append(region.Compatible, revision)
			}
		}
		sortASMRevisions(region.Compatible)
		if common == nil {
			common = compatible
		} else {
			for revision := range common {
				if !compatible[revision] {
					delete(common, revision)
				}
			}
		}
		source := revisions[req.Current]
		region.Current = "VALID"
		if source == nil || !supportsKubernetes(source, kubernetes[1]) {
			region.Current = "INVALID"
			failures = append(failures, fmt.Errorf("%s: configured %s is unavailable or incompatible with AKS %s", location, req.Current, kubernetes[1]))
		}
		if source != nil {
			for _, upgrade := range source.Upgrades {
				if upgrade != nil {
					region.Upgrades = append(region.Upgrades, *upgrade)
				}
			}
		}
		region.Candidate = "READY"
		region.Detail = "advertised upgrade and Kubernetes compatibility confirmed"
		if reason := istioUpgradeBlocker(revisions, req.Current, next, kubernetes[1]); reason != "" {
			region.Candidate, region.Detail = "BLOCKED", reason
			blocked = true
		}
		result.Regions = append(result.Regions, region)
	}
	for revision := range common {
		result.Available = append(result.Available, revision)
	}
	sortASMRevisions(result.Available)
	if common == nil && len(failures) == 0 {
		failures = append(failures, errors.New("no locations returned an Istio mesh catalog"))
	}
	if len(failures) > 0 {
		result.Detail = "configuration unchanged: regional validation failed"
		return result, errors.Join(failures...)
	}
	if blocked {
		result.Detail = fmt.Sprintf("update deferred: %s is not ready in every region", next)
		if next == "" {
			result.Detail = "no newer revision discovered within n+2 of the configured version"
		}
		return result, nil
	}
	result.Next = next
	result.Detail = "advertised upgrade and Kubernetes compatibility confirmed in all discovery regions"
	return result, nil
}

func istioUpgradeBlocker(revisions map[string]*armcontainerservice.MeshRevision, current, candidate, kubernetes string) string {
	source := revisions[current]
	switch {
	case source == nil || !supportsKubernetes(source, kubernetes):
		return "configured revision unavailable or incompatible with configured AKS"
	case candidate == "":
		return "no newer revision discovered within n+2"
	case revisions[candidate] == nil:
		return "next revision unavailable"
	case !contains(source.Upgrades, candidate):
		return "upgrade path not advertised"
	case !supportsKubernetes(revisions[candidate], kubernetes):
		return "Kubernetes compatibility not confirmed"
	default:
		return ""
	}
}

func (p *IstioProvider) locations(ctx context.Context) ([]string, error) {
	aliases := map[string]string{}
	pager := p.subscriptions.NewListLocationsPager(p.subscriptionID, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, location := range page.Value {
			if location == nil || location.Name == nil || strings.TrimSpace(*location.Name) == "" {
				return nil, errors.New("subscription response contains an invalid location")
			}
			names := []string{*location.Name}
			if location.DisplayName != nil {
				names = append(names, *location.DisplayName)
			}
			for _, alias := range names {
				key := strings.ToLower(strings.TrimSpace(alias))
				if previous, exists := aliases[key]; exists && previous != *location.Name {
					return nil, fmt.Errorf("ambiguous subscription location %q", alias)
				}
				aliases[key] = *location.Name
			}
		}
	}
	if len(aliases) == 0 {
		return nil, errors.New("subscription response contains no locations")
	}
	provider, err := p.resources.Get(ctx, "Microsoft.ContainerService", nil)
	if err != nil {
		return nil, fmt.Errorf("reading mesh resource-provider metadata: %w", err)
	}
	var locations []string
	for _, resourceType := range provider.ResourceTypes {
		if resourceType == nil || resourceType.ResourceType == nil {
			return nil, errors.New("resource-provider metadata contains an invalid resource type")
		}
		if !strings.EqualFold(*resourceType.ResourceType, "locations/meshRevisionProfiles") {
			continue
		}
		for _, supported := range resourceType.Locations {
			if supported == nil || strings.TrimSpace(*supported) == "" {
				return nil, errors.New("mesh resource-provider metadata contains an invalid location")
			}
			name, exists := aliases[strings.ToLower(strings.TrimSpace(*supported))]
			if !exists {
				return nil, fmt.Errorf("mesh-supported location %q is missing from subscription discovery", *supported)
			}
			if !slices.Contains(locations, name) {
				locations = append(locations, name)
			}
		}
	}
	if len(locations) == 0 {
		return nil, errors.New("resource-provider metadata contains no mesh-supported locations")
	}
	return locations, nil
}

func (p *IstioProvider) revisions(ctx context.Context, location string) (map[string]*armcontainerservice.MeshRevision, error) {
	result := map[string]*armcontainerservice.MeshRevision{}
	pager := p.client.NewListMeshRevisionProfilesPager(location, nil)
	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, profile := range page.Value {
			if profile == nil || profile.Name == nil {
				return nil, errors.New("mesh response contains an invalid profile")
			}
			if *profile.Name != "istio" {
				continue
			}
			if profile.Properties == nil {
				return nil, errors.New("istio mesh profile is missing properties")
			}
			if len(profile.Properties.MeshRevisions) == 0 {
				return nil, errNoMeshCatalog
			}
			found = true
			for _, revision := range profile.Properties.MeshRevisions {
				if revision == nil || revision.Revision == nil || !asmRevisionPattern.MatchString(*revision.Revision) {
					return nil, fmt.Errorf("mesh profile contains an invalid revision")
				}
				result[*revision.Revision] = revision
			}
		}
	}
	if !found {
		return nil, errNoMeshCatalog
	}
	return result, nil
}

func supportsKubernetes(revision *armcontainerservice.MeshRevision, version string) bool {
	// Match the support plan configured in dev-infrastructure/modules/aks-cluster-base.bicep.
	for _, compatibility := range revision.CompatibleWith {
		if compatibility != nil && compatibility.Name != nil &&
			strings.EqualFold(*compatibility.Name, "KubernetesOfficial") && contains(compatibility.Versions, version) {
			return true
		}
	}
	return false
}

func contains(values []*string, target string) bool {
	return slices.ContainsFunc(values, func(value *string) bool { return value != nil && *value == target })
}

func parseASMRevision(revision string) (major, minor int, ok bool) {
	match := asmRevisionPattern.FindStringSubmatch(revision)
	if match == nil {
		return 0, 0, false
	}
	major, majorErr := strconv.Atoi(match[1])
	minor, minorErr := strconv.Atoi(match[2])
	return major, minor, majorErr == nil && minorErr == nil
}

func sortASMRevisions(revisions []string) {
	sort.Slice(revisions, func(i, j int) bool {
		aMajor, aMinor, _ := parseASMRevision(revisions[i])
		bMajor, bMinor, _ := parseASMRevision(revisions[j])
		return aMajor < bMajor || (aMajor == bMajor && aMinor < bMinor)
	})
}
