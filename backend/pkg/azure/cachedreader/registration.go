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

package cachedreader

import (
	"strings"

	"github.com/Azure/ARO-HCP/internal/controllerregistry"
)

func Register[Context any](registry map[string]controllerregistry.Registration[Context], instance func(Context) *FPAVirtualMachineResourceSKUsCachedReaderController) {
	registry[strings.ToLower(FPAVirtualMachineResourceSKUsCachedReaderControllerName)] = controllerregistry.Registration[Context]{
		Workers: 20,
		Instantiate: func(context Context) (controllerregistry.Runnable, error) {
			return instance(context), nil
		},
	}
}
