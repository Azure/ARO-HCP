public.config.json:
	ev2 configuration get --rolloutinfra Prod

config.yaml: sanitizer/sanitize.go sanitizer/sanitizedconfig.go ff.config.json public.config.json
	go run ./sanitizer/... --input ff.config.json --input public.config.json --output config.yaml


