package v20240610preview

import (
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

// InternalToExternalValidationPathMapping takes an internal path from validation and converts it to the external path
// for this particular version.  This needs to be as close as possible, but perfection isn't required since fields
// could be split or combined.
func InternalToExternalValidationPathMapping(internalPath *field.Path) *field.Path {
	// there isn't a great field path access that gives us steps (as far as I know), so we'll parse out what we have.
	internalPathString := internalPath.String()
	if strings.Contains(internalPathString, "customerProperties") {
	}

	externalPath := field.NewPath("")

	paths := strings.Split(internalPath.String(), ".")
	for _, path := range paths {
		if strings.Contains(path, "[") && strings.Contains(path, "]") {
			parts := strings.SplitN(path, "[", 2)
			step := parts[0]
			externalPath = externalPath.Child(internalToExternalPathName(step))

			indexesString := "[" + parts[1]                       // remember there can be more than one
			tokenizedIndexes := strings.Split(indexesString, "[") // remember these should have trailing "]"
			for _, tokenizedIndex := range tokenizedIndexes {
				currIndexValue := tokenizedIndex[:(len(tokenizedIndex) - 2)]
				if arrayIndex, err := strconv.Atoi(currIndexValue); err == nil {
					externalPath = externalPath.Index(arrayIndex)
				} else {
					externalPath = externalPath.Key(currIndexValue)
				}
			}
			continue
		}
		externalPath = externalPath.Child(internalToExternalPathName(path))
	}

	return externalPath
}

func internalToExternalPathName(internalStep string) string {
	switch internalStep {
	case "customerProperties":
		return "properties"
	case "serviceProviderProperties":
		return "properties"
	default:
		return internalStep
	}
}
