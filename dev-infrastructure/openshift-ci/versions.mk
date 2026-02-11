# Tool versions for the ARO-HCP openshift-ci image.
#
# This file is the single source of truth for tool versions used in the
# CI container. Update versions here and run `make verify-versions` to
# validate, then `make test` to confirm the image builds correctly.
#
# Upstream release pages:
#   Go:        https://go.dev/dl/
#   kubectl:   https://dl.k8s.io/release/stable.txt
#   kubelogin: https://github.com/Azure/kubelogin/releases
#   promtool:  https://github.com/prometheus/prometheus/releases

GO_VERSION        ?= 1.25.7
KUBECTL_VERSION   ?= v1.35.0
KUBELOGIN_VERSION ?= v0.2.14
PROMTOOL_VERSION  ?= 3.2.1
