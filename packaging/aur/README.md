# Arch package draft

`PKGBUILD` builds `sandboxctl`, its `sandboxctl-*` command executables, and `sandboxd` from the Git repository. It installs these executables and the systemd unit files. It depends on Arch's `firecracker` package, which provides both Firecracker and jailer.

The package does not enable the socket or change host configuration. Run `sudo sandboxctl host install --operator USER` after you install the package. That command applies the host-specific setup and enables the socket.

This draft uses the GitHub URL inferred from the workspace path. Confirm that the repository is public and that this URL is correct before you submit the package to the Arch User Repository. The project also needs a declared software license before publication.

Build the package as an unprivileged user. Review the package build script before you use it. Arch builds packages from `PKGBUILD` files with `makepkg` and installs the resulting package with pacman. See the [Arch package creation guide](https://wiki.archlinux.org/title/Creating_packages) and [AUR guide](https://wiki.archlinux.org/title/Arch_User_Repository).
