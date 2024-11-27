package ev2

import (
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func EV2Mapping(input config.Variables, prefix []string) (map[string]string, map[string]interface{}) {
	vars, _ := config.InterfaceToVariables(input)
	output := map[string]string{}
	replaced := map[string]interface{}{}
	for key, value := range vars {
		nestedKey := append(prefix, key)
		nested, ok := value.(config.Variables)
		if ok {
			flattened, replacement := EV2Mapping(nested, nestedKey)
			for index, what := range flattened {
				output[index] = what
			}
			replaced[key] = replacement
		} else {
			placeholder := fmt.Sprintf("__%s__", strings.Join(nestedKey, "_"))
			output[placeholder] = strings.Join(nestedKey, ".")
			replaced[key] = placeholder
		}
	}
	return output, replaced
}
