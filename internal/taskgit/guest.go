package taskgit

import "fmt"

func guestVariables(record *Record) string {
	return "set -eu\numask 077\n" +
		"name=" + quote(record.Name) + "\nid=" + quote(record.InstanceID) + "\n" +
		"bare=" + quote("/home/dev/repos/"+record.Name+".git") + "\n" +
		"work=" + quote("/home/dev/work/"+record.Name) + "\n" +
		"start=" + quote(record.StartCommit) + "\n" +
		"git_task() { git -c core.hooksPath=/dev/null \"$@\"; }\n" +
		"test -r /var/lib/sandboxctl/bootstrap-complete\n" +
		"test ! -L /home/dev/repos\ntest ! -L /home/dev/work\n" +
		"test ! -L \"$bare\"\ntest ! -L \"$work\"\n"
}

func prepareGuest(record *Record) string {
	return guestVariables(record) + `
mkdir -p /home/dev/repos /home/dev/work
chmod 0700 /home/dev/repos /home/dev/work
if test -e "$bare"; then
    test "$(git_task --git-dir="$bare" rev-parse --is-bare-repository)" = true
    test "$(git_task --git-dir="$bare" config --get sandboxctl.id)" = "$id"
else
    git_task init --quiet --bare --template= --initial-branch="$name" "$bare"
    git_task --git-dir="$bare" config sandboxctl.id "$id"
fi
if test -e "$work"; then
    test ! -L "$work/.git"
    test "$(git_task -C "$work" config --local --get sandboxctl.id)" = "$id"
    test "$(git_task -C "$work" symbolic-ref HEAD)" = "refs/heads/$name"
    printf 'existing\n'
elif git_task --git-dir="$bare" show-ref --verify --quiet "refs/heads/$name"; then
    printf 'clone\n'
else
    printf 'seed\n'
fi`
}

func finishGuest(record *Record) string {
	return guestVariables(record) +
		"author_name=" + quote(record.AuthorName) + "\nauthor_email=" + quote(record.AuthorEmail) + "\n" + `
test "$(git_task --git-dir="$bare" config --get sandboxctl.id)" = "$id"
git_task --git-dir="$bare" merge-base --is-ancestor "$start" "refs/heads/$name"
if test ! -e "$work"; then
    temporary=$(mktemp -d "/home/dev/work/.$name.XXXXXX")
    trap 'rm -rf -- "$temporary"' EXIT HUP INT TERM
    git_task clone --quiet --no-hardlinks --template= --origin "$name" --branch "$name" "$bare" "$temporary"
    git_task -C "$temporary" config sandboxctl.id "$id"
    git_task -C "$temporary" config user.name "$author_name"
    git_task -C "$temporary" config user.email "$author_email"
    mv -T -- "$temporary" "$work"
    trap - EXIT HUP INT TERM
fi
test ! -L "$work/.git"
test "$(git_task -C "$work" config --local --get sandboxctl.id)" = "$id"
test "$(git_task -C "$work" symbolic-ref HEAD)" = "refs/heads/$name"
test "$(git_task -C "$work" remote get-url "$name")" = "$bare"`
}

func publishGuest(record *Record) string {
	return guestVariables(record) + `
test ! -L "$work/.git"
test "$(git_task --git-dir="$bare" config --get sandboxctl.id)" = "$id"
test "$(git_task -C "$work" config --local --get sandboxctl.id)" = "$id"
test "$(git_task -C "$work" symbolic-ref HEAD)" = "refs/heads/$name"
test "$(git_task -C "$work" remote get-url "$name")" = "$bare"
test -z "$(git_task -C "$work" status --porcelain=v1 --untracked-files=all --ignore-submodules=none)"
git_task -C "$work" merge-base --is-ancestor "$start" HEAD
git_task -C "$work" push --quiet --no-verify "$name" "HEAD:refs/heads/$name"`
}

var readReport = fmt.Sprintf(`set -eu
if test -e /REPORT.md || test -L /REPORT.md; then
    test ! -L /REPORT.md
    test -f /REPORT.md
    test "$(stat -c %%s /REPORT.md)" -le %d
    printf '1'
    if test -r /REPORT.md; then
        cat /REPORT.md
    else
        sudo -n cat /REPORT.md
    fi
else
    printf '0'
fi`, maxOutput-1)
