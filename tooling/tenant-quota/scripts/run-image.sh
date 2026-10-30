#!/bin/bash

set -euo pipefail

[[ $# == 4 ]] || { echo "Usage: $0 ENGINE IMAGE CONFIG_FILE SECRETS_DIR" >&2; exit 1; }
engine="$1"
image="$2"
config_file="$3"
secrets_dir="$4"
caller_uid=$(id -u)
caller_gid=$(id -g)
[[ "$caller_uid" != 0 ]] || { echo "Run as the non-root owner of the local credential files" >&2; exit 1; }
engine_info=$("$engine" info --format '{{json .}}')
identity=()
if jq -e '.host.security.rootless == true' <<< "$engine_info" >/dev/null; then
    identity=("--userns=keep-id:uid=65532,gid=65532" --user 65532:65532)
elif jq -e '.SecurityOptions | type == "array" and all(type == "string")' <<< "$engine_info" >/dev/null; then
    if jq -e '.SecurityOptions | any(contains("rootless") or contains("userns"))' <<< "$engine_info" >/dev/null; then
        echo "Rootless or userns-remapped Docker cannot directly read these private host files as a non-root container user; use rootless Podman or make run" >&2
        exit 1
    fi
    identity=(--user "$caller_uid:$caller_gid")
else
    echo "Supported local engines: rootless Podman or rootful Docker without userns remapping; use make run otherwise" >&2
    exit 1
fi

exec "$engine" run --rm "${identity[@]}" -p 8080:8080 \
    -e CONFIG_PATH=/config.yaml \
    -e SECRETS_STORE_PATH=/secrets \
    -v "$config_file:/config.yaml:ro" \
    -v "$secrets_dir:/secrets:ro" \
    "$image"
