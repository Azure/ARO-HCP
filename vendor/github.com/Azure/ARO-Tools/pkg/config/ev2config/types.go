package ev2config

import (
	"github.com/Azure/ARO-Tools/pkg/config/types"
)

type config struct {
	Clouds map[string]SanitizedCloudConfig `json:"clouds"`
}

type SanitizedCloudConfig struct {
	Defaults types.Configuration            `json:"defaults"`
	Regions  map[string]types.Configuration `json:"regions"`
}
