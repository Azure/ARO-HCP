SHELL = /bin/bash
STAMP ?= 1

render-config:
	$(MAKE) -C ./../tooling/templatize templatize
	./../tooling/templatize/templatize configuration render \
	  --service-config-file ./config.yaml \
	  --config-file-override ./config.msft.clouds-overlay.yaml \
	  --skip-schema-validation \
	  --cloud "$(ARO_HCP_CLOUD)" \
	  --environment "$(ARO_HCP_DEPLOY_ENV)" \
	  --region "$(LOCATION)" \
	  --stamp "$(STAMP)" \
	  --ev2-cloud public \
	  --output "$(CONFIG_OUTPUT)"
.PHONY: render-config

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
