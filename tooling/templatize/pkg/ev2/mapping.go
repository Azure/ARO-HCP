package ev2

import (
	"fmt"
	"strings"
)

func EV2Mapping(input map[string]interface{}, prefix []string) (map[string]string, map[string]interface{}) {
	output := map[string]string{}
	replaced := map[string]interface{}{}
	for key, value := range input {
		nestedKey := append(prefix, key)
		nested, ok := value.(map[string]interface{})
		if ok {
			flattened, replacement := EV2Mapping(nested, nestedKey)
			for index, what := range flattened {
				output[index] = what
			}
			replaced[key] = replacement
		} else {
			placeholder := fmt.Sprintf("__%s__", strings.ToUpper(strings.Join(nestedKey, "_")))
			output[placeholder] = strings.Join(nestedKey, ".")
			replaced[key] = placeholder
		}
	}
	return output, replaced
}
