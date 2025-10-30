LOCATION ?= {{ .region }}
ARO_HCP_IMAGE_ACR ?= {{ .acr.svc.name }}
BACKEND_IMAGE_REPOSITORY ?= {{ .backend.image.repository }}