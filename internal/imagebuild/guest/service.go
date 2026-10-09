package guest

import (
	"embed"
	"fmt"
)

//go:embed image-builder.sh
var serviceFiles embed.FS

// ImageBuilderService returns the fixed guest-side build and finalization script.
func ImageBuilderService() ([]byte, error) {
	content, err := serviceFiles.ReadFile("image-builder.sh")
	if err != nil {
		return nil, fmt.Errorf("read image builder service: %w", err)
	}
	return content, nil
}
