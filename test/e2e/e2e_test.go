// Copyright 2025 Microsoft Corporation
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

//go:build E2Etests

package e2e

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

type nodePoolVersionTestTransport func(*http.Request) (*http.Response, error)

func (f nodePoolVersionTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestResolveNodePoolTestVersionValidation(t *testing.T) {
	RegisterFailHandler(Fail)
	for _, tc := range []struct {
		name, minor, version string
		fail, tooOld         bool
	}{
		{name: "bare version fails instead of skipping", minor: "4.21", version: "4.21", fail: true},
		{name: "unsupported nightly is skippable", minor: "4.20", version: "4.20.0-0.nightly-multi-2026-09-23-090319", tooOld: true},
		{name: "supported nightly is preserved", minor: "4.21", version: "4.21.0-0.nightly-multi-2026-09-23-090319"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalClient := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = originalClient })
			http.DefaultClient = &http.Client{Transport: nodePoolVersionTestTransport(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"tags":[{"name":"` + tc.version + `"}]}`)),
				}, nil
			})}
			var version string
			var err error
			failures := InterceptGomegaFailures(func() {
				version, err = resolveNodePoolTestVersion(context.Background(), "nightly", tc.minor, 0)
			})
			if tc.fail {
				if len(failures) != 1 || !strings.Contains(failures[0], "invalid resolved node pool version") {
					t.Fatalf("invalid resolver output should fail the spec, got failures %v and error %v", failures, err)
				}
				return
			}
			if len(failures) != 0 {
				t.Fatalf("valid resolver output should not fail the spec: %v", failures)
			}
			if tc.tooOld {
				if !errors.Is(err, framework.ErrNodePoolVersionTooOld) {
					t.Fatalf("unsupported version should return a skippable minimum error, got %v", err)
				}
			} else if err != nil || version != tc.version {
				t.Fatalf("supported version should be preserved, got version %q and error %v", version, err)
			}
		})
	}
}

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ARO-HCP E2E Tests")
}

var _ = BeforeSuite(func() {
	if err := setup(context.Background()); err != nil {
		panic(err)
	}
})

var _ = AfterSuite(func() {
	// Cleanup is done by Resource Group DeferCleanup
})
