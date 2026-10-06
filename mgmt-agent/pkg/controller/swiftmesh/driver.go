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

package swiftmesh

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

// Driver probes a single directed edge from a vantage to a target. A broken
// edge is a valid Result (OK=false), not an error return.
type Driver interface {
	Probe(ctx context.Context, from RouterPod, target Target, port int) Result
}

// RunMesh probes every edge of one HCP mesh concurrently and returns the
// aggregated report, tagged with the HCP's identity. perProbe caps each
// individual probe. sem is a shared concurrency budget: it bounds in-flight
// probes across every HCP in a sweep (not just this mesh), so the total exec
// pressure on the API server is capped regardless of HCP count. A nil sem means
// unbounded.
func RunMesh(ctx context.Context, mesh HCPMesh, d Driver, sem chan struct{}, perProbe time.Duration) MeshReport {
	edges := enumerateEdges(mesh)
	results := make([]Result, len(edges))

	g, gctx := errgroup.WithContext(ctx)
	for i, e := range edges {
		g.Go(func() error {
			defer utilruntime.HandleCrash()
			if sem != nil {
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-gctx.Done():
					results[i] = Result{FromPod: e.from.Name, FromIP: e.from.SwiftIP, ToIP: e.target.IP, Kind: e.target.Kind, Err: gctx.Err()}
					return nil
				}
			}
			// A broken edge is the signal we are after, so each probe owns its
			// own timeout and never returns an error that would cancel siblings.
			pctx, cancel := context.WithTimeout(gctx, perProbe)
			defer cancel()
			results[i] = d.Probe(pctx, e.from, e.target, mesh.Port)
			return nil
		})
	}
	_ = g.Wait()

	rep := BuildReport(results, len(mesh.Routers))
	rep.Namespace = mesh.Namespace
	rep.ResourceID = mesh.ResourceID
	return rep
}

// NewConcurrencyBudget returns a semaphore channel bounding in-flight probes to
// maxConcurrent across a whole sweep, or nil when maxConcurrent <= 0 (unbounded).
func NewConcurrencyBudget(maxConcurrent int) chan struct{} {
	if maxConcurrent <= 0 {
		return nil
	}
	return make(chan struct{}, maxConcurrent)
}
