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

package ocadminspectcmd

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestNormalizeNamespaces(t *testing.T) {
	got := normalizeNamespaces([]string{"kube-system", "ns/ocm-stg-abc", "namespace/foo", " bar ", "kube-system", ""})
	want := []string{"kube-system", "ocm-stg-abc", "foo", "bar"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeNamespaces = %v, want %v", got, want)
	}
}

func TestValidate(t *testing.T) {
	ctx := context.Background()
	min := time.Date(2026, 9, 1, 18, 31, 35, 0, time.UTC)
	max := time.Date(2026, 9, 1, 19, 37, 9, 0, time.UTC)

	base := func() *RawOptions {
		o := DefaultOptions()
		o.Kusto = "hcp-dev-us-2"
		o.Region = "eastus2"
		o.TimestampMin = min
		o.TimestampMax = max
		return o
	}

	t.Run("missing namespace errors", func(t *testing.T) {
		if _, err := base().Validate(ctx, nil); err == nil {
			t.Errorf("expected error when no namespace is given")
		}
	})

	t.Run("missing cluster is allowed (deferred to discovery)", func(t *testing.T) {
		o := base()
		o.Namespaces = []string{"kube-system"}
		v, err := o.Validate(ctx, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.ClusterName != "" {
			t.Errorf("expected empty ClusterName, got %q", v.ClusterName)
		}
	})

	t.Run("timestamp-min after timestamp-max errors", func(t *testing.T) {
		o := base()
		o.Namespaces = []string{"kube-system"}
		o.TimestampMin = max
		o.TimestampMax = min
		if _, err := o.Validate(ctx, nil); err == nil {
			t.Errorf("expected error when timestamp-min is after timestamp-max")
		}
	})

	t.Run("cluster and window pass through", func(t *testing.T) {
		o := base()
		o.Namespaces = []string{"kube-system"}
		o.ManagementCluster = "aro-hcp-mgmt-1"
		v, err := o.Validate(ctx, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.ClusterName != "aro-hcp-mgmt-1" {
			t.Errorf("ClusterName = %q, want aro-hcp-mgmt-1", v.ClusterName)
		}
		if !v.TimestampMax.Equal(max) || !v.TimestampMin.Equal(min) {
			t.Errorf("window = [%v..%v], want [%v..%v]", v.TimestampMin, v.TimestampMax, min, max)
		}
	})

	t.Run("rejects path-traversal / invalid namespaces", func(t *testing.T) {
		for _, ns := range []string{"../etc", "a/b", "..", "Kube-System"} {
			o := base()
			o.Namespaces = []string{ns}
			if _, err := o.Validate(ctx, nil); err == nil {
				t.Errorf("expected error for invalid namespace %q", ns)
			}
		}
	})

	t.Run("out rejects namespace flags", func(t *testing.T) {
		o := base()
		o.Out = "capture.kshrk"
		o.Namespaces = []string{"kube-system"}
		if _, err := o.Validate(ctx, nil); err == nil {
			t.Errorf("expected error when --out is combined with --namespace")
		}
	})

	t.Run("out rejects positional namespace args", func(t *testing.T) {
		o := base()
		o.Out = "capture.kshrk"
		if _, err := o.Validate(ctx, []string{"ns/ocm-stg-abc"}); err == nil {
			t.Errorf("expected error when --out is combined with a positional ns/<name> argument")
		}
	})

	t.Run("out alone needs no namespace", func(t *testing.T) {
		o := base()
		o.Out = "capture.kshrk"
		o.ManagementCluster = "aro-hcp-mgmt-1"
		v, err := o.Validate(ctx, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(v.Namespaces) != 0 {
			t.Errorf("Namespaces = %v, want empty", v.Namespaces)
		}
	})

	t.Run("positional args are treated as namespaces", func(t *testing.T) {
		o := base()
		v, err := o.Validate(ctx, []string{"ns/ocm-stg-abc"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(v.Namespaces, []string{"ocm-stg-abc"}) {
			t.Errorf("Namespaces = %v, want [ocm-stg-abc]", v.Namespaces)
		}
	})
}
