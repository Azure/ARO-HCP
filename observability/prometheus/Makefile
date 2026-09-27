pull-chart:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts;
	helm repo update;
	helm dependency build deploy/
.PHONY: pull-chart

test:
	go test -count=1 -v scheduling_test.go
.PHONY: test
