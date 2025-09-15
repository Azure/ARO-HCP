include ../.bingo/Variables.mk
SHELL = /bin/bash

.DEFAULT_GOAL := generate

.PHONY: generate
generate: swagger autorest

.PHONY: swagger
swagger:
	npm run format-check
	npm run compile

.PHONY: autorest
autorest: $(GOIMPORTS)
	npm run autorest
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP ../internal

.PHONY: fmt
fmt:
	npm run format
