package main

import (
	"errors"
	"fmt"
	"os"

	"sandboxctl/internal/cmdutil"
)

func main() {
	cmdutil.Main(run)
}

func run(arguments []string) error {
	if len(arguments) != 0 {
		return errors.New("usage: sandboxctl version")
	}
	fmt.Fprintln(os.Stdout, "0.1.0-dev")
	return nil
}
