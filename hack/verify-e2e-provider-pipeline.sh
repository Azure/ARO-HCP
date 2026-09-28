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

# Checks that the committed provider-registration pipeline still matches what
# hack/generate-e2e-provider-pipeline.sh produces from the E2E subscription
# inventory in config/config-dev-ci.yaml. A diff here means the two have
# drifted — most likely a subscription was onboarded to ci.<env>.e2eSubscriptions
# without regenerating, which would silently skip provider registration on it.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PIPELINE_FILE="$REPO_ROOT/dev-infrastructure/dev-ci/e2e-subscription-providers/pipeline.yaml"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

"$REPO_ROOT/hack/generate-e2e-provider-pipeline.sh" "$tmpdir/pipeline.yaml" >/dev/null

fail=0

if ! diff -u "$PIPELINE_FILE" "$tmpdir/pipeline.yaml"; then
  fail=1
  echo
  echo "ERROR: ${PIPELINE_FILE#"$REPO_ROOT"/} is out of date with the"
  echo "       ci.<env>.e2eSubscriptions inventory in config/config-dev-ci.yaml."
  echo "  run 'make generate-e2e-provider-pipeline' and commit the result"
  echo "  see docs/ci/e2e-subscription-onboarding.md for the full onboarding workflow"
fi

exit "$fail"
