package customize

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func LoadScaffoldTemplates(scaffoldDir string) ([]unstructured.Unstructured, error) {
	var manifests []unstructured.Unstructured
	err := filepath.Walk(scaffoldDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml") {
			fileContent, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			mapContent := make(map[string]interface{})
			err = yaml.Unmarshal(fileContent, &mapContent)
			if err != nil {
				return err
			}

			manifests = append(manifests, convertMapToUnstructured(mapContent))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return manifests, nil
}
