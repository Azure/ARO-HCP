#!/usr/bin/env bash

# Copyright 2017 The Kubernetes Authors.
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

THIS_PKG="github.com/Azure/ARO-HCP/sessiongate"


SESSIONGATE_ROOT=$(dirname "${BASH_SOURCE[0]}")/..

source "${KUBE_CODEGEN_SH}"

kube::codegen::gen_helpers \
    --boilerplate "${SESSIONGATE_ROOT}/hack/boilerplate.go.txt" \
    "${SESSIONGATE_ROOT}/pkg/apis"

kube::codegen::gen_client \
    --with-watch \
    --with-applyconfig \
    --output-dir "${SESSIONGATE_ROOT}/pkg/generated" \
    --output-pkg "${THIS_PKG}/pkg/generated" \
    --boilerplate "${SESSIONGATE_ROOT}/hack/boilerplate.go.txt" \
    "${SESSIONGATE_ROOT}/pkg/apis"
