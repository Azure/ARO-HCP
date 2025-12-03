#!/usr/bin/env bash

# Filters the aro-hcp-frontend log to show only ERROR entries and prints an
# indented copy of each entry's "msg" field for easier reading.
# Usage: ./show_frontend_errors.sh [/absolute/path/to/logfile]

set -euo pipefail

DEFAULT_LOG=""
LOG_FILE="${1:-$DEFAULT_LOG}"

if [[ ! -f "$LOG_FILE" ]]; then
  echo "error: log file not found: $LOG_FILE" >&2
  exit 1
fi

python3 - "$LOG_FILE" <<'PY'
import json
import sys

log_path = sys.argv[1]

with open(log_path, "r", encoding="utf-8") as handle:
    for raw_line in handle:
        raw_line = raw_line.strip()
        if not raw_line:
            continue
        try:
            entry = json.loads(raw_line)
        except json.JSONDecodeError:
            continue
        if entry.get("level") != "ERROR":
            continue

        message = entry.get("msg", "")
        if message:
            for msg_line in message.rstrip("\n").splitlines():
                print(f"  {msg_line}")
        print()
PY


