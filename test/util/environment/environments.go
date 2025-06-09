package environment

type Environment string

const (
	Development Environment = "development"
	Integration Environment = "integration"
	Staging     Environment = "staging"
	Production  Environment = "production"
)

var environmentUrl = map[Environment]string{
	Development: "http://localhost:8443",
	Integration: "https://centraluseuap.management.azure.com",
	Staging:     "https://...",
	Production:  "https://...",
}

func (env Environment) Url() string {
	return environmentUrl[env]
}

func (env Environment) CompareUrl(url string) bool {
	return environmentUrl[env] == url
}
