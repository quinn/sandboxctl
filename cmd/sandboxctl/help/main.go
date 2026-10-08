package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"sandboxctl/internal/cmdutil"
)

func main() {
	cmdutil.Main(run)
}

func run(arguments []string) error {
	if len(arguments) != 0 {
		return errors.New("usage: sandboxctl help")
	}
	write(os.Stdout)
	return nil
}

func write(writer io.Writer) {
	fmt.Fprintln(writer, `sandboxctl manages local Firecracker development sandboxes.

Usage:
  sandboxctl host install --operator USER
  sandboxctl doctor
  sandboxctl init
  sandboxctl create NAME --kernel PATH --root-drive PATH
  sandboxctl start NAME [--dry-run]
  sandboxctl stop NAME
  sandboxctl list
  sandboxctl ssh NAME [COMMAND ...]
  sandboxctl paths
  sandboxctl ssh-config

Use sandboxctl ssh-config to print the block for your SSH configuration.
Connect with ssh sandboxctl-NAME after you add the block.

This implementation does not yet create the TAP network or prepare the guest image.
Read docs/CONTINUE.md before you use it with important data.`)
}
