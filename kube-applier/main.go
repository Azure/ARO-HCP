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

package main

import (
	"os"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/kube-applier/cmd"
)

func main() {
	utilruntime.PanicHandlers = append(utilruntime.PanicHandlers, utils.IncrementPanicMetrics)

	cmdRoot := cmd.NewCmdRoot()
	if err := cmdRoot.Execute(); err != nil {
		cmdRoot.PrintErrln(cmdRoot.ErrPrefix(), err.Error())
		os.Exit(1)
	}
}
