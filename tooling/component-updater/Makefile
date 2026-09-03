.PHONY: build check update clean test help

BINARY_NAME := component-updater
CONFIG_FILE := config.yaml
COMPONENT ?=
SUBSCRIPTION_ID ?=

COMPONENT_FLAGS := $(if $(COMPONENT),--component $(COMPONENT))
SUBSCRIPTION_FLAGS := $(if $(SUBSCRIPTION_ID),--subscription-id $(SUBSCRIPTION_ID))

.DEFAULT_GOAL := help

build:
	@go build -o $(BINARY_NAME) .

check: build
	@./$(BINARY_NAME) check --config $(CONFIG_FILE) $(COMPONENT_FLAGS) $(SUBSCRIPTION_FLAGS)

update: build
	@./$(BINARY_NAME) update --config $(CONFIG_FILE) $(COMPONENT_FLAGS) $(SUBSCRIPTION_FLAGS)

test:
	@go test ./...

clean:
	rm -f $(BINARY_NAME)

help:
	@echo "Available targets:"
	@echo "  build   - Build the component-updater binary"
	@echo "  check   - Show available version bumps (dry-run)"
	@echo "  update  - Apply version bumps to config files"
	@echo "  test    - Run tests"
	@echo "  clean   - Remove built binary"
	@echo ""
	@echo "Variables:"
	@echo "  COMPONENT       - Specific component to check/update"
	@echo "  SUBSCRIPTION_ID - Azure subscription ID"
	@echo ""
	@echo "Examples:"
	@echo "  make check"
	@echo "  make check COMPONENT=grafana"
	@echo "  make update COMPONENT=aks-kubernetes"
