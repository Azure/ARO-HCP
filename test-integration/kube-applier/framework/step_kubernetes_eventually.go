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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Step type "kubernetesEventually":
//
//	NN-kubernetesEventually-description/
//	    00-key.json   # {apiVersion, kind, namespace, name, [resource], [absent]}
//	    expected.json # subset to match against the live object's JSON shape;
//	                  # ignored when "absent": true is set on the key.
//
// When the key has "absent": true, the step instead waits until Get returns
// IsNotFound. expected.json is then optional and unused.

type kubeEventuallyKey struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	Resource   string `json:"resource,omitempty"`
	Absent     bool   `json:"absent,omitempty"`
}

type kubernetesEventuallyStep struct {
	id      string
	stepDir fs.FS
}

func (s *kubernetesEventuallyStep) StepID() string { return s.id }

func (s *kubernetesEventuallyStep) Run(ctx context.Context, t *testing.T, h *Harness) {
	t.Helper()
	keyRaw, err := fs.ReadFile(s.stepDir, "00-key.json")
	require.NoErrorf(t, err, "read 00-key.json")
	var k kubeEventuallyKey
	require.NoError(t, json.Unmarshal(keyRaw, &k))

	g, v, err := splitAPIVersion(k.APIVersion)
	require.NoErrorf(t, err, "apiVersion %q", k.APIVersion)
	resource := k.Resource
	if resource == "" {
		resource = strings.ToLower(k.Kind) + "s"
	}
	gvr := schema.GroupVersionResource{Group: g, Version: v, Resource: resource}
	r := h.Dyn.Resource(gvr)

	deadline := time.Now().Add(EventuallyTimeout)
	if k.Absent {
		var lastErr error
		for time.Now().Before(deadline) {
			var err error
			if k.Namespace != "" {
				_, err = r.Namespace(k.Namespace).Get(ctx, k.Name, metav1.GetOptions{})
			} else {
				_, err = r.Get(ctx, k.Name, metav1.GetOptions{})
			}
			if apierrors.IsNotFound(err) {
				return
			}
			lastErr = err
			time.Sleep(EventuallyTick)
		}
		t.Fatalf("kubernetesEventually %s: object %s/%s still present after %v (last err: %v)",
			s.id, k.Namespace, k.Name, EventuallyTimeout, lastErr)
		return
	}

	expected := readSingleSubsetJSON(t, s.stepDir)
	var lastObserved any
	for time.Now().Before(deadline) {
		var live any
		var getErr error
		if k.Namespace != "" {
			live, getErr = r.Namespace(k.Namespace).Get(ctx, k.Name, metav1.GetOptions{})
		} else {
			live, getErr = r.Get(ctx, k.Name, metav1.GetOptions{})
		}
		if getErr != nil {
			lastObserved = fmt.Sprintf("Get error: %v", getErr)
			time.Sleep(EventuallyTick)
			continue
		}
		actualMap := jsonRoundTrip(t, live)
		lastObserved = actualMap
		if matchSubset(expected, actualMap) {
			return
		}
		time.Sleep(EventuallyTick)
	}
	t.Fatalf("kubernetesEventually %s: condition not met within %v.\nexpected subset:\n%s\nlast observed:\n%s",
		s.id, EventuallyTimeout, prettyJSON(expected), prettyJSON(lastObserved))
}

func newKubernetesEventuallyStep(id string, dir fs.FS) (Step, error) {
	return &kubernetesEventuallyStep{id: id, stepDir: dir}, nil
}
