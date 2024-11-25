package pipeline

type Pipeline struct {
	pipelineFilePath string
	ServiceGroup     string           `yaml:"serviceGroup"`
	RolloutName      string           `yaml:"rolloutName"`
	ResourceGroups   []*ResourceGroup `yaml:"resourceGroups"`
}

type ResourceGroup struct {
	Name         string  `yaml:"name"`
	Subscription string  `yaml:"subscription"`
	AKSCluster   string  `yaml:"aksCluster,omitempty"`
	Steps        []*Step `yaml:"steps"`
}

type outPutHandler func(string)

type Step struct {
	Name       string   `yaml:"name"`
	Action     string   `yaml:"action"`
	Command    []string `yaml:"command,omitempty"`
	Env        []EnvVar `yaml:"env,omitempty"`
	Template   string   `yaml:"template,omitempty"`
	Parameters string   `yaml:"parameters,omitempty"`
	DependsOn  []string `yaml:"dependsOn,omitempty"`
	DryRun     DryRun   `yaml:"dryRun,omitempty"`
	outputFunc outPutHandler
}

type DryRun struct {
	EnvVars []EnvVar `yaml:"envVars,omitempty"`
	Command []string `yaml:"command,omitempty"`
}

type EnvVar struct {
	Name      string `yaml:"name"`
	ConfigRef string `yaml:"configRef,omitempty"`
	Value     string `yaml:"value,omitempty"`
}
