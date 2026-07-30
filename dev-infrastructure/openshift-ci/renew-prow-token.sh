#!/bin/bash
set -euo pipefail

# Script to renew the prow-token used by EV2 to trigger Prow E2E gating jobs via Gangway API.
#
# The prow-token is a Kubernetes ServiceAccount token for the "periodic-job-bot" SA in the
# "aro-hcp-prow-ci" namespace on the OpenShift CI cluster. It authenticates requests to the
# Gangway API that trigger postsubmit E2E jobs during EV2 rollouts.
#
# Usage:
#   ./renew-prow-token.sh --token-file /tmp/prow-token.txt
#   ./renew-prow-token.sh --token-file /tmp/prow-token.txt --vault arohcpdev-global
#   ./renew-prow-token.sh --extract
#
# Prerequisites:
#   - secret-sync binary built (cd tooling/secret-sync && make)
#   - For --extract: oc CLI logged into OpenShift CI cluster
#   - For --extract: membership in the aro-hcp-prow-ci Rover group
#
# For full documentation, see docs/sops/renew-prow-token.md

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
SECRET_SYNC="${REPO_ROOT}/tooling/secret-sync/secret-sync"
ENCRYPTED_SECRETS_FILE="${REPO_ROOT}/dev-infrastructure/data/encryptedsecrets.yaml"

OPENSHIFT_CI_NAMESPACE="aro-hcp-prow-ci"
OPENSHIFT_CI_SECRET="api-token-secret"
OPENSHIFT_CI_CONSOLE="https://console-openshift-console.apps.ci.l2s4.p1.openshiftapps.com/"

# STG-global cutover (AROSLSRE-529): stg's global secrets moved to the dedicated
# hcp-stg-global key vault (arohcpstg2-global); the shared arohcpstg-global is retired for stg.
VAULT_NAMES=("arohcpdev-global" "arohcpint-global" "arohcpstg2-global" "arohcpprod-global")

cloud_for_vault() {
    case "$1" in
        arohcpdev-global) echo "dev" ;;
        *)                echo "public" ;;
    esac
}

TOKEN_FILE=""
TARGET_VAULT=""
EXTRACT=false
WORK_FILE=""

cleanup() {
    if [[ -n "$WORK_FILE" ]] && [[ -f "$WORK_FILE" ]]; then
        rm -f "$WORK_FILE"
    fi
}
trap cleanup EXIT

