package config

type configProviderImpl struct {
	baseVariableOverrides *VariableOverrides
	config                string
	region                string
	user                  string
}

type Variables map[string]string

type VariableOverrides struct {
	Defaults Variables `yaml:"defaults"`
	// key is the cloud alias
	Overrides map[string]*CloudVariableOverride `yaml:"clouds"`
}

type CloudVariableOverride struct {
	Defaults Variables `yaml:"defaults"`
	// key is the deploy env
	Overrides map[string]*DeployEnvVariableOverride `yaml:"environments"`
}

type DeployEnvVariableOverride struct {
	Defaults Variables `yaml:"defaults"`
	// key is the region name
	Overrides map[string]Variables `yaml:"regions"`
}
