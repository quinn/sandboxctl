# Deferred Herdr wrapper design

This design is outside the active scope.
Do not implement it unless the user requests it.

A deferred design proposed tool-owned `ssh` and `scp` wrappers first in `PATH` when starting Herdr.
Each wrapper removed `-F FILE` and `-FFILE` options supplied by Herdr.
It then started `/usr/bin/ssh` or `/usr/bin/scp` with `-F TOOL_CONFIG`.
It forwarded the remaining arguments without a shell.
It preserved the environment and exit status.
Malformed `-F` arguments required an error.

The wrappers lived in `$XDG_DATA_HOME/sandboxctl/bin`.
The deferred interface used `sandboxctl herdr run` and `sandboxctl herdr register NAME`.
Registration ran `herdr machine add TARGET --label LABEL`.

This approach required release-specific tests of Herdr's subprocess behavior.
It also required tests for argument filtering, argument preservation, and wrapper environment construction.
The prototype did not prove compatibility with the installed release.

The active design uses a tool-owned SSH configuration and a manually installed include.
