HCPCTL_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

HCPCTL := $(HCPCTL_DIR)/hcpctl

$(HCPCTL):
	make -C $(HCPCTL_DIR) build