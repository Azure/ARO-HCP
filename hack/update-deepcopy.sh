#!/usr/bin/env bash

# Copyright 2026 Microsoft Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

DEEPCOPY_GEN="${DEEPCOPY_GEN:-deepcopy-gen}"

"${DEEPCOPY_GEN}" \
  --output-file zz_generated.deepcopy.go \
  --go-header-file "${REPO_ROOT}/hack/boilerplate.go.txt" \
  github.com/Azure/ARO-HCP/internal/api \
  github.com/Azure/ARO-HCP/internal/api/arm

# Post-process generated files.
#
# deepcopy-gen resolves the azcorearm.ResourceID type alias to its internal
# definition (github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/internal/resource),
# producing an import that is not accessible outside the Azure SDK module.
# We replace it with the public import path.
#
# Additionally, deepcopy-gen emits .DeepCopyInto() calls for external types
# that do not implement the interface (azcorearm.ResourceID and time.Time).
# We replace those with plain value copies. The "any" interface also gets an
# erroneous .DeepCopyany() call which we replace with a direct assignment.
for f in \
  "${REPO_ROOT}/internal/api/zz_generated.deepcopy.go" \
  "${REPO_ROOT}/internal/api/arm/zz_generated.deepcopy.go"; do

  if [[ ! -f "${f}" ]]; then
    continue
  fi

  # Fix internal Azure SDK import path and type references.
  sed -i \
    -e 's|resource "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/internal/resource"|azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"|g' \
    -e 's|resource\.ResourceID|azcorearm.ResourceID|g' \
    "${f}"

  # Fix pointer-type fields for external types without DeepCopyInto.
  # Pattern:  *out = new(T)   →   *out = new(T)
  #           (*in).DeepCopyInto(*out)   →   **out = **in
  for type in 'azcorearm\.ResourceID' 'time\.Time'; do
    sed -i \
      "/\*out = new(${type})/{n;s/(\*in)\.DeepCopyInto(\*out)/**out = **in/;}" \
      "${f}"
  done

  # Fix value-type azcorearm.ResourceID field (ServiceProviderCluster.ResourceID).
  sed -i \
    's/in\.ResourceID\.DeepCopyInto(&out\.ResourceID)/out.ResourceID = in.ResourceID/g' \
    "${f}"

  # Fix value-type time.Time fields. These appear as:
  #   in.<Field>.DeepCopyInto(&out.<Field>)
  # and need to become:
  #   out.<Field> = in.<Field>
  # We match all known time.Time field names in the codebase.
  for field in LastTransitionTime StartTime ExpirationTimestamp EndOfLifeTimestamp; do
    sed -i \
      "s/in\.${field}\.DeepCopyInto(&out\.${field})/out.${field} = in.${field}/g" \
      "${f}"
  done

  # Fix "any" fields: deepcopy-gen generates .DeepCopyany() which does not
  # exist on interface{}. The initial *out = *in already performs a shallow
  # copy so we just reassign the value.
  sed -i \
    's/\(.*\)\.DeepCopyany()/\1/g' \
    "${f}"
done
