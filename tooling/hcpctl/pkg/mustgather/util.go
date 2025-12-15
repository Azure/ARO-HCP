// FileWriter provides an interface for writing files to support testing
package mustgather

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
)

type FileWriter interface {
	WriteFile(outputPath, fileName string, data any) error
}

// JsonEncoderWriter implements FileWriter using the standard file system
type JsonEncoderWriter struct{}

func (d *JsonEncoderWriter) WriteFile(outputPath, fileName string, data any) error {
	file, err := os.Create(path.Join(outputPath, fileName))
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(data)
}
