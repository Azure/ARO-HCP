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

package app

import (
	"fmt"
	"maps"
	"sync"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosclient"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
)

// StorageFactory supplies clients bound to a bucket for each controller and
// Cosmos container. Implementations must support concurrent lookups and retain
// the same budget across repeated lookups. Different containers have independent
// allocations and debt, including different management clusters' containers.
//
// Names are registered at construction, so client initialization errors are
// returned before controllers start. Looking up an unregistered name is a
// programming error and panics, like other invalid controller wiring.
type StorageFactory interface {
	ResourcesStorageClient(controllerName string) corecosmosstorage.ResourcesDBClient
	BillingStorageClient(controllerName string) billingcosmosstorage.BillingDBClient
	FleetStorageClient(controllerName string) fleetcosmosstorage.FleetDBClient
	KubeApplierStorageClients(controllerName string) kubeappliercosmosstorage.KubeApplierDBClients
}

type StorageFactoryOptions struct {
	ResourcesRUsPerSecond float64
	BillingRUsPerSecond   float64
	FleetRUsPerSecond     float64
	// KubeApplierRUsPerSecond applies independently to each MC container.
	KubeApplierRUsPerSecond float64
	// KubeApplierUtilization overrides Utilization for MC containers so the
	// backend and kube-applier binary can reserve separate shares.
	KubeApplierUtilization float64
	Utilization            float64
	ControllerNames        []string
	ControllerFractions    map[string]float64
}

type controllerStorageClients struct {
	resources   corecosmosstorage.ResourcesDBClient
	billing     billingcosmosstorage.BillingDBClient
	fleet       fleetcosmosstorage.FleetDBClient
	kubeApplier kubeappliercosmosstorage.KubeApplierDBClients
}

type cosmosStorageFactory struct {
	rateLimitsMu                sync.Mutex
	rateLimits                  map[string]*cosmosratelimit.ControllerRateLimits
	kubeApplierRateLimitOptions cosmosratelimit.ControllerRateLimitOptions
	cosmosURL                   string
	databaseName                string
	clientOptions               cosmosclient.Options
	// Immutable after construction; the contained clients are concurrency-safe.
	clients map[string]*controllerStorageClients
}

var _ StorageFactory = (*cosmosStorageFactory)(nil)

// NewStorageFactory creates container clients for each registered controller.
// Each container has its own ControllerRateLimits. Each client owns its Cosmos
// pipeline and uses the bucket for that controller and container. The count is
// derived from all registered storage consumers; unused shares stay reserved.
func NewStorageFactory(cosmosURL, databaseName string, clientOptions cosmosclient.Options, options StorageFactoryOptions) (StorageFactory, error) {
	return newStorageFactory(cosmosURL, databaseName, clientOptions, options)
}

func newStorageFactory(cosmosURL, databaseName string, clientOptions cosmosclient.Options, options StorageFactoryOptions) (*cosmosStorageFactory, error) {
	names := make(map[string]struct{}, len(options.ControllerNames))
	for _, name := range options.ControllerNames {
		if name == "" {
			return nil, fmt.Errorf("storage factory requires nonempty controller names")
		}
		if _, found := names[name]; found {
			return nil, fmt.Errorf("duplicate storage controller %q", name)
		}
		names[name] = struct{}{}
	}
	for name := range options.ControllerFractions {
		if _, found := names[name]; !found {
			return nil, fmt.Errorf("RU fraction specified for unregistered storage controller %q", name)
		}
	}
	commonOptions := cosmosratelimit.ControllerRateLimitOptions{
		ControllerCount: len(names), Utilization: options.Utilization,
		ControllerFractions: maps.Clone(options.ControllerFractions),
	}
	limitsByContainer := make(map[string]*cosmosratelimit.ControllerRateLimits)
	for _, allocation := range []struct {
		container string
		rus       float64
	}{
		{"Resources", options.ResourcesRUsPerSecond},
		{"Billing", options.BillingRUsPerSecond},
		{"Fleet", options.FleetRUsPerSecond},
	} {
		containerOptions := commonOptions
		containerOptions.TotalRUsPerSecond = allocation.rus
		limits, err := cosmosratelimit.NewControllerRateLimits(containerOptions)
		if err != nil {
			return nil, fmt.Errorf("invalid %s rate limits: %w", allocation.container, err)
		}
		limitsByContainer[allocation.container] = limits
	}
	kubeApplierOptions := commonOptions
	kubeApplierOptions.TotalRUsPerSecond = options.KubeApplierRUsPerSecond
	if options.KubeApplierUtilization != 0 {
		kubeApplierOptions.Utilization = options.KubeApplierUtilization
	}
	// Validate before controllers start; actual MC container names are resolved
	// lazily from Fleet, and each gets its own ControllerRateLimits instance.
	if _, err := cosmosratelimit.NewControllerRateLimits(kubeApplierOptions); err != nil {
		return nil, fmt.Errorf("invalid kube-applier rate limits: %w", err)
	}
	factory := &cosmosStorageFactory{
		rateLimits: limitsByContainer, kubeApplierRateLimitOptions: kubeApplierOptions,
		cosmosURL: cosmosURL, databaseName: databaseName, clientOptions: clientOptions,
		clients: make(map[string]*controllerStorageClients, len(names)),
	}
	for _, name := range options.ControllerNames {
		clients, err := factory.newControllerClients(name)
		if err != nil {
			return nil, fmt.Errorf("initialize storage for controller %q: %w", name, err)
		}
		factory.clients[name] = clients
	}
	return factory, nil
}

