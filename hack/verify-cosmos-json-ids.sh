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

# Verifies Cosmos fixture document ids, then interactively offers to fix each
# violation (or skip it).
#
# Usage:
#   ./hack/verify-cosmos-json-ids.sh [dir ...]
#   ./hack/verify-cosmos-json-ids.sh --check [dir ...]   # verify only, no prompts
#
# Interactive prompts (per violation):
#   y / yes  - apply the corrected id
#   n / no   - skip this file
#   a / all  - fix this and all remaining
#   q / quit - stop without fixing the rest

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

CHECK_ONLY=0
DIRS=()
for arg in "$@"; do
	case "${arg}" in
	--check | -check)
		CHECK_ONLY=1
		;;
	*)
		DIRS+=("${arg}")
		;;
	esac
done
if [[ ${#DIRS[@]} -eq 0 ]]; then
	DIRS=(".")
fi

# unescape_tsv reverses escapeTSV from hack/verify-cosmos-ids.
unescape_tsv() {
	local s=$1
	s=${s//\\n/$'\n'}
	s=${s//\\t/$'\t'}
	s=${s//\\\\/\\}
	printf '%s' "${s}"
}

# apply_cosmos_id_fix replaces the top-level "id" value in a JSON file.
# Args: path got_id want_id
apply_cosmos_id_fix() {
	local path=$1
	local got=$2
	local want=$3

	python3 - "${path}" "${got}" "${want}" <<'PY'
import sys
from pathlib import Path

path, got, want = sys.argv[1], sys.argv[2], sys.argv[3]
text = Path(path).read_text()
candidates = [
    (f'"id": "{got}"', f'"id": "{want}"'),
    (f'"id":\t"{got}"', f'"id":\t"{want}"'),
    (f'"id" : "{got}"', f'"id" : "{want}"'),
]
for old, new in candidates:
    if old in text:
        Path(path).write_text(text.replace(old, new, 1))
        sys.exit(0)
print(f"error: could not find top-level id {got!r} in {path}", file=sys.stderr)
sys.exit(1)
PY
}

# interactive_fix_cosmos_ids prompts fix/skip/all/quit for each TSV violation
# line in $1 (multiline string). Uses /dev/tty for prompts when available so
# stdin can still supply scripted answers.
interactive_fix_cosmos_ids() {
	local tsv_blob=$1
	local path got want reason
	local ans
	local fixed=0
	local skipped=0
	local fix_all=0
	local total=0
	local lines=()
	local line

	while IFS= read -r line || [[ -n "${line}" ]]; do
		[[ -z "${line}" ]] && continue
		lines+=("${line}")
	done <<<"${tsv_blob}"

	total=${#lines[@]}
	if [[ "${total}" -eq 0 ]]; then
		return 0
	fi

	echo "Found ${total} Cosmos id mismatch(es)."
	echo

	local i=0
	for line in "${lines[@]}"; do
		i=$((i + 1))
		IFS=$'\t' read -r path got want reason <<<"${line}"
		path=$(unescape_tsv "${path}")
		got=$(unescape_tsv "${got}")
		want=$(unescape_tsv "${want}")
		reason=$(unescape_tsv "${reason}")

		echo "[${i}/${total}] ${path}"
		echo "  got id:      ${got}"
		echo "  correct id:  ${want}"
		if [[ -n "${reason}" ]]; then
			echo "  reason:      ${reason}"
		fi

		if [[ "${fix_all}" -eq 1 ]]; then
			ans=y
		else
			# When stdout is a TTY, prompt on /dev/tty so answers are not mixed
			# with any redirected stdin. Otherwise read answers from stdin
			# (supports printf 'y\n' | ./hack/verify-cosmos-json-ids.sh).
			if [[ -t 1 ]]; then
				read -r -p "  Apply correction? [y/N/a/q] " ans </dev/tty || ans=n
			elif [[ -t 0 ]]; then
				read -r -p "  Apply correction? [y/N/a/q] " ans || ans=n
			else
				echo "  Apply correction? [y/N/a/q] " >&2
				read -r ans || ans=n
			fi
		fi

		case "${ans}" in
		y | Y | yes | YES)
			apply_cosmos_id_fix "${path}" "${got}" "${want}"
			echo "  → fixed"
			fixed=$((fixed + 1))
			;;
		a | A | all | ALL)
			apply_cosmos_id_fix "${path}" "${got}" "${want}"
			echo "  → fixed (and will fix remaining)"
			fixed=$((fixed + 1))
			fix_all=1
			;;
		q | Q | quit | QUIT)
			echo "  → quit"
			skipped=$((skipped + total - i + 1))
			break
			;;
		*)
			echo "  → skipped"
			skipped=$((skipped + 1))
			;;
		esac
		echo
	done

	echo "Done: ${fixed} fixed, ${skipped} skipped."
	if [[ "${skipped}" -gt 0 ]]; then
		return 1
	fi
	return 0
}

set +e
TSV_OUT="$(go run ./hack/verify-cosmos-ids -tsv "${DIRS[@]}" 2>/dev/null)"
RC=$?
set -e

if [[ "${RC}" -eq 0 ]]; then
	echo "OK: no Cosmos document id mismatches."
	exit 0
fi
if [[ "${RC}" -ne 1 ]]; then
	echo "error: verify-cosmos-ids failed with exit ${RC}" >&2
	go run ./hack/verify-cosmos-ids "${DIRS[@]}"
	exit "${RC}"
fi

if [[ "${CHECK_ONLY}" -eq 1 ]]; then
	# Human-readable report for CI / non-interactive use.
	go run ./hack/verify-cosmos-ids "${DIRS[@]}"
	exit 1
fi

interactive_fix_cosmos_ids "${TSV_OUT}"
