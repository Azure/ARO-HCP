SHELL = /bin/bash

.PHONY: generate
generate:
	npm run format-check
	npm run generate
	# autorest.go insists on writing a go.mod file
	# when in azure-arm mode. Remove it.
	rm --force ../internal/api/v*/generated/go.mod

.PHONY: fmt
fmt:
	npm run format
