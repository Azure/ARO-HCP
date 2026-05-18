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
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/keys"
)

// Step type "desireEventually":
//
//	NN-desireEventually-description/
//	    00-key.json   # {"resourceID": ".../applyDesires/foo"}
//	    expected.json # subset to match against the observed desire's JSON
//	                  # shape; see matchSubset in subset.go for the rule.
//
// The kind of *Desire (Apply/Delete/Read) is inferred from the resourceID's
// leaf resource type. The step polls Cosmos via the matching CRUD's Get
// until matchSubset succeeds, or EventuallyTimeout elapses.

type desireEventuallyStep struct {
	id      string
	stepDir fs.FS
}

func (s *desireEventuallyStep) StepID() string { return s.id }

func (s *desireEventuallyStep) Run(ctx context.Context, t *testing.T, h *Harness) {
	t.Helper()

	keyRaw, err := fs.ReadFile(s.stepDir, "00-key.json")
	require.NoErrorf(t, err, "read 00-key.json")
	var key struct {
		ResourceID string `json:"resourceID"`
	}
	require.NoError(t, json.Unmarshal(keyRaw, &key))

	expected := readSingleSubsetJSON(t, s.stepDir)

	id, err := azcorearm.ParseResourceID(key.ResourceID)
	require.NoErrorf(t, err, "parse resourceID %q", key.ResourceID)

	leafType := id.ResourceType.Types[len(id.ResourceType.Types)-1]
	getter, ok := desireGetters[strings.ToLower(leafType)]
	if !ok {
		t.Fatalf("desireEventually: cannot infer Desire type from leaf %q in %q", leafType, key.ResourceID)
	}

	deadline := time.Now().Add(EventuallyTimeout)
	var lastObserved any
	for time.Now().Before(deadline) {
		actual, getErr := getter(ctx, h.KubeApplierDBClient, id)
		if getErr != nil {
			lastObserved = fmt.Sprintf("Get error: %v", getErr)
			time.Sleep(EventuallyTick)
			continue
		}
		actualMap := jsonRoundTrip(t, actual)
		lastObserved = actualMap
		if matchSubset(expected, actualMap) {
			return
		}
		time.Sleep(EventuallyTick)
	}
	t.Fatalf("desireEventually %s: condition not met within %v.\nexpected subset:\n%s\nlast observed:\n%s",
		s.id, EventuallyTimeout, prettyJSON(expected), prettyJSON(lastObserved))
}

func newDesireEventuallyStep(id string, dir fs.FS) (Step, error) {
	return &desireEventuallyStep{id: id, stepDir: dir}, nil
}

// desireGetters maps the lower-cased leaf resource type from the ResourceID
// to a function that fetches that *Desire kind. Adding a fourth desire type
// in the future is one entry in this map.
var desireGetters = map[string]func(ctx context.Context, kac database.KubeApplierDBClient, id *azcorearm.ResourceID) (any, error){
	strings.ToLower(kubeapplier.ApplyDesireResourceTypeName): func(ctx context.Context, kac database.KubeApplierDBClient, id *azcorearm.ResourceID) (any, error) {
		k, err := keys.ApplyDesireKeyFromResourceID(id)
		if err != nil {
			return nil, err
		}
		c, err := kac.KubeApplier(ManagementCluster).ApplyDesires(k.ResourceParent())
		if err != nil {
			return nil, err
		}
		return c.Get(ctx, id.Name)
	},
	strings.ToLower(kubeapplier.DeleteDesireResourceTypeName): func(ctx context.Context, kac database.KubeApplierDBClient, id *azcorearm.ResourceID) (any, error) {
		k, err := keys.DeleteDesireKeyFromResourceID(id)
		if err != nil {
			return nil, err
		}
		c, err := kac.KubeApplier(ManagementCluster).DeleteDesires(k.ResourceParent())
		if err != nil {
			return nil, err
		}
		return c.Get(ctx, id.Name)
	},
	strings.ToLower(kubeapplier.ReadDesireResourceTypeName): func(ctx context.Context, kac database.KubeApplierDBClient, id *azcorearm.ResourceID) (any, error) {
		k, err := keys.ReadDesireKeyFromResourceID(id)
		if err != nil {
			return nil, err
		}
		c, err := kac.KubeApplier(ManagementCluster).ReadDesires(k.ResourceParent())
		if err != nil {
			return nil, err
		}
		return c.Get(ctx, id.Name)
	},
}

// readSingleSubsetJSON reads the (single) non-key JSON file in dir and
// unmarshals it into a generic interface{} suitable for matchSubset.
func readSingleSubsetJSON(t *testing.T, dir fs.FS) any {
	t.Helper()
	entries, err := fs.ReadDir(dir, ".")
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || e.Name() == "00-key.json" || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := fs.ReadFile(dir, e.Name())
		require.NoErrorf(t, err, "read %s", e.Name())
		var v any
		require.NoErrorf(t, json.Unmarshal(raw, &v), "unmarshal %s", e.Name())
		return v
	}
	t.Fatal("no expected JSON found alongside 00-key.json")
	return nil
}

// jsonRoundTrip marshals then unmarshals v through encoding/json so that the
// resulting tree is built out of map[string]any / []any / float64 / string /
// bool / nil — the canonical shape matchSubset works against.
func jsonRoundTrip(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoErrorf(t, err, "marshal observed for compare")
	var out any
	require.NoErrorf(t, json.Unmarshal(raw, &out), "unmarshal observed for compare")
	return out
}

func prettyJSON(v any) string {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}
