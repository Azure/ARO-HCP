
-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)

# Build flags
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS = -ldflags "\
	-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${ARO_HCP_REVISION}"


# Default target
.DEFAULT_GOAL := test

# Build the binary
test:
	go test github.com/Azure/ARO-HCP/test-int
.PHONY: build


clean:
.PHONY: clean

