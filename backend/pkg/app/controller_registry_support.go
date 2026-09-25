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

package app

const (
	unionKubeApplierInformersControllerName              = "union-kube-applier-informers-controller"
	virtualMachineResourceSKUsCachedReaderControllerName = "fpavirtualmachineresourceskuscachedreader"
)

// Supporting controller: union kube-applier informers.
func registerUnionKubeApplierInformersController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     1,
		instantiate: instantiateUnionKubeApplierInformersController,
	}
}

func instantiateUnionKubeApplierInformersController(controllerContext ControllerContext) (Runnable, error) {
	return controllerContext.UnionKubeApplierInformersController, nil
}

// Supporting controller: Azure SKU cached-reader.
func registerVirtualMachineResourceSKUsCachedReaderController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateVirtualMachineResourceSKUsCachedReaderController,
	}
}

func instantiateVirtualMachineResourceSKUsCachedReaderController(controllerContext ControllerContext) (Runnable, error) {
	return controllerContext.VirtualMachineResourceSKUsCachedReaderController, nil
}
