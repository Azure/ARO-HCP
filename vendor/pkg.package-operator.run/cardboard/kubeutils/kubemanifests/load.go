package kubemanifests

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// Loads kubernets objects from all .yaml files in the given folder.
// Does not recurse into subfolders.
// Preserves lexical file order.
func LoadKubernetesObjectsFromFolder(folderPath string) ([]unstructured.Unstructured, error) {
	folder, err := os.Open(folderPath)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", folderPath, err)
	}
	defer folder.Close()

	files, err := folder.Readdir(-1)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}
	sort.Sort(fileInfosByName(files))

	var objects []unstructured.Unstructured
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if path.Ext(file.Name()) != ".yaml" {
			continue
		}

		objs, err := LoadKubernetesObjectsFromFile(path.Join(folderPath, file.Name()))
		if err != nil {
			return nil, fmt.Errorf("loading kubernetes objects from file %q: %w", file, err)
		}
		objects = append(objects, objs...)
	}
	return objects, nil
}

// Loads kubernetes objects from the given file.
func LoadKubernetesObjectsFromFile(filePath string) ([]unstructured.Unstructured, error) {
	fileYaml, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}

	return LoadKubernetesObjectsFromBytes(fileYaml)
}

var splitYAMLDocumentsRegEx = regexp.MustCompile(`(?m)^---$`)

// Loads kubernetes objects from given bytes.
// A single file may contain multiple objects separated by "---\n".
func LoadKubernetesObjectsFromBytes(fileYaml []byte) ([]unstructured.Unstructured, error) {
	// Trim empty starting and ending objects
	fileYaml = bytes.Trim(fileYaml, "-\n")

	//nolint:prealloc // Can't prealloc here.
	var objects []unstructured.Unstructured
	// Split for every included yaml document.
	for i, yamlDocument := range splitYAMLDocumentsRegEx.Split(string(bytes.Trim(fileYaml, "---\n")), -1) {
		obj := unstructured.Unstructured{}
		if err := yaml.Unmarshal([]byte(yamlDocument), &obj); err != nil {
			return nil, fmt.Errorf(
				"unmarshalling yaml document at index %d: %w", i, err)
		}
		if len(obj.Object) == 0 {
			continue
		}
		objects = append(objects, obj)
	}

	return objects, nil
}

// Sorts fs.FileInfo objects by basename.
type fileInfosByName []fs.FileInfo

func (x fileInfosByName) Len() int { return len(x) }

func (x fileInfosByName) Less(i, j int) bool {
	iName := path.Base(x[i].Name())
	jName := path.Base(x[j].Name())
	return iName < jName
}

func (x fileInfosByName) Swap(i, j int) { x[i], x[j] = x[j], x[i] }

// LoadAndConvertIntoObject loads one Kubernetes object from a file into the out object.
// It uses the `Convert` method of `scheme` under the hood, so it does any conversion
// that method would do. LoadAndUnmarshalIntoObject provides similar functionality, without the
// conversion aspect. LoadAndUnmarshalIntoObject should only be used when there is no available
// scheme or when the user wants to explicitly block any conversions.
func LoadAndConvertIntoObject(scheme *runtime.Scheme, filePath string, out interface{}) error {
	objs, err := LoadKubernetesObjectsFromFile(filePath)
	if err != nil {
		return fmt.Errorf("loading object from file: %w", err)
	}
	if err := scheme.Convert(&objs[0], out, nil); err != nil {
		return fmt.Errorf("converting: %w", err)
	}
	return nil
}

// LoadAndUnmarshalIntoObject loads one Kubernetes object from a file into the out object.
// LoadAndUnmarshalIntoObject provides similar functionality, but uses `runtime.Scheme.Convert`
// under the hood. LoadAndUnmarshalIntoObject should only be used when there is no available
// scheme or when the user wants to explicitly block any conversions.
func LoadAndUnmarshalIntoObject(filePath string, out interface{}) error {
	obj, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	if err = yaml.Unmarshal(obj, &out); err != nil {
		return err
	}
	return nil
}
