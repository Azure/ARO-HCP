SHELL = /bin/bash
STAMP=999

materialize: render
	$(MAKE) -C .. update-helm-fixtures
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

validate:
	$(MAKE) -C ./../tooling/templatize templatize
	USER='test' BUILD_ID='1234567890987654321' ./../tooling/templatize/templatize configuration validate --service-config-file ./config.yaml \
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
