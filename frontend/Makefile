SHELL = /bin/bash
TAG ?= $(shell git describe --exact-match 2>/dev/null)
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)

ifeq ($(TAG),)
	VERSION = $(COMMIT)
else
	VERSION = $(TAG)
endif

frontend:
	go build -ldflags "-X main.Version=$(VERSION)" -o aro-hcp-frontend .

clean:
	rm --force aro-hcp-frontend
