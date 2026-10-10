KUSTO_EMULATOR_IMAGE ?= mcr.microsoft.com/azuredataexplorer/kustainer-linux:latest
EMULATOR_NAME ?= kusto-emulator
EMULATOR_PORT ?= 8082
KQL_ROOT ?= $(abspath ..)
JQ ?= jq
JQ_FMT = $(JQ) --tab 'def sort_keys: if type == "object" then to_entries | sort_by(.key) | map(.value |= sort_keys) | from_entries elif type == "array" then map(sort_keys) else . end; sort_keys'

.DEFAULT_GOAL := test

test:
	@if ! command -v podman >/dev/null 2>&1 && ! command -v docker >/dev/null 2>&1; then \
		echo "Error: neither podman nor docker found"; exit 1; \
	fi; \
	RUNTIME=$$(command -v podman 2>/dev/null || command -v docker 2>/dev/null); \
	echo "Starting Kusto emulator..."; \
	$$RUNTIME rm -f $(EMULATOR_NAME) 2>/dev/null || true; \
	$$RUNTIME run -d --name $(EMULATOR_NAME) \
		-e ACCEPT_EULA=Y \
		-p 127.0.0.1:$(EMULATOR_PORT):8080 \
		$(KUSTO_EMULATOR_IMAGE); \
	trap "echo 'Stopping Kusto emulator...'; $$RUNTIME rm -f $(EMULATOR_NAME) 2>/dev/null || true" EXIT; \
	echo "Waiting for Kusto emulator to be ready..."; \
	for i in $$(seq 1 60); do \
		if curl -sf http://localhost:$(EMULATOR_PORT)/v1/rest/mgmt -d '{"csl":".show version"}' -H "Content-Type: application/json" >/dev/null 2>&1; then \
			break; \
		fi; \
		sleep 2; \
	done; \
	if ! curl -sf http://localhost:$(EMULATOR_PORT)/v1/rest/mgmt -d '{"csl":".show version"}' -H "Content-Type: application/json" >/dev/null 2>&1; then \
		echo "ERROR: Kusto emulator failed to start after 120 seconds"; \
		exit 1; \
	fi; \
	echo "Running Kusto function tests..."; \
	KUSTO_ENDPOINT=http://localhost:$(EMULATOR_PORT) \
	KUSTO_KQL_ROOT=$(KQL_ROOT) \
	go test -v -timeout 120s ./...
.PHONY: test

fmt:
	@for f in $$(find artifacts -name '*.json'); do \
		$(JQ_FMT) "$$f" > "$$f.tmp" && mv "$$f.tmp" "$$f"; \
	done; \
	echo "Formatted $$(find artifacts -name '*.json' | wc -l) JSON files"
.PHONY: fmt

verify-fmt:
	@failed=0; \
	for f in $$(find artifacts -name '*.json'); do \
		if ! $(JQ_FMT) "$$f" | diff -q - "$$f" >/dev/null 2>&1; then \
			echo "FAIL: $$f is not formatted (run 'make -C $$(pwd) fmt')"; \
			failed=1; \
		fi; \
	done; \
	if [ "$$failed" -eq 1 ]; then exit 1; fi; \
	echo "All JSON fixtures are formatted"
.PHONY: verify-fmt
