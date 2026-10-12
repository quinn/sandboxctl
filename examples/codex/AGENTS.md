# Base rules

- Read the repository instructions before you change code.
- Work autonomously until the task is complete or a blocker requires operator guidance.
- Use guest sudo when it is necessary to complete the task.
- Use Simplified Technical English for replies, comments, and documents.
- Keep credentials out of source files, logs, and reports.

# Git task

Each sandbox holds one task.
The working directory is `/home/dev/work/NAME`.
The Git branch and remote are both named `NAME`.
`NAME` is the sandbox name.
The remote points to a bare repository inside this guest.

- Work on the existing task branch.
- Preserve existing user changes.
- Keep temporary files outside the repository.
- Run the project checks that apply to your changes.
- Review `git diff` and `git status` before you commit.
- Stage only files that belong to the task.
- Commit the task changes with a clear commit message.
- Preserve partial work in commits when a blocker prevents completion.
- Finish with no uncommitted changes or untracked files in the working directory.
- Push the task branch without force.

Publish the commits from the working directory:

```sh
task_branch=$(git branch --show-current)
git push "$task_branch" "$task_branch"
```

The operator runs `sandboxctl pull NAME` on the host to retrieve the commits and report.
Pull also runs before deletion.
Uncommitted changes and untracked files prevent pull and deletion.

# Finish work

- Create `/REPORT.md` when you complete the task or need operator guidance.
- Write the report as a regular file with mode `0600`.
- Use guest sudo to create the report when needed.
- State whether the task is complete or blocked.
- Include the task branch and final commit identifier.
- Summarize the changes.
- List the checks and their results.
- Describe failed checks, incomplete work, and blockers.
- State the next steps when guidance is required.
- Keep the report smaller than 4 MiB.
