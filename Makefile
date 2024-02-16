SHELL = /bin/bash
TAG ?= $(shell git describe --exact-match 2>/dev/null)
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)

ifeq ($(TAG),)
	VERSION = $(COMMIT)
else
	VERSION = $(TAG)
endif

build-all:
	go build ./...

frontend:
	go build -ldflags "-X github.com/Azure/ARO-HCP/pkg/util/version.Version=$(VERSION)" -o aro-hcp-frontend ./cmd/frontend
