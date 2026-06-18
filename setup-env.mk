CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)

// You can specify which docker command az acr login should use by setting DOCKER_COMMAND accordingly.
// See also https://learn.microsoft.com/en-us/azure/container-registry/container-registry-authentication?tabs=azure-cli#sign-in-by-using-an-alternative-container-tool-instead-of-docker
define acr-login
DOCKER_COMMAND="$(CONTAINER_ENGINE)" az acr login --name $(ARO_HCP_IMAGE_ACR)
endef