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

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
vendor_tmp=$(mktemp -d)
trap 'rm -rf "${vendor_tmp}"' EXIT

# Compare full trees without overwriting local changes. This catches modified,
# missing and extra files, including files not tracked by Git.
go work vendor -o "${vendor_tmp}/workspace"
(cd image-sync/oc-mirror/acrauth && GOWORK=off go mod vendor -o "${vendor_tmp}/acrauth")

failed=0
diff -qr vendor "${vendor_tmp}/workspace" || failed=1
diff -qr image-sync/oc-mirror/acrauth/vendor "${vendor_tmp}/acrauth" || failed=1
if [[ ${failed} -ne 0 ]]; then
    echo "Vendored dependencies are out of date. Run 'make vendor' and commit both vendor trees and module changes." >&2
    exit 1
fi
