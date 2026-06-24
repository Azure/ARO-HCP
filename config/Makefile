-include ../setup-templatize-env.mk

SHELL = /bin/bash
STAMP ?= 1

render-partial-config:
	$(MAKE) -C ./../tooling/templatize templatize
	./../tooling/templatize/$(TEMPLATIZE_BINARY) configuration render \
	  --service-config-file ./config.yaml \
	  --config-file-override ./config.msft.clouds-overlay.yaml \
	  --skip-schema-validation \
	  --cloud "$(ARO_HCP_CLOUD)" \
	  --environment "$(ARO_HCP_DEPLOY_ENV)" \
	  --region "$(LOCATION)" \
	  --stamp "$(STAMP)" \
	  --ev2-cloud public \
	  --output "$(CONFIG_OUTPUT)"
.PHONY: render-partial-config

materialize: render
	$(MAKE) -C .. update-helm-fixtures
.PHONY: materialize

detect-change: materialize
	@if [ -n "$$(git status --short)" ]; then \
		echo "The following files are out of date after running 'make -C config/ materialize':"; \
		echo; \
		git status --short; \
		echo; \
		git diff; \
		echo; \
		echo "===================================================="; \
		echo "Run 'make -C config/ materialize' and commit the changes."; \
		echo "===================================================="; \
		exit 1; \
	else \
		echo "No changes detected. Config and helm fixtures are up to date."; \
	fi
.PHONY: detect-change

validate:
	$(MAKE) -C ./../tooling/templatize templatize
	USER='test' BUILD_ID='1234567890987654321' ./../tooling/templatize/$(TEMPLATIZE_BINARY) configuration validate --service-config-file ./config.yaml \
	                                                                      --dev-settings-file ./../tooling/templatize/settings.yaml \
	                                                                      --output-dir ./rendered
.PHONY: validate

render:
	rm -rf ./rendered
	$(MAKE) validate
.PHONY: render

rebase-and-materialize: render
	git fetch https://github.com/Azure/ARO-HCP.git main
	git rebase FETCH_HEAD
.PHONY: rebase-and-materialize
