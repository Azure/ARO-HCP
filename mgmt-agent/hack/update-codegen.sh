#!/usr/bin/env bash

# Copyright 2025 Microsoft Corporation
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

THIS_PKG="github.com/Azure/ARO-HCP/mgmt-agent"

MGMT_AGENT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..

source "${KUBE_CODEGEN_SH}"

kube::codegen::gen_helpers \
    --boilerplate "${MGMT_AGENT_ROOT}/hack/boilerplate.go.txt" \
    "${MGMT_AGENT_ROOT}/pkg/apis"

# Note: --applyconfig-openapi-schema is omitted because our types use
# corev1.ResourceList whose resource.Quantity has a oneOf that
# applyconfiguration-gen cannot parse. The apply configs are generated
# correctly without it — ResourceList is handled via built-in heuristics.
kube::codegen::gen_client \
    --with-watch \
    --with-applyconfig \
    --output-dir "${MGMT_AGENT_ROOT}/pkg/generated" \
    --output-pkg "${THIS_PKG}/pkg/generated" \
    --boilerplate "${MGMT_AGENT_ROOT}/hack/boilerplate.go.txt" \
    "${MGMT_AGENT_ROOT}/pkg/apis"
