CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)

# Podman cannot use 'az acr login' directly (requires a Docker daemon).
# When CONTAINER_ENGINE is podman, use --expose-token and pipe into podman login.
# Falls back to plain az acr login for docker (default).
define acr-login
DOCKER_COMMAND=$(CONTAINER_ENGINE) az acr login --name $(ARO_HCP_IMAGE_ACR)
endef