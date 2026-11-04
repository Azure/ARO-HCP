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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

func TestLabelNodeEgressAssignable(t *testing.T) {
	cacheMessage := "autoscaling.openshift.io/ManagedNode infra config cache not synchronized"
	cacheError := apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "worker", errors.New(cacheMessage))
	for _, testCase := range []struct {
		name            string
		responseError   *apierrors.StatusError
		failingAttempts int
		wantAttempts    int
		wantError       bool
	}{
		{name: "success", wantAttempts: 1},
		{name: "cache synchronizes after multiple retries", responseError: cacheError, failingAttempts: 2, wantAttempts: 3},
		{name: "cache retry budget exhausted", responseError: cacheError, failingAttempts: retry.DefaultBackoff.Steps, wantAttempts: retry.DefaultBackoff.Steps, wantError: true},
		{name: "unrelated forbidden fails immediately", responseError: apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "worker", errors.New("permission denied")), failingAttempts: 1, wantAttempts: 1, wantError: true},
		{name: "partial cache message fails immediately", responseError: apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "worker", errors.New("infra config cache not synchronized")), failingAttempts: 1, wantAttempts: 1, wantError: true},
		{name: "matching message without forbidden fails immediately", responseError: apierrors.NewInternalError(errors.New(cacheMessage)), failingAttempts: 1, wantAttempts: 1, wantError: true},
		{name: "not found fails immediately", responseError: apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, "worker"), failingAttempts: 1, wantAttempts: 1, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				attempt := int(attempts.Add(1))
				if request.Method != http.MethodPatch || request.URL.Path != "/api/v1/nodes/worker" {
					t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
				}
				if contentType := request.Header.Get("Content-Type"); contentType != string(types.MergePatchType) {
					t.Errorf("patch content type = %q, want %q", contentType, types.MergePatchType)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read patch body: %v", err)
				}
				if string(body) != `{"metadata":{"labels":{"k8s.ovn.org/egress-assignable":""}}}` {
					t.Errorf("unexpected patch on attempt %d: %s", attempt, body)
				}
				writer.Header().Set("Content-Type", "application/json")
				if attempt <= testCase.failingAttempts {
					status := testCase.responseError.ErrStatus.DeepCopy()
					status.APIVersion = "v1"
					status.Kind = "Status"
					writer.WriteHeader(int(status.Code))
					if err := json.NewEncoder(writer).Encode(status); err != nil {
						t.Errorf("encode API error: %v", err)
					}
					return
				}
				if _, err := io.WriteString(writer, `{"apiVersion":"v1","kind":"Node","metadata":{"name":"worker"}}`); err != nil {
					t.Errorf("write node response: %v", err)
				}
			}))
			t.Cleanup(server.Close)

			err := LabelNodeEgressAssignable(t.Context(), &rest.Config{Host: server.URL}, "worker")
			if actual := int(attempts.Load()); actual != testCase.wantAttempts {
				t.Errorf("PATCH attempts = %d, want %d", actual, testCase.wantAttempts)
			}
			if !testCase.wantError {
				if err != nil {
					t.Fatalf("label node: %v", err)
				}
				return
			}
			var statusError *apierrors.StatusError
			if !errors.As(err, &statusError) {
				t.Fatalf("expected wrapped API status error, got %v", err)
			}
			if statusError.ErrStatus.Code != testCase.responseError.ErrStatus.Code || statusError.ErrStatus.Reason != testCase.responseError.ErrStatus.Reason || statusError.Error() != testCase.responseError.Error() {
				t.Errorf("API error was not preserved: got %#v, want %#v", statusError.ErrStatus, testCase.responseError.ErrStatus)
			}
		})
	}
}
