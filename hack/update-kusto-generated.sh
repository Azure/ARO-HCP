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

# Regenerates dev-infrastructure/modules/logs/kusto/main.bicep (from
# main.bicep.tmpl), computed in dependency order via tooling/kustoctl's
# kqlfunc package (the same mechanism "kustoctl validate kql" uses), so a
# function always loads after anything it depends on without that order
# being hand-maintained. Run this after adding, removing, or
# re-dependencying a function file, and commit the result.
#
# The test harness's manifest (database -> tables/functions) is not part of
# this: it is computed in-process at test time by
# dev-infrastructure/modules/logs/kusto/test/runner.go (function files
# grouped by database via the same kqlfunc package; tables discovered by
# scanning test fixtures' own "ingestRows" steps), never written to disk or
# committed, and fully decoupled from this production deployment generation.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUSTO_DIR="${REPO_ROOT}/dev-infrastructure/modules/logs/kusto"

go run "${REPO_ROOT}/tooling/kustoctl" generate kql-bicep \
  --functions-dir "${KUSTO_DIR}/functions" \
  --template "${KUSTO_DIR}/main.bicep.tmpl" \
  --output "${KUSTO_DIR}/main.bicep"
