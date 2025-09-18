include ../.bingo/Variables.mk
SHELL = /bin/bash

.DEFAULT_GOAL := generate

.PHONY: generate
generate: swagger models

.PHONY: swagger
swagger:
	npm run format-check
	npm run compile

.PHONY: models
models: $(GOIMPORTS)
	npm run models
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP ../internal

.PHONY: fmt
fmt:
	npm run format
