#!/bin/bash

set -euo pipefail

# Credential helper invoked by `az acr login` via DOCKER_COMMAND. It receives a
# `docker login` invocation and writes the resulting credential into auth.json
# for oc-mirror to consume.
#
# The credential is passed differently depending on the Azure CLI version:
#   < 2.88.0  ->  login --username <user> --password <token> <registry>
#   >= 2.88.0 ->  login --username <user> --password-stdin <registry>, token on stdin
# Parse the flags rather than relying on positional arguments so both forms work.
# `az acr login` also probes this helper with `ps` before sending the login
# (get_docker_command in azure-cli's acr/custom.py) and treats *any* stderr
# output from that probe as a fatal "container runtime unusable" error --
# it never looks at the exit code. Non-login invocations must therefore
# exit 0 and stay silent, or the login is never attempted.

if [[ "${1:-}" != "login" ]]; then
    exit 0
fi
shift

USERNAME=""
PASSWORD=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --username|-u)
            USERNAME="$2"
            shift 2
            ;;
        --password|-p)
            PASSWORD="$2"
            shift 2
            ;;
        --password-stdin)
            # Command substitution already strips trailing newlines; also drop a
            # trailing CR so a CRLF-terminated token does not corrupt the value.
            PASSWORD="$(cat)"
            PASSWORD="${PASSWORD%$'\r'}"
            shift
            ;;
        *)
            shift
            ;;
    esac
done

if [[ -z "${USERNAME}" || -z "${PASSWORD}" ]]; then
    echo "docker-login.sh: could not determine registry credentials from 'docker login' arguments" >&2
    exit 1
fi

# -w0 keeps the value on a single line; auth.json consumers expect an unwrapped string.
AUTH=$(printf '%s:%s' "${USERNAME}" "${PASSWORD}" | base64 -w0)

AUTH_FILE="${XDG_RUNTIME_DIR}/containers/auth.json"
TMP_AUTH_FILE="${XDG_RUNTIME_DIR}/containers/tmp-auth.json"

jq --arg registry "$REGISTRY_URL" --arg auth "$AUTH" '.auths[$registry] = { "auth": $auth }' "${AUTH_FILE}" > "${TMP_AUTH_FILE}"
# Prefer mv: within a directory it is atomic, so a reader never sees a partial
# file. The dry-run make targets bind-mount auth.json itself, and rename() over
# a mount point fails with EBUSY, so fall back to a copy in that case.
if ! mv "${TMP_AUTH_FILE}" "${AUTH_FILE}" 2>/dev/null; then
    cp "${TMP_AUTH_FILE}" "${AUTH_FILE}"
    rm -f "${TMP_AUTH_FILE}"
fi
