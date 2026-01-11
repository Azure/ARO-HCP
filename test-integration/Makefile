TEST_INTEGRATION_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

.DEFAULT_GOAL := test

##@ Testing

.PHONY: test
test:
	FRONTEND_SIMULATION_TESTING=true \
	go test -race github.com/Azure/ARO-HCP/test-integration/...

.PHONY: test-verbose
test-verbose:
	FRONTEND_SIMULATION_TESTING=true \
	go test -v -race github.com/Azure/ARO-HCP/test-integration/...

##@ Cosmos DB Emulator

.PHONY: cosmos-start
cosmos-start:
	$(TEST_INTEGRATION_DIR)/hack/start-cosmos-emulator.sh

.PHONY: cosmos-stop
cosmos-stop:
	$(TEST_INTEGRATION_DIR)/hack/stop-cosmos-emulator.sh

##@ Combined Targets

.PHONY: test-with-cosmos
test-with-cosmos: cosmos-start test cosmos-stop
