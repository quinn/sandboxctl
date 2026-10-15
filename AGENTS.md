# Instructions for coding agents

Read these files before you change code:

1. `docs/SANDBOX-RUNTIME-V2.md`
2. `README.md`
3. `docs/SPEC.md`
4. `docs/DECISIONS.md`
5. `docs/CONTINUE.md`

`docs/SANDBOX-RUNTIME-V2.md` is authoritative. It replaces conflicting requirements in other documents and the current implementation.

## Text rules

Use ASD-STE100 principles for all user-facing text.

- Use short sentences.
- Use one instruction in each sentence.
- Use active voice.
- Use approved and consistent technical terms.
- Do not use unnecessary words.
- Define an abbreviation when you first use it.
- Keep command help, errors, comments, and documents consistent.
- Use generic examples and documented product defaults in project documents.
- Keep personal identities, local machine inventories, account status, private network details, and personal task names out of project documents.
- Preserve required upstream license notices and source attribution.
- Use `.yaml` for YAML files.

Do not change external command names, option names, file names, protocol names, or source identifiers.

## Work rules

- Support Linux hosts and Linux guests only.
- Keep the standard library as the default choice.
- Use gRPC over the Unix socket for sandboxctl-to-sandboxd communication.
- Define the protocol with Protocol Buffers and use Buf for linting and code generation.
- Pin Buf remote plugin versions. Do not edit generated code by hand.
- Carry the API version and request ID in gRPC metadata. Set and validate them in interceptors.
- Do not add a protocol negotiation RPC or metadata fields to Protocol Buffers messages.
- Keep privileged host state in root-owned system directories.
- Keep operator SSH keys and generated SSH configuration in XDG directories.
- Do not write to `~/.ssh`.
- Do not change the user's existing SSH configuration automatically.
- Generate one SSH fragment for the user to include with OpenSSH Include.
- Do not add direct Herdr integration or generate `ssh` or `scp` wrappers.
- Keep destructive commands explicit.
- Refuse to delete a running sandbox.
- Do not delete a source image.
- Treat guest images, SSH keys, and known-hosts data as private.
- Use mode `0600` for private files.
- Use mode `0700` for private directories.
- Add tests for each parser and state transition.
- Use the socket-activated `sandboxd` system service for privileged operations.
- Do not require `sudo` for routine sandbox operations.
- Do not run Firecracker as root or as the operator in the final design.
- Use `sudo` only for an isolated development proof when needed.
- Keep the build green after each implementation step.

## Progress tracking

Keep `docs/PROGRESS.md` current.
Record the active phase, completed work, checks, blockers, decisions, and next task.
Update it when a phase or decision changes.

Complete the risk-first Phase 1 storage proof before you start the daemon, installer, or other control-plane phases.
The Phase 1 proof may use a temporary direct Firecracker runner.
Do not treat that runner as production architecture.

## Required checks

Run these commands before you finish a code change:

```sh
gofmt -w $(find . -name '*.go' -type f)
go vet ./...
go test ./...
```

If a check cannot run, state the reason in the handoff.

## Current task

Continue the implementation sequence in section 18 of `docs/SANDBOX-RUNTIME-V2.md`.
The Phase 1 SquashFS and OverlayFS proof has passed.
The active task is section 18, step 6: add UID, GID, address, and TAP allocation registries.
