SHELL = /bin/bash

run:
	go run github.com/onsi/ginkgo/v2/ginkgo run --tags E2Etests --junit-report ./report-$(shell date +'%s').xml ./
.PHONY: run
