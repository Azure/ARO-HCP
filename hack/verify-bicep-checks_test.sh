#!/usr/bin/env bash
# Tests the infrastructure Make recipes with a real standalone Bicep compiler.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BICEP="$(command -v "${BICEP:-bicep}")"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/dev-infrastructure/templates" "$WORK/dev-infrastructure/modules/nested"
cd "$WORK/dev-infrastructure"
ln -s "$ROOT/dev-infrastructure/Makefile" Makefile

cat > templates/valid.bicep <<'BICEP'
param name string
output greeting string=name
BICEP
cat > templates/valid.bicepparam <<'BICEP'
using './valid.bicep'
param name='hello'
BICEP
cat > modules/nested/valid.bicep <<'BICEP'
output greeting string='hello'
BICEP
cat > templates/invalid.bicep <<'BICEP'
output broken string = missingSymbol
BICEP
cat > templates/invalid.bicepparam <<'BICEP'
using './valid.bicep'
param name = 42
BICEP

check_recipe() {
    local operation="$1" file="$2" expected_status="${3:-0}" direct_status=0 recipe_status=0 suffix="$1"
    "$BICEP" "$operation" "$file" > "$WORK/direct" 2>&1 || direct_status=$?
    awk 'NF' "$WORK/direct" > "$WORK/expected"
    [[ "$operation" != format ]] || suffix=fmt
    # Restore unformatted input so the recipe must actually format the file.
    if [[ "$operation" == format && -f "$file" ]]; then
        cp "$file" "$WORK/formatted"
        printf '\n\n' >> "$file"
    fi
    make --no-print-directory -s \
        "BICEP=$BICEP" "$file.$suffix" > "$WORK/recipe" 2>&1 || recipe_status=$?
    if (( direct_status != expected_status || (direct_status == 0) != (recipe_status == 0) )); then
        cat "$WORK/direct" "$WORK/recipe"
        echo "FAIL $operation $file: compiler=$direct_status make=$recipe_status"
        exit 1
    fi
    while IFS= read -r diagnostic; do
        if [[ "$(cat "$WORK/recipe")" != *"$diagnostic"* ]]; then
            cat "$WORK/recipe"
            echo "FAIL missing compiler diagnostic: $diagnostic"
            exit 1
        fi
    done < "$WORK/expected"
    if [[ "$operation" == format && -f "$file" ]]; then
        cmp "$WORK/formatted" "$file"
    fi
    echo "ok $operation $file (compiler=$direct_status make=$recipe_status)"
}

for file in templates/valid.bicep templates/valid.bicepparam modules/nested/valid.bicep; do
    check_recipe format "$file"
    check_recipe lint "$file"
done
for file in templates/invalid.bicep templates/invalid.bicepparam; do
    check_recipe lint "$file" 1
done
# A missing input fails both commands, including format's output-filter pipeline.

check_recipe format templates/missing.bicep 1
check_recipe lint templates/missing.bicepparam 1

# Isolate valid aggregate inputs without changing the repository's file selection.
rm templates/invalid.bicep templates/invalid.bicepparam
make --no-print-directory -s \
    "BICEP=$BICEP" parameters= NPROC=2 fmt lint
echo 'All standalone Bicep recipe tests passed'
