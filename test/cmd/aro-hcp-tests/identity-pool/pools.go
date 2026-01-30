package identitypool

type identityPool struct {
	Size                  int
	ResourceGroupBaseName string
	Location              string
	SubscriptionIDHash    string
}

// Pool size calculations are based on the limit of role assignments per subscription (4000)
//
// Rules for pool size calculation:
// * Each HCP created in resourceGroupScope mode consumes 24 role assignments
// * Each HCP created in resourceScope mode consumes 41 role assignments
// * The e2e suite runs all tests using resourceGroupScope except for one test, which uses resourceScope
// * Leave at least 100 role assignments free for other things
//
// max-concurrency = floor(3900 / (((suite-parallelism - 1) * 24) + 41))
// pool-size = max-concurrency * suite-parallelism

var identityPoolMapping = map[string]identityPool{
	"dev": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-dev",
		Location:              "westus3",
		SubscriptionIDHash:    "f5ead0cb5023266042158b287cc43d43e037bc009fb010d3c6efa596b9e18d47",
		Size:                  150,
	},
	"int": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-int",
		Location:              "uksouth",
		SubscriptionIDHash:    "25aa33440faa44e53b4e36694bf34b27d67104550f3263bec102c52fafe46191",
		Size:                  150,
	},
	"stg": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-stg",
		Location:              "uksouth",
		SubscriptionIDHash:    "1234567890",
		Size:                  150,
	},
	"prod": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-prod",
		Location:              "uksouth",
		SubscriptionIDHash:    "1234567890",
		Size:                  150,
	},
}
