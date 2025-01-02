ifdef DRY_RUN
HELM_CMD ?= helm diff upgrade --install --dry-run=server --suppress-secrets --three-way-merge
else
HELM_CMD ?= helm upgrade --install
endif
