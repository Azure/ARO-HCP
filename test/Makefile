SHELL = /bin/bash

build: update
	go build github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests
.PHONY: build

update: update-bicep-json
.PHONY: update

update-bicep-json:
	hack/update-bicep-json.sh
.PHONY: -bicep-json

verify: verify-bicep-json
.PHONY: update

verify-bicep-json:
	hack/verify-bicep-json.sh
.PHONY: -bicep-json
