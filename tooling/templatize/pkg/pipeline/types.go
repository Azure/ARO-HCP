package pipeline

type Pipeline struct {
	pipelineFilePath string
	ServiceGroup     string           `yaml:"serviceGroup"`
	RolloutName      string           `yaml:"rolloutName"`
	ResourceGroups   []*resourceGroup `yaml:"resourceGroups"`
}

type resourceGroup struct {
	Name         string  `yaml:"name"`
	Subscription string  `yaml:"subscription"`
	AKSCluster   string  `yaml:"aksCluster"`
	Steps        []*step `yaml:"steps"`
}

type step struct {
	Name       string   `yaml:"name"`
	Action     string   `yaml:"action"`
	Command    []string `yaml:"command"`
	Env        []EnvVar `yaml:"env"`
	Template   string   `yaml:"template"`
	Parameters string   `yaml:"parameters"`
	DependsOn  []string `yaml:"dependsOn"`
}

type EnvVar struct {
	Name      string `yaml:"name"`
	ConfigRef string `yaml:"configRef"`
}
