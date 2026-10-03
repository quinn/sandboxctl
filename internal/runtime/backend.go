package runtime

import (
	"os"

	"sandboxctl/internal/model"
)

// Backend isolates the SDK process lifecycle from the application layer.
type Backend interface {
	Start(instance model.Instance, executable string) (int, error)
	Run(instance model.Instance, ready *os.File) error
}
