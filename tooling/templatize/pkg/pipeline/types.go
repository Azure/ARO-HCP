package pipeline

type Pipeline struct {
	pipelineFilePath string
	RolloutName      string  `yaml:"rolloutName"`
	Steps            []*step `yaml:"steps"`
}

type step struct {
	Name           string `yaml:"name"`
	Subscription   string `yaml:"subscription"`
	ResourceGroup  string `yaml:"resourceGroup"`
	AKSClusterName string `yaml:"aksCluster"`
	Action         action `yaml:"action"`
}

type action struct {
	Type    string   `yaml:"type"`
	Command []string `yaml:"command"`
	Env     []EnvVar `yaml:"env"`
}

type EnvVar struct {
	Name      string `yaml:"name"`
	ConfigRef string `yaml:"configRef"`
}
