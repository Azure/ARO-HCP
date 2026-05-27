#!/bin/bash
# Shared version sorting utility for OpenShift version strings
# Compatible with Linux (GNU sort) and macOS (BSD sort)
# Handles semantic versions including pre-release tags (e.g., "4.20.0-0.nightly-...")

# Portable version sorting function
# Works on both Linux (GNU sort) and macOS (BSD sort)
sort_versions() {
    # Try sort -V (GNU sort, available on Linux and via coreutils on macOS)
    if sort -V /dev/null &>/dev/null 2>&1; then
        sort -V
    # Try gsort -V (GNU sort via Homebrew coreutils on macOS)
    elif command -v gsort &>/dev/null && gsort -V /dev/null &>/dev/null 2>&1; then
        gsort -V
    # Fallback: use Python for semantic version sorting
    elif command -v python3 &>/dev/null; then
        python3 -c '
import sys
from packaging import version
versions = [line.strip() for line in sys.stdin if line.strip()]
try:
    sorted_versions = sorted(versions, key=lambda v: version.parse(v))
    for v in sorted_versions:
        print(v)
except:
    # Fallback to basic string sort if packaging module not available
    for v in sorted(versions):
        print(v)
'
    else
        # Last resort: basic alphanumeric sort (not semver-aware, but better than nothing)
        echo "WARNING: No proper version sorting available. Install GNU coreutils or Python with packaging module for accurate results." >&2
        sort
    fi
}
