# End-to-end tests

Run the complete end-to-end (E2E) suite with:

```sh
make e2e
```

Install the Arch package before you run this suite. Configure the host with
`sandboxctl host install` and give the operator access to the
`sandboxctl` group. Start a new login session after you add the operator to
that group. The suite does not install packages or run `sudo`.

The suite uses the installed `sandboxctl` command. It refuses to run when
`PATH` selects the workspace binary. It runs `sandboxctl init` and
`sandboxctl doctor` before the tests. The suite then checks guest bootstrap,
the builder image, base-image builds, Dockerfile builds, rejected Dockerfile
builds, stopped-sandbox deletion, image listing and deletion, and state-disk
reset.

The suite needs an Arch Linux x86-64 host, working access to `/dev/kvm`, a
running `sandboxd` service, Go, Podman, Make, Python, OpenSSH client, and
SquashFS tools. It needs network access to download pinned image assets and
package data. The image-build tests also need access to Ubuntu's container
registry and package servers.

The suite stores its private data and logs under `tmp/e2e/`. It ignores XDG
directory settings from the caller. It removes test sandboxes and images after
successful checks. If a check fails while a sandbox is running, the suite
leaves that sandbox available for diagnosis. Use the name and console log path
printed by the failed step to inspect it. Stop and delete the sandbox after
diagnosis.

You can run one step with its Make target, such as `make manual-bootstrap-test`
or `make manual-dockerfile-build`. A step uses `SANDBOXCTL_BIN` when you set
it. Otherwise, it uses `bin/sandboxctl` when that file exists, then falls back
to `sandboxctl` from `PATH`.

The shell scripts and Make targets for this suite live in `e2e/`. The suite
does not need a separate Go command. Put a future Go helper used only by this
suite under `cmd/e2e/`.
