.PHONY: build run dry-run test clean help

BINARY_NAME := rightsize-requests
CONFIG_FILE ?= ../../config/config.yaml
GRAFANA_URL ?=
WINDOW ?= 14d
MARGIN ?= 1.25
DATASOURCE_PATTERN ?=

DS_FLAG := $(if $(DATASOURCE_PATTERN),--datasource-pattern $(DATASOURCE_PATTERN))

.DEFAULT_GOAL := help

build:
	@go build -o $(BINARY_NAME) .

run: build
	@./$(BINARY_NAME) --grafana-url "$(GRAFANA_URL)" --config $(CONFIG_FILE) --window $(WINDOW) --margin $(MARGIN) $(DS_FLAG)

dry-run: build
	@./$(BINARY_NAME) --grafana-url "$(GRAFANA_URL)" --config $(CONFIG_FILE) --window $(WINDOW) --margin $(MARGIN) $(DS_FLAG) --dry-run

test:
	@go test ./...

clean:
	@rm -f $(BINARY_NAME)

help:
	@echo "Targets:"
	@echo "  build     - build the rightsize-requests binary"
	@echo "  dry-run   - report proposed request changes without editing (set GRAFANA_URL)"
	@echo "  run       - query Grafana and edit config.yaml in place (set GRAFANA_URL)"
	@echo "  test      - run unit tests"
	@echo "  clean     - remove the built binary"
	@echo ""
	@echo "Variables: GRAFANA_URL, CONFIG_FILE, WINDOW, MARGIN, DATASOURCE_PATTERN"
	@echo ""
	@echo "Example:"
	@echo "  make dry-run GRAFANA_URL=https://arohcp-prod-xxxx.grafana.azure.com/"
