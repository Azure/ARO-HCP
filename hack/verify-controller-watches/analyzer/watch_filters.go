// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package analyzer

import (
	"slices"
)

// Watch binding precedes AddInterestingResource in most constructors. Capture
// both against the same controller instance and apply the final filter only
// after constructor/reconciliation traversal. Custom event handlers are separate
// subscriptions and must not inherit this generic watcher's filter.
type watchFilter struct {
	configured bool
	unknown    bool
	resources  map[string]bool
}

type pendingWatch struct {
	source *value
	filter *watchFilter
	at     Location
	path   []Location
}

func (e *evaluator) filter(v *value) *watchFilter {
	if v.watchFilter == nil {
		v.watchFilter = &watchFilter{resources: map[string]bool{}}
	}
	return v.watchFilter
}

func (e *evaluator) queueWatch(controller, source *value, at Location) {
	e.pendingWatches = append(e.pendingWatches, pendingWatch{source: source, filter: e.filter(controller), at: at, path: slices.Clone(e.path)})
}

func (e *evaluator) applyWatchFilters() {
	for _, watch := range e.pendingWatches {
		old := e.path
		e.path = watch.path
		e.filteredWatchValue(watch.source, watch.filter, watch.at)
		e.path = old
	}
}

func (e *evaluator) filteredWatchValue(source *value, filter *watchFilter, at Location) {
	if source == nil || source.nilValue {
		return
	}
	if len(source.alternatives) > 0 {
		for _, alt := range source.alternatives {
			e.filteredWatchValue(alt, filter, at)
		}
		return
	}
	if source.doc == "" && len(source.items) > 0 {
		for _, item := range source.items {
			e.filteredWatchValue(item, filter, at)
		}
		return
	}
	if !filter.configured || filter.unknown || source.doc == "" {
		e.watchValue(source, at)
		return
	}
	expressions := e.program.ResourceTypes[source.doc]
	if len(expressions) == 0 {
		e.gap("cannot resolve ARM resource types for filtered informer: "+source.doc, at)
		e.watchValue(source, at)
		return
	}
	unknown, accepted := false, false
	for _, expr := range expressions {
		v := e.eval(expr, &frame{vars: map[string]*value{}, types: map[string]string{}})
		if len(v.items) == 0 {
			unknown = true
		}
		for _, resource := range v.items {
			if !resource.known {
				unknown = true
				continue
			}
			if filter.resources[resource.literal] {
				accepted = true
			}
		}
	}
	if unknown {
		e.gap("incomplete ARM resource types for filtered informer: "+source.doc, at)
	}
	if accepted || unknown {
		e.watchValue(source, at)
	}
}
