#!/usr/bin/env bash
# Render the committed Graphviz sources. Requires Graphviz (dot).
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
for source in *.dot; do
    dot -Tpng "$source" -o "${source%.dot}.png"
done
