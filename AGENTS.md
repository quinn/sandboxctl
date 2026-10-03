# Instructions for coding agents

Read these files before you change code:

1. `README.md`
2. `docs/SPEC.md`
3. `docs/DECISIONS.md`
4. `docs/CONTINUE.md`

## Text rules

Use ASD-STE100 principles for all user-facing text.

- Use short sentences.
- Use one instruction in each sentence.
- Use active voice.
- Use approved and consistent technical terms.
- Do not use unnecessary words.
- Define an abbreviation when you first use it.
- Keep command help, errors, comments, and documents consistent.

Do not change external command names, option names, file names, protocol names, or source identifiers.

## Work rules

- Support Linux hosts and Linux guests only.
- Keep the standard library as the default choice.
- Keep host state in the XDG base directories.
- Do not write to `~/.ssh`.
- Do not change the user's existing SSH configuration.
- Do not change the user's Herdr configuration.
- Manage the tool-owned SSH configuration.
- Let the user install the include in the main SSH configuration.
- Keep application-specific SSH management outside the active scope.
- Keep destructive commands explicit.
- Refuse to delete a running sandbox.
- Do not delete a source image.
- Treat guest images, SSH keys, and known-hosts data as private.
- Use `0600` for private files.
- Use `0700` for private directories.
- Add tests for each parser and state transition.
- Do not require systemd for normal sandbox use.

## Required checks

Run these commands before you finish a change:

```sh
gofmt -w $(find . -name '*.go' -type f)
go vet ./...
go test ./...
```

If a check cannot run, state the reason in the handoff.

## Current first task

Implement Milestone 1 in `docs/CONTINUE.md`. Do not start Milestone 2 until all Milestone 1 tests pass.

