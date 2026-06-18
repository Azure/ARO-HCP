CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)

define acr-login
DOCKER_COMMAND="$(CONTAINER_ENGINE)" az acr login --name $(ARO_HCP_IMAGE_ACR)
endef