// tokenBucket retains one budget per physical container and controller. The
// three fixed containers are preconfigured; MC containers use the kube-applier
// allocation and are registered when their names become known.
func (f *cosmosStorageFactory) tokenBucket(containerName, controllerName string) (*cosmosratelimit.TokenBucket, error) {
	f.rateLimitsMu.Lock()
	defer f.rateLimitsMu.Unlock()
	limits, found := f.rateLimits[containerName]
	if !found {
		var err error
		limits, err = cosmosratelimit.NewControllerRateLimits(f.kubeApplierRateLimitOptions)
		if err != nil {
			return nil, err
		}
		f.rateLimits[containerName] = limits
	}
	return limits.ForController(controllerName)
}

func (f *cosmosStorageFactory) newControllerClients(name string) (*controllerStorageClients, error) {
	resourcesBucket, err := f.tokenBucket("Resources", name)
	if err != nil {
		return nil, err
	}
	resources, err := corecosmosstorage.NewResourcesDBClient(f.cosmosURL, f.databaseName, f.clientOptions, resourcesBucket)
	if err != nil {
		return nil, err
	}
	billingBucket, err := f.tokenBucket("Billing", name)
	if err != nil {
		return nil, err
	}
	billing, err := billingcosmosstorage.NewBillingDBClient(f.cosmosURL, f.databaseName, f.clientOptions, billingBucket)
	if err != nil {
		return nil, err
	}
	fleetBucket, err := f.tokenBucket("Fleet", name)
	if err != nil {
		return nil, err
	}
	fleet, err := fleetcosmosstorage.NewFleetDBClient(f.cosmosURL, f.databaseName, f.clientOptions, fleetBucket)
	if err != nil {
		return nil, err
	}
	kubeApplier := kubeappliercosmosstorage.NewKubeApplierDBClients(f.cosmosURL, f.databaseName, f.clientOptions,
		func(containerName string) (*cosmosratelimit.TokenBucket, error) {
			return f.tokenBucket(containerName, name)
		},
		kubeappliercosmosstorage.NewDBBackedManagementClusterLister(fleet))
	return &controllerStorageClients{resources: resources, billing: billing, fleet: fleet, kubeApplier: kubeApplier}, nil
}

func (f *cosmosStorageFactory) forController(name string) *controllerStorageClients {
	clients, found := f.clients[name]
	if !found {
		panic(fmt.Sprintf("unregistered storage controller %q", name))
	}
	return clients
}

func (f *cosmosStorageFactory) ResourcesStorageClient(name string) corecosmosstorage.ResourcesDBClient {
	return f.forController(name).resources
}
func (f *cosmosStorageFactory) BillingStorageClient(name string) billingcosmosstorage.BillingDBClient {
	return f.forController(name).billing
}
func (f *cosmosStorageFactory) FleetStorageClient(name string) fleetcosmosstorage.FleetDBClient {
	return f.forController(name).fleet
}
func (f *cosmosStorageFactory) KubeApplierStorageClients(name string) kubeappliercosmosstorage.KubeApplierDBClients {
	return f.forController(name).kubeApplier
}
