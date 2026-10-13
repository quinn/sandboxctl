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
  sandboxctl image list
  sandboxctl image install NAME --version sha256:DIGEST
  sandboxctl image delete NAME [--yes]
  sandboxctl image build NAME --base-image IMAGE@sha256:DIGEST --guest-version VERSION [--install=false] [--builder-image NAME --builder-version sha256:DIGEST] [--distribution debian|ubuntu]
  sandboxctl create NAME [--cpus N] [--memory MiB] [--disk-size GiB] [--image NAME]
  sandboxctl start NAME [--dry-run | --copy-codex-auth]
  sandboxctl stop NAME [--force]
  sandboxctl forward NAME [HOST_PORT:]GUEST_PORT [--replace-host-key]
  sandboxctl forward NAME --list
  sandboxctl forward NAME --remove GUEST_PORT
  sandboxctl pull NAME
  sandboxctl reset NAME
  sandboxctl delete NAME [--yes]
  sandboxctl list
  sandboxctl ssh NAME [COMMAND ...]
  sandboxctl paths
  sandboxctl ssh-config

Use sandboxctl ssh-config to print the block for your SSH configuration.
Connect with ssh sandbox-NAME after you add the block.

Image install reads a verified image bundle from the XDG data directory.
Image list shows installed image names and versions.
Image delete removes an installed image that no saved sandbox uses.
Image build runs a fixed build service in a temporary builder VM, stages a verified bundle, and installs it by default. Set --install=false to stage it only.
Create requires an installed image.
Start requires an installed image, a prepared TAP device, and KVM access.
Start with --copy-codex-auth replaces the guest Codex login file with the host file.
Create and start attach a Git repository from the current directory.
The sandbox name identifies the Git branch and remote.
Forward saves a background TCP mapping on the host loopback address.
Forward selects a free host port if the preferred port is in use.
Pull retrieves committed task results and the guest report.
Pull temporarily starts a stopped sandbox and stops it after the transfer.
Delete removes a stopped sandbox and its writable state disk.
Delete pulls attached Git task results before it removes any selected sandbox.
Reset replaces only a stopped sandbox's writable state disk.
Read docs/CONTINUE.md before you use it with important data.`)
}