while [[ $# -gt 0 ]]; do
    case "$1" in
        --token-file)
            if [[ $# -lt 2 || "$2" == --* ]]; then
                echo "Error: --token-file requires a file path argument"
                exit 1
            fi
            TOKEN_FILE="$2"
            shift 2
            ;;
        --vault)
            if [[ $# -lt 2 || "$2" == --* ]]; then
                echo "Error: --vault requires a vault name argument"
                echo "Valid vaults: ${VAULT_NAMES[*]}"
                exit 1
            fi
            TARGET_VAULT="$2"
            valid=false
            for v in "${VAULT_NAMES[@]}"; do
                [[ "$v" == "$TARGET_VAULT" ]] && valid=true
            done
            if [[ "$valid" != "true" ]]; then
                echo "Error: unknown vault '$TARGET_VAULT'"
                echo "Valid vaults: ${VAULT_NAMES[*]}"
                exit 1
            fi
            shift 2
            ;;
        --extract)
            EXTRACT=true
            shift
            ;;
        --help|-h)
            echo "Usage: $0 --token-file FILE [--vault VAULT]"
            echo "       $0 --extract [--vault VAULT]"
            echo ""
            echo "Renew the prow-token used by EV2 to trigger Prow E2E gating jobs."
            echo "Registers the token in Azure Key Vaults via secret-sync."
            echo ""
            echo "Options:"
            echo "  --token-file FILE   Path to file containing the new token"
            echo "  --extract           Extract token from OpenShift CI cluster and register it"
            echo "  --vault VAULT       Register only in this vault (default: all 4 vaults)"
            echo "  --help, -h          Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0 --extract"
            echo "  $0 --token-file /tmp/prow-token.txt"
            echo "  $0 --token-file /tmp/prow-token.txt --vault arohcpdev-global"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Run $0 --help for usage"
            exit 1
            ;;
    esac
done

# --- Validate flag combinations ---

if [[ "$EXTRACT" == "true" ]] && [[ -n "$TOKEN_FILE" ]]; then
    echo "Error: --extract and --token-file are mutually exclusive"
    exit 1
fi

if [[ "$EXTRACT" != "true" ]] && [[ -z "$TOKEN_FILE" ]]; then
    echo "Error: --token-file or --extract is required"
    echo "Run $0 --help for usage"
    exit 1
fi

# --- Extract token from cluster if requested ---

if [[ "$EXTRACT" == "true" ]]; then
    if ! command -v oc &> /dev/null; then
        echo "Error: oc CLI is not installed"
        exit 1
    fi

    echo "Extracting token from OpenShift CI cluster..."
    echo "  Namespace: $OPENSHIFT_CI_NAMESPACE"
    echo "  Secret:    $OPENSHIFT_CI_SECRET"

    token=$(oc -n "$OPENSHIFT_CI_NAMESPACE" extract "secret/$OPENSHIFT_CI_SECRET" --to=- --keys=token 2>/dev/null) || {
        echo ""
        echo "Error: could not extract token from cluster."
        echo "  1. Log into the OpenShift CI cluster:"
        echo "     Go to $OPENSHIFT_CI_CONSOLE"
        echo "     Click your name -> 'Copy login command'"
        echo "  2. Make sure you are in the aro-hcp-prow-ci Rover group:"
        echo "     https://rover.redhat.com/groups/edit/members/aro-hcp-prow-ci"
        exit 1
    }

    WORK_FILE=$(mktemp)
    chmod 600 "$WORK_FILE"
    printf '%s' "$token" > "$WORK_FILE"
    unset token
    echo "  Token extracted."
    echo ""
fi

# --- Prepare working copy of the token ---

if [[ -z "$WORK_FILE" ]]; then
    if [[ ! -f "$TOKEN_FILE" ]]; then
        echo "Error: token file not found: $TOKEN_FILE"
        exit 1
    fi

    if [[ ! -s "$TOKEN_FILE" ]]; then
        echo "Error: token file is empty"
        exit 1
    fi

    WORK_FILE=$(mktemp)
    chmod 600 "$WORK_FILE"
    cp "$TOKEN_FILE" "$WORK_FILE"
fi

# Strip trailing newline from the working copy (never modifies the original)
if [[ "$(tail -c 1 "$WORK_FILE" | wc -l)" -gt 0 ]]; then
    echo "Note: stripping trailing newline from token."
    printf '%s' "$(cat "$WORK_FILE")" > "$WORK_FILE"
fi

# --- Check prerequisites ---

if [[ ! -x "$SECRET_SYNC" ]]; then
    echo "Error: secret-sync binary not found at $SECRET_SYNC"
    echo "Build it first: cd tooling/secret-sync && make"
    exit 1
fi

if [[ ! -f "$ENCRYPTED_SECRETS_FILE" ]]; then
    echo "Error: encrypted secrets file not found: $ENCRYPTED_SECRETS_FILE"
    exit 1
fi

# --- Register token in Key Vaults ---

echo "Registering prow-token in Key Vaults..."
echo ""

if [[ -n "$TARGET_VAULT" ]]; then
    vaults=("$TARGET_VAULT")
else
    vaults=("${VAULT_NAMES[@]}")
fi

for vault in "${vaults[@]}"; do
    cloud=$(cloud_for_vault "$vault")
    echo "  $vault (cloud: $cloud)..."
    "$SECRET_SYNC" register \
        --cloud "$cloud" \
        --config-file "$ENCRYPTED_SECRETS_FILE" \
        --keyvault "$vault" \
        --secret-file "$WORK_FILE" \
        --secret-name prow-token
done

echo ""
echo "Done. Updated: $ENCRYPTED_SECRETS_FILE"
echo ""
echo "Next steps:"
echo "  1. Review:   git diff dev-infrastructure/data/encryptedsecrets.yaml"
echo "  2. Validate: cd tooling/secret-sync && make test-decrypt"
echo "  3. Commit:   git add dev-infrastructure/data/encryptedsecrets.yaml"
echo "  4. After PR merge:"
echo "     - Dev: deployed automatically"
echo "     - Int/Stg/Prod: update SDP pipelines (https://aka.ms/arohcp-pipelines)"
