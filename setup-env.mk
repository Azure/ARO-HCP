CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)

# Podman cannot use 'az acr login' directly (requires a Docker daemon).
# When CONTAINER_ENGINE is podman, use --expose-token and pipe into podman login.
# Falls back to plain az acr login for docker (default).
define acr-login
@case "$(CONTAINER_ENGINE)" in *podman*) az acr login --name $(ARO_HCP_IMAGE_ACR) --expose-token --output tsv --query accessToken | $(CONTAINER_ENGINE) login $(ARO_HCP_IMAGE_REGISTRY) --username 00000000-0000-0000-0000-000000000000 --password-stdin;; *) az acr login --name $(ARO_HCP_IMAGE_ACR);; esac
endef