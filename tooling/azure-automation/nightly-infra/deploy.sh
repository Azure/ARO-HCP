#!/usr/bin/env bash
set -euo pipefail

ENV="nightly"
WHAT_IF=false
CLEAN=false
SKIP_CONFIRM=false

usage() {
  cat <<EOF
Usage: $0 [--env ENV] [--what-if] [--clean] [--yes]
  --env ENV     use ENV (defaults to 'nightly')
  --what-if     run the *.what-if targets instead of real deploy
  --clean       run the *.clean targets to clean up the clusters
  --yes         skip all what-if confirmations
  -h, --help    show this help message
EOF
  exit 1
}

while [[ $# -gt 0 ]]; do
  case $1 in
    --env)    ENV="$2"; shift 2 ;;
    --what-if) WHAT_IF=true; shift ;;
    --clean)   CLEAN=true; shift ;;
    --yes)     SKIP_CONFIRM=true; shift ;;
    -h|--help) usage ;;
    *) echo "Unknown arg: $1"; usage ;;
  esac
done

if $SKIP_CONFIRM; then
  export SKIP_CONFIRM=1
  echo "→ skipping all what-if confirmations"
fi

echo "→ generating config for ENV='$ENV'"
./../../../dev-infrastructure/create-config.sh "$ENV"

if $CLEAN; then
    echo "→ Cleaning up '$ENV'..."
    make DEPLOY_ENV="$ENV" nightly.clean
    exit 0
fi

if $WHAT_IF; then
    echo "→ Running what-if for '$ENV'..."
    cd ../../../dev-infrastructure
    make DEPLOY_ENV="$ENV" nightly.what-if
    exit 0
fi

echo "→ Deploying '$ENV' infra..."
cd ../../../dev-infrastructure
make DEPLOY_ENV="$ENV" nightly
