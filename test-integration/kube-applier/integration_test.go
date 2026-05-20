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

// Package kube_applier_integration runs the kube-applier controllers in-process
// against a real kube-apiserver provided by sigs.k8s.io/controller-runtime's
// envtest (etcd + kube-apiserver binaries; no Docker required) and a mock
// Cosmos KubeApplierDBClient. Each test is described by an artifact directory
// under ./artifacts/. See ./framework for step types and conventions.
//
// KUBEBUILDER_ASSETS must point at the envtest binaries before running these
// tests. The repo's top-level `make test-unit` target sets it automatically;
// see the package README for the manual setup.
package kube_applier_integration

import (
	"embed"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/Azure/ARO-HCP/test-integration/kube-applier/framework"
)

//go:embed artifacts
var artifacts embed.FS

var (
	// testEnv is sigs.k8s.io/controller-runtime's envtest harness. It is
	// started once in TestMain (which boots the kube-apiserver + etcd
	// binaries pointed at by KUBEBUILDER_ASSETS) and stopped after the
	// suite. All cases share this single cluster.
	testEnv *envtest.Environment
	// cfg is the *rest.Config produced by testEnv.Start, scoped to the
	// envtest-managed apiserver. Each test case consumes it via
	// dynamic.NewForConfig to talk to that cluster.
	cfg *rest.Config
)

const setupHelp = `KUBEBUILDER_ASSETS is not set. The kube-applier integration tests need
envtest's kube-apiserver + etcd binaries.

The simplest path is to run the repo target:

    make test-unit

That target downloads the envtest binaries into ./bin/envtest the first time
and exports KUBEBUILDER_ASSETS for the test invocation. It is idempotent and
safe to re-run.

To set up the environment by hand for ad-hoc go test runs:

    make envtest-setup
    export KUBEBUILDER_ASSETS=$(make -s envtest-setup)
    go test ./test-integration/kube-applier/...
`

func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		fmt.Fprint(os.Stderr, setupHelp)
		os.Exit(1)
	}

	testEnv = &envtest.Environment{}
	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		panic(err)
	}

	code := m.Run()

	_ = testEnv.Stop()
	os.Exit(code)
}

func TestKubeApplierIntegration(t *testing.T) {
	cases, err := framework.LoadTestCases(artifacts, "artifacts")
	require.NoError(t, err)
	require.NotEmpty(t, cases, "no artifact directories found under ./artifacts")

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) { tc.RunCase(t, cfg) })
	}
}
