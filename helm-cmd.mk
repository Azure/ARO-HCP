ifdef DRY_RUN
HELM_CMD ?= helm diff --install --suppress-secrets
else
HELM_CMD ?= helm upgrade --install
endif
