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
  sandboxctl image install NAME --version sha256:DIGEST
  sandboxctl create NAME [--cpus N] [--memory MiB] [--disk-size GiB] [--image NAME]
  sandboxctl start NAME [--dry-run]
  sandboxctl stop NAME [--force]
  sandboxctl list
  sandboxctl ssh NAME [COMMAND ...]
  sandboxctl paths
  sandboxctl ssh-config

Use sandboxctl ssh-config to print the block for your SSH configuration.
Connect with ssh sandbox-NAME after you add the block.

Image install reads a verified image bundle from the XDG data directory.
Create requires an installed image.
Start requires an installed image, a prepared TAP device, and KVM access.
Guest networking is not configured yet.
Read docs/CONTINUE.md before you use it with important data.`)
}
