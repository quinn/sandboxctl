package hostinstall

import (
	"debug/elf"
	"fmt"
)

func requireStaticallyLinked(path string) error {
	executable, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("inspect Firecracker executable %s: %w", path, err)
	}
	defer executable.Close()
	if hasELFInterpreter(executable.Progs) {
		return fmt.Errorf("Firecracker executable %s must be statically linked for the jailer", path)
	}
	return nil
}

func hasELFInterpreter(programs []*elf.Prog) bool {
	for _, program := range programs {
		if program.Type == elf.PT_INTERP {
			return true
		}
	}
	return false
}
