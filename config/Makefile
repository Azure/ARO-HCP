SHELL = /bin/bash
STAMP=999

materialize: render
.PHONY: materialize

detect-change: materialize
	@diff_output=$$(git diff -- './*.json'); \
	if [ -n "$$diff_output" ]; then \
		echo "Please review the diffs below:\n\n"; \
		echo "$$diff_output"; \
		echo "\n\n===================================================="; \
		echo "\n\nOnce you reviewed the changes and consider them meaningful, update them by running "make -C config/ materialize" and commit your changes."; \
		echo "\n\n===================================================="; \
		exit 1; \
	else \
		echo "No changes detected in the configuration files."; \
	fi
.PHONY: detect-change

# This target validates that all object types in the schema have the `additionalProperties` field defined
validate-additional-properties:
	@paths=$$(jq -r 'paths(objects | select(has("type") and .type == "object" and (has("additionalProperties") | not))) | "$$." + (map(tostring) | join("."))' ./config.schema.json); \
	if [ -n "$$paths" ]; then \
		echo "****************************** Schema error ******************************"; \
		echo "Object definitions missing an \`additionalProperties\` property found at:"; \
		echo "$$paths"; \
		echo "Please add \`\"additionalProperties\": false\` to the object definitions in config.schema.yaml."; \
		echo "If you are sure that the object should allow additional properties, you can set \`\"additionalProperties\": true\` instead."; \
		echo "**************************************************************************"; \
		exit 1; \
	fi

validate: validate-additional-properties
	$(MAKE) -C ./../tooling/templatize templatize
	USER='test' ./../tooling/templatize/templatize configuration validate --service-config-file ./config.yaml \
	                                                                      --dev-settings-file ./../tooling/templatize/settings.yaml \
	                                                                      --digest-file ./dev.digests.yaml \
	                                                                      --output-dir ./rendered $(UPDATE)
.PHONY: validate

render:
	rm -rf ./rendered
	$(MAKE) validate UPDATE="--update"
.PHONY: render

rebase-and-materialize: render
	git fetch https://github.com/Azure/ARO-HCP.git main
	git rebase FETCH_HEAD
.PHONY: rebase-and-materialize
