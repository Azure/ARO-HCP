package utils

import (
	"fmt"
)

// AnyToString maps some types to strings, as they are used in OS Env.
func AnyToString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case int:
		return fmt.Sprintf("%d", v)
	case bool:
		return fmt.Sprintf("%t", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
