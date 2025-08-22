SHELL = /bin/bash

# Integration test targets
.PHONY: test-integration test-integration-dry-run test-integration-validate

# Run full integration tests (requires authentication and consent)
test-integration:
	@echo "Running integration tests..."
	@go test -tags=integration -v ./util/...

# Run integration tests in dry-run mode (safe validation)
test-integration-dry-run:
	@echo "Running integration tests in dry-run mode..."
	@INTEGRATION_TEST_DRY_RUN=true go test -tags=integration -v ./util/...