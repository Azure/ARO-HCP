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

# shellcheck source=hack/utils.sh
source "${REPO_ROOT}/hack/utils.sh"

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
# We replace it with the public import path and rewrite DeepCopyInto calls on
# ResourceID to use arm.DeepCopyResourceID which round-trips through String/Parse.
#
# Additionally, deepcopy-gen emits .DeepCopyInto() calls for time.Time which
# does not implement the interface. We replace those with plain value copies.
# The "any" interface also gets an erroneous .DeepCopyany() call which we
# replace with a direct assignment.
for f in \
  "${REPO_ROOT}/internal/api/zz_generated.deepcopy.go" \
  "${REPO_ROOT}/internal/api/arm/zz_generated.deepcopy.go"; do

  if [[ ! -f "${f}" ]]; then
    continue
  fi

  # Determine the function prefix based on which package we're in.
  if [[ "${f}" == *"/arm/"* ]]; then
    RESOURCEID_FUNC="DeepCopyResourceID"
  else
    RESOURCEID_FUNC="arm.DeepCopyResourceID"
  fi

  # Fix internal Azure SDK import path and type references.
  os::util::sed \
    -e 's|resource "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/internal/resource"|azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"|g' \
    -e 's|resource\.ResourceID|azcorearm.ResourceID|g' \
    "${f}"

  # Fix pointer-type azcorearm.ResourceID fields: replace two-line
  #     *out = new(azcorearm.ResourceID)
  #     (*in).DeepCopyInto(*out)
  # with single-line call to DeepCopyResourceID.
  os::util::sed -E \
    "/\*out = new\(azcorearm\.ResourceID\)/{N;s|\*out = new\(azcorearm\.ResourceID\)\n[[:space:]]*\(\*in\)\.DeepCopyInto\(\*out\)|*out = ${RESOURCEID_FUNC}(*in)|;}" \
    "${f}"

  # Fix value-type azcorearm.ResourceID field (e.g. ServiceProviderCluster.ResourceID).
  os::util::sed \
    "s/in\.ResourceID\.DeepCopyInto(&out\.ResourceID)/out.ResourceID = *${RESOURCEID_FUNC}(\&in.ResourceID)/g" \
    "${f}"

  # Fix pointer-type time.Time fields (time.Time has no DeepCopyInto).
  os::util::sed \
    '/\*out = new(time\.Time)/{n;s/(\*in)\.DeepCopyInto(\*out)/**out = **in/;}' \
    "${f}"

  # Fix value-type time.Time fields.
  for field in LastTransitionTime StartTime ExpirationTimestamp EndOfLifeTimestamp; do
    os::util::sed \
      "s/in\.${field}\.DeepCopyInto(&out\.${field})/out.${field} = in.${field}/g" \
      "${f}"
  done

  # Fix pointer-type semver.Version fields (github.com/blang/semver/v4).
  os::util::sed \
    '/\*out = new(v4\.Version)/{n;s/(\*in)\.DeepCopyInto(\*out)/**out = **in/;}' \
    "${f}"

  # Fix "any" fields: deepcopy-gen generates .DeepCopyany() which does not
  # exist on interface{}. The initial *out = *in already performs a shallow
  # copy so we just reassign the value.
  os::util::sed \
    's/\(.*\)\.DeepCopyany()/\1/g' \
    "${f}"
done
