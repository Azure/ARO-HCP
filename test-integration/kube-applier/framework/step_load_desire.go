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
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/keys"
)

// Step types "loadApplyDesire", "loadDeleteDesire", "loadReadDesire":
//
//	NN-load{Apply,Delete,Read}Desire-description/
//	    desire.json   # the *Desire document, unmarshalled directly into the
//	                  # corresponding kubeapplier type and inserted into the
//	                  # mock Cosmos at this step's index.
//
// Multiple JSON files in the directory load multiple desires (in arbitrary
// order). The Resource ID embedded in each desire determines its parent.

type loadApplyDesireStep struct {
	id      string
	stepDir fs.FS
}

func (s *loadApplyDesireStep) StepID() string { return s.id }

func (s *loadApplyDesireStep) Run(ctx context.Context, t *testing.T, h *Harness) {
	t.Helper()
	for _, d := range readJSONsAs[kubeapplier.ApplyDesire](t, s.stepDir) {
		key, err := keys.ApplyDesireKeyFromResourceID(d.GetResourceID())
		require.NoErrorf(t, err, "derive parent for ApplyDesire %s", d.GetResourceID())
		crud, err := h.KubeApplierDBClient.KubeApplier(ManagementCluster).ApplyDesires(key.ResourceParent())
		require.NoError(t, err)
		_, err = crud.Create(ctx, d, nil)
		require.NoErrorf(t, err, "create ApplyDesire %s", d.GetResourceID())
	}
}

func newLoadApplyDesireStep(id string, dir fs.FS) (Step, error) {
	return &loadApplyDesireStep{id: id, stepDir: dir}, nil
}

type loadDeleteDesireStep struct {
	id      string
	stepDir fs.FS
}

func (s *loadDeleteDesireStep) StepID() string { return s.id }

func (s *loadDeleteDesireStep) Run(ctx context.Context, t *testing.T, h *Harness) {
	t.Helper()
	for _, d := range readJSONsAs[kubeapplier.DeleteDesire](t, s.stepDir) {
		key, err := keys.DeleteDesireKeyFromResourceID(d.GetResourceID())
		require.NoErrorf(t, err, "derive parent for DeleteDesire %s", d.GetResourceID())
		crud, err := h.KubeApplierDBClient.KubeApplier(ManagementCluster).DeleteDesires(key.ResourceParent())
		require.NoError(t, err)
		_, err = crud.Create(ctx, d, nil)
		require.NoErrorf(t, err, "create DeleteDesire %s", d.GetResourceID())
	}
}

func newLoadDeleteDesireStep(id string, dir fs.FS) (Step, error) {
	return &loadDeleteDesireStep{id: id, stepDir: dir}, nil
}

type loadReadDesireStep struct {
	id      string
	stepDir fs.FS
}

func (s *loadReadDesireStep) StepID() string { return s.id }

func (s *loadReadDesireStep) Run(ctx context.Context, t *testing.T, h *Harness) {
	t.Helper()
	for _, d := range readJSONsAs[kubeapplier.ReadDesire](t, s.stepDir) {
		key, err := keys.ReadDesireKeyFromResourceID(d.GetResourceID())
		require.NoErrorf(t, err, "derive parent for ReadDesire %s", d.GetResourceID())
		crud, err := h.KubeApplierDBClient.KubeApplier(ManagementCluster).ReadDesires(key.ResourceParent())
		require.NoError(t, err)
		_, err = crud.Create(ctx, d, nil)
		require.NoErrorf(t, err, "create ReadDesire %s", d.GetResourceID())
	}
}

func newLoadReadDesireStep(id string, dir fs.FS) (Step, error) {
	return &loadReadDesireStep{id: id, stepDir: dir}, nil
}

// readJSONsAs reads every non-key *.json in dir and unmarshals each into a
// fresh *T. The 00-key.json filename is reserved for steps that key a
// resource separately from the resource document itself.
func readJSONsAs[T any](t *testing.T, dir fs.FS) []*T {
	t.Helper()
	entries, err := fs.ReadDir(dir, ".")
	require.NoError(t, err)
	var out []*T
	for _, e := range entries {
		if e.IsDir() || e.Name() == "00-key.json" || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := fs.ReadFile(dir, e.Name())
		require.NoErrorf(t, err, "read %s", e.Name())
		var v T
		require.NoErrorf(t, json.Unmarshal(raw, &v), "unmarshal %s", e.Name())
		out = append(out, &v)
	}
	return out
}
