package identitypool

type identityPool struct {
	Size                  int
	ResourceGroupBaseName string
	Location              string
	SubscriptionIDHash    string
}

var identityPoolMapping = map[string]identityPool{
	"dev": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-dev",
		Location:              "westus3",
		SubscriptionIDHash:    "f5ead0cb5023266042158b287cc43d43e037bc009fb010d3c6efa596b9e18d47",
		Size:                  140,
	},
	"int": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-int",
		Location:              "uksouth",
		SubscriptionIDHash:    "25aa33440faa44e53b4e36694bf34b27d67104550f3263bec102c52fafe46191",
		Size:                  140,
	},
	"stg": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-stg",
		Location:              "uksouth",
		SubscriptionIDHash:    "1234567890",
		Size:                  140,
	},
	"prod": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-prod",
		Location:              "uksouth",
		SubscriptionIDHash:    "1234567890",
		Size:                  140,
	},
}
