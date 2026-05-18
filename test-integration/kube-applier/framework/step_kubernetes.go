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

	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Step types that interact with the envtest cluster directly:
//
//	NN-kubernetesLoad-description/
//	    object.json     # an Unstructured Kubernetes object; created via the
//	                    # dynamic client. Multiple files create multiple objects.
//
//	NN-kubernetesApply-description/
//	    object.json     # an Unstructured Kubernetes object; updated in place via
//	                    # a fetch + Update round-trip.
//
//	NN-kubernetesDelete-description/
//	    00-key.json     # {apiVersion, kind, namespace, name}
//
// All steps treat namespace as a normal field — there is no implicit "test
// namespace" substitution. Test authors should use the per-test namespace
// the framework creates (the test's directory name, lower-case with hyphens).

type kubeKey struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	// Resource overrides the lower-cased pluralization of Kind for the
	// edge cases where the implicit pluralization is wrong.
	Resource string `json:"resource,omitempty"`
}

func (k kubeKey) gvr() (schema.GroupVersionResource, error) {
	g, v, err := splitAPIVersion(k.APIVersion)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	resource := k.Resource
	if resource == "" {
		resource = strings.ToLower(k.Kind) + "s"
	}
	return schema.GroupVersionResource{Group: g, Version: v, Resource: resource}, nil
}

func splitAPIVersion(av string) (group, version string, err error) {
	parts := strings.SplitN(av, "/", 2)
	switch len(parts) {
	case 1:
		return "", parts[0], nil
	case 2:
		return parts[0], parts[1], nil
	}
	return "", "", fmt.Errorf("invalid apiVersion %q", av)
}

func readKubeKey(t *testing.T, dir fs.FS) kubeKey {
	t.Helper()
	raw, err := fs.ReadFile(dir, "00-key.json")
	require.NoErrorf(t, err, "read 00-key.json")
	var k kubeKey
	require.NoErrorf(t, json.Unmarshal(raw, &k), "unmarshal 00-key.json")
	return k
}

// readUnstructureds reads every non-key *.json file in dir and parses each
// as an Unstructured kube object.
func readUnstructureds(t *testing.T, dir fs.FS) []*unstructured.Unstructured {
	t.Helper()
	entries, err := fs.ReadDir(dir, ".")
	require.NoError(t, err)
	var out []*unstructured.Unstructured
	for _, e := range entries {
		if e.IsDir() || e.Name() == "00-key.json" || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := fs.ReadFile(dir, e.Name())
		require.NoErrorf(t, err, "read %s", e.Name())
		obj := &unstructured.Unstructured{}
		require.NoErrorf(t, obj.UnmarshalJSON(raw), "unmarshal %s", e.Name())
		out = append(out, obj)
	}
	return out
}

// kubernetesLoad: create the supplied object(s) in the cluster.
type kubernetesLoadStep struct {
	id      string
	stepDir fs.FS
}

func (s *kubernetesLoadStep) StepID() string { return s.id }

func (s *kubernetesLoadStep) Run(ctx context.Context, t *testing.T, h *Harness) {
	t.Helper()
	for _, obj := range readUnstructureds(t, s.stepDir) {
		gvr := schema.GroupVersionResource{
			Group:    obj.GroupVersionKind().Group,
			Version:  obj.GroupVersionKind().Version,
			Resource: strings.ToLower(obj.GetKind()) + "s",
		}
		r := h.Dyn.Resource(gvr)
		var ns = obj.GetNamespace()
		if ns != "" {
			_, err := r.Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
			require.NoErrorf(t, err, "kubernetesLoad %s/%s", ns, obj.GetName())
		} else {
			_, err := r.Create(ctx, obj, metav1.CreateOptions{})
			require.NoErrorf(t, err, "kubernetesLoad %s", obj.GetName())
		}
	}
}

func newKubernetesLoadStep(id string, dir fs.FS) (Step, error) {
	return &kubernetesLoadStep{id: id, stepDir: dir}, nil
}

// kubernetesApply: get-and-Update the supplied object(s). Replaces the live
// object's spec/data fields with the fixture's; preserves apiserver-managed
// fields (resourceVersion, uid, ...).
type kubernetesApplyStep struct {
	id      string
	stepDir fs.FS
}

func (s *kubernetesApplyStep) StepID() string { return s.id }

func (s *kubernetesApplyStep) Run(ctx context.Context, t *testing.T, h *Harness) {
	t.Helper()
	for _, obj := range readUnstructureds(t, s.stepDir) {
		gvr := schema.GroupVersionResource{
			Group:    obj.GroupVersionKind().Group,
			Version:  obj.GroupVersionKind().Version,
			Resource: strings.ToLower(obj.GetKind()) + "s",
		}
		r := h.Dyn.Resource(gvr)
		var live *unstructured.Unstructured
		var err error
		if ns := obj.GetNamespace(); ns != "" {
			live, err = r.Namespace(ns).Get(ctx, obj.GetName(), metav1.GetOptions{})
		} else {
			live, err = r.Get(ctx, obj.GetName(), metav1.GetOptions{})
		}
		require.NoErrorf(t, err, "kubernetesApply Get %s", obj.GetName())

		// Carry over apiserver-managed fields, then overwrite mutable fields
		// with the fixture's values.
		obj.SetResourceVersion(live.GetResourceVersion())
		obj.SetUID(live.GetUID())

		if ns := obj.GetNamespace(); ns != "" {
			_, err = r.Namespace(ns).Update(ctx, obj, metav1.UpdateOptions{})
		} else {
			_, err = r.Update(ctx, obj, metav1.UpdateOptions{})
		}
		require.NoErrorf(t, err, "kubernetesApply Update %s", obj.GetName())
	}
}

func newKubernetesApplyStep(id string, dir fs.FS) (Step, error) {
	return &kubernetesApplyStep{id: id, stepDir: dir}, nil
}

// kubernetesDelete: delete the object identified by 00-key.json.
type kubernetesDeleteStep struct {
	id      string
	stepDir fs.FS
}

func (s *kubernetesDeleteStep) StepID() string { return s.id }

func (s *kubernetesDeleteStep) Run(ctx context.Context, t *testing.T, h *Harness) {
	t.Helper()
	k := readKubeKey(t, s.stepDir)
	gvr, err := k.gvr()
	require.NoError(t, err)
	r := h.Dyn.Resource(gvr)
	if k.Namespace != "" {
		err = r.Namespace(k.Namespace).Delete(ctx, k.Name, metav1.DeleteOptions{})
	} else {
		err = r.Delete(ctx, k.Name, metav1.DeleteOptions{})
	}
	if apierrors.IsNotFound(err) {
		return
	}
	require.NoErrorf(t, err, "kubernetesDelete %s/%s", k.Namespace, k.Name)
}

func newKubernetesDeleteStep(id string, dir fs.FS) (Step, error) {
	return &kubernetesDeleteStep{id: id, stepDir: dir}, nil
}
