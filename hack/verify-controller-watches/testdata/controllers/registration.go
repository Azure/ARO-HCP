// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

// Package controllers is analyzed, never executed. It deliberately imports the
// real registry, wrappers, informers, listers, CRUDs and document types so changes
// to those libraries must continue satisfying the independently specified test.
package controllers

import (
	"context"

	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
)

type controllerContext struct {
	Informers     coreinformers.BackendInformers
	Resources     corecosmosstorage.ResourcesDBClient
	Transaction   cosmosstorageutils.DBTransaction
	Lookup        Lookup
	Flag          bool
	Key, OtherKey string
}

type registration = controllerregistry.Registration[controllerContext]

func newControllerRegistry() map[string]registration {
	registry := map[string]registration{}
	registry["first"] = registration{Workers: 1, Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) { return newController(c), nil }}
	registry["inherited"] = registration{Workers: 1, Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) { return newController(c), nil }}
	registry["second"] = registration{Workers: 1, Enabled: func(controllerContext) bool { return false }, Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) { return newController(c), nil }}
	registry["wrapped"] = registration{Workers: 1, Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) { return newWrappedController(c), nil }}
	registry["operations"] = registration{Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) {
		return &operations{resources: c.Resources, transaction: c.Transaction}, nil
	}}
	registry["callbacks"] = registration{Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) {
		return &callbacks{context: c, worker: chooseWorker(c)}, nil
	}}
	registry["indexed"] = registration{Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) { return newIndexed(c), nil }}
	registry["optional-nil"] = registration{Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) { return newOptional(c, nil), nil }}
	registry["optional-watch"] = registration{Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) {
		informer, _ := c.Informers.ServiceProviderClusters()
		return newOptional(c, informer), nil
	}}
	return registry
}

type storageFactory interface{ ForController(string) Lookup }

type factory struct{ clients map[string]Lookup }

func newStorageFactory(resources corecosmosstorage.ResourcesDBClient) storageFactory {
	return &factory{clients: map[string]Lookup{
		"first":   &subscriptionLookup{resources: resources},
		"second":  &externalAuthLookup{resources: resources},
		"wrapped": &externalAuthLookup{resources: resources},
	}}
}

func (f *factory) ForController(name string) Lookup { return f.clients[name] }

// Bootstrap is the real test entrypoint: the analyzer must reach Instantiate
// through this injection loop, rather than calling each registration directly.
func Bootstrap(ctx context.Context, shared controllerContext) {
	shared.Lookup = &externalAuthLookup{resources: shared.Resources}
	registry := newControllerRegistry()
	storage := newStorageFactory(shared.Resources)
	instantiateControllers(registry, shared, storage)
}

func BootstrapWithExtraEvidence(ctx context.Context, shared controllerContext) {
	shared.Lookup = &externalAuthLookup{resources: shared.Resources}
	registry := newControllerRegistry()
	registry["operations"] = registration{Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) {
		return &operationsMoreEvidence{&operations{resources: c.Resources, transaction: c.Transaction}}, nil
	}}
	instantiateControllers(registry, shared, newStorageFactory(shared.Resources))
}

func BootstrapIncomplete(ctx context.Context) {
	registry := map[string]registration{}
	registry["incomplete"] = registration{Instantiate: func(controllerContext) (controllerregistry.Runnable, error) { return &incomplete{}, nil }}
	instantiateControllers(registry, controllerContext{}, newStorageFactory(nil))
}

func instantiateControllers(registry map[string]registration, shared controllerContext, storage storageFactory) {
	for name, entry := range registry {
		if entry.Enabled != nil && !entry.Enabled(shared) {
			continue
		}
		instanceContext := shared
		if name != "inherited" {
			instanceContext.Lookup = storage.ForController(name)
		}
		_, _ = entry.Instantiate(instanceContext)
	}
}
