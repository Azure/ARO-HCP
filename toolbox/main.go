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

// Package main is the kubectl entry point for the first-party toolbox image
// (AROSLSRE-2528). It is a verbatim copy of the upstream main at
// k8s.io/kubernetes/cmd/kubectl/kubectl.go, v1.37.1, so that the binary is
// built from this repository's vendored dependency tree with the same
// Microsoft Go FIPS toolchain as every other in-repo image, instead of
// copying a prebuilt upstream binary. Copy upstream's changes to this file
// whenever the vendored k8s.io/kubectl version is bumped.
package main

import (
	"os"

	"k8s.io/component-base/cli"
	"k8s.io/component-base/logs"
	"k8s.io/kubectl/pkg/cmd"
	"k8s.io/kubectl/pkg/cmd/util"

	// Import to initialize client auth plugins.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

func main() {
	// We need to manually parse the arguments looking for verbosity flag and
	// set appropriate level here, because in the normal flow the flag parsing,
	// including the logging verbosity, happens inside cli.RunNoErrOutput.
	// Doing it here ensures we can continue using klog during kubectl command
	// construction, which includes handling plugins and parsing .kuberc file,
	// for example.
	logs.GlogSetter(cmd.GetLogVerbosity(os.Args)) // nolint:errcheck
	command := cmd.NewDefaultKubectlCommand()
	if err := cli.RunNoErrOutput(command); err != nil {
		// Pretty-print the error and exit with an error.
		util.CheckErr(err)
	}
}
