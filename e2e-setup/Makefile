.PHONY: ci lint test

ci: lint test

lint:
	shellcheck *.sh env.defaults env.example
	jsonlint templates/*.json

test:
	test/test-arocurl.sh
	test/test-aro-setup-metadata.sh
