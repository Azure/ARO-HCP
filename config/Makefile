materialize:
	@../templatize.sh dev > public-cloud-dev.json
	@../templatize.sh cs-pr > public-cloud-cs-pr.json
	@USER=tst ../templatize.sh personal-dev > public-cloud-personal-dev.json
.PHONY: materialize

detect-change: materialize
	@diff_output=$$(git diff -- './*.json'); \
	if [ -n "$$diff_output" ]; then \
		echo "Please review the diffs below:\n\n"; \
		echo "$$diff_output"; \
		echo "\n\n===================================================="; \
		echo "\n\nOnce you reviewed the changes and consider them meaningful, commit them."; \
		echo "\n\n===================================================="; \
		exit 1; \
	else \
		echo "No changes detected in the configuration files."; \
	fi
.PHONY: detect-change
