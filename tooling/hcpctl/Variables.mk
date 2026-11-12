HCPCTL_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

HCPCTL := $(HCPCTL_DIR)/hcpctl

$(HCPCTL): force-go-build
	make -C $(HCPCTL_DIR) build

# Always consult Go's build cache, including for dependency-only changes.
.PHONY: force-go-build
force-go-build:
