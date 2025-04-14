SHELL = /bin/bash

# Define the binary name
BINARY = prometheus-rules

# Define the source files
SOURCES = $(shell find . -name '*.go')

# Build the binary
$(BINARY): $(SOURCES) $(MAKEFILE_LIST)
	go build -o $(BINARY) .

# Clean the build artifacts
clean:
	rm -f $(BINARY)

.PHONY: clean

kubernetesControlPlane-prometheusRule.yaml:
	mkdir -p $(dir $@)
	wget --quiet --output-document=$@ https://raw.githubusercontent.com/prometheus-operator/kube-prometheus/refs/heads/main/manifests/$(notdir $@)
