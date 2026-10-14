package packaging

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sandboxctl/internal/imagebuild/manifest"
)

const releaseTag = "v0.2.0"

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func requireTools(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("release fixture needs %s", name)
		}
	}
}

func releasePackage(t *testing.T, directory string, corrupt bool) string {
	t.Helper()
	requireTools(t, "tar", "zstd", "python3")
	image := manifest.Manifest{SchemaVersion: 1, Name: "builder", Architecture: "x86_64", Firecracker: "1.17.0", KernelVersion: "fixture", Guest: manifest.Guest{Distribution: "ubuntu", Version: "24.04"}, Provenance: "release fixture", KernelArgs: []string{"console=ttyS0", "rdinit=/guest-overlay-init"}, Artifacts: map[string]manifest.Artifact{}}
	files := map[string][]byte{".PKGINFO": []byte("pkgname = sandboxctl\npkgver = 0.2.0-1\narch = x86_64\n")}
	for _, name := range manifest.ArtifactNames() {
		data := []byte("fixture " + name)
		checksum := sha256.Sum256(data)
		image.Artifacts[name] = manifest.Artifact{Size: int64(len(data)), SHA256: hex.EncodeToString(checksum[:])}
		files["usr/share/sandboxctl/builder/"+name] = data
	}
	image.Version = manifest.VersionForManifest(image)
	if err := image.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(image)
	if err != nil {
		t.Fatal(err)
	}
	files["usr/share/sandboxctl/builder/manifest.json"] = data
	if corrupt {
		files["usr/share/sandboxctl/builder/root.squashfs"][0] = 'X'
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for name, content := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "sandboxctl-0.2.0-1-x86_64.pkg.tar.zst")
	command := exec.Command("zstd", "-q", "-o", path)
	command.Stdin = &archive
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compress package: %v\n%s", err, output)
	}
	return path
}

func artifactCommand(t *testing.T, operation, directory, tag string, extra ...string) ([]byte, error) {
	t.Helper()
	args := append([]string{filepath.Join(repositoryRoot(t), "scripts", "release-artifacts.py"), operation, directory, tag}, extra...)
	return exec.Command("python3", args...).CombinedOutput()
}

func TestReleaseArtifactValidation(t *testing.T) {
	for _, kind := range []string{"valid", "corrupt-builder", "tampered-package", "tampered-record", "symlink", "extra-package", "wrong-tag", "wrong-commit"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			packagePath := releasePackage(t, directory, kind == "corrupt-builder")
			commit := strings.Repeat("a", 40)
			if kind == "wrong-commit" {
				commit = "invalid"
			}
			output, err := artifactCommand(t, "create", directory, releaseTag, commit)
			if kind == "corrupt-builder" || kind == "wrong-commit" {
				if err == nil {
					t.Fatalf("accepted %s: %s", kind, output)
				}
				return
			}
			if err != nil {
				t.Fatalf("create: %v\n%s", err, output)
			}
			tag := releaseTag
			switch kind {
			case "tampered-package":
				writeFile(t, packagePath, "invalid", 0600)
			case "tampered-record":
				writeFile(t, filepath.Join(directory, "release.json"), "{}", 0600)
			case "symlink":
				path := filepath.Join(directory, "SHA256SUMS")
				if err := os.Rename(path, path+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".saved", path); err != nil {
					t.Fatal(err)
				}
			case "extra-package":
				writeFile(t, filepath.Join(directory, "extra.pkg.tar.zst"), "extra", 0600)
			case "wrong-tag":
				tag = "v0.3.0"
			}
			output, err = artifactCommand(t, "verify", directory, tag)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("verify %s: %v\n%s", kind, err, output)
			}
		})
	}
}

func TestReleasePackageProfile(t *testing.T) {
	recipe := filepath.Join(repositoryRoot(t), "packaging", "local", "PKGBUILD")
	for _, version := range []string{"0.2.0", "0.2", "01.2.0", "0.2.0;echo unsafe", "v0.2.0"} {
		t.Run(version, func(t *testing.T) {
			command := exec.Command("bash", "-euc", `source "$RECIPE"; printf '%s\n' "$pkgname" "$pkgver"; pkgver; printf '%s\n' "${conflicts[@]}"`)
			command.Env = append(os.Environ(), "RECIPE="+recipe, "SANDBOXCTL_RELEASE_VERSION="+version)
			output, err := command.CombinedOutput()
			if version == "0.2.0" {
				if err != nil || string(output) != "sandboxctl\n0.2.0\n0.2.0\nsandboxctl-local\nsandboxctl-git\n" {
					t.Fatalf("release profile: %v\n%s", err, output)
				}
			} else if err == nil {
				t.Fatalf("accepted invalid version: %s", output)
			}
		})
	}
}

func TestReleaseBuildScript(t *testing.T) {
	requireTools(t, "git", "makepkg", "python3", "tar", "zstd")
	if os.Geteuid() == 0 {
		t.Skip("makepkg release builds require an unprivileged user")
	}
	for _, kind := range []string{"valid", "invalid-tag", "dirty", "wrong-head", "existing-output", "check-fails", "check-modifies"} {
		t.Run(kind, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "release with spaces")
			repo := filepath.Join(base, "repo")
			bin := filepath.Join(base, "tools")
			outputDir := filepath.Join(base, "output")
			for _, name := range []string{"scripts/build-release.sh", "scripts/release-artifacts.py", "packaging/local/PKGBUILD"} {
				data, err := os.ReadFile(filepath.Join(repositoryRoot(t), name))
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(repo, name), string(data), 0700)
			}
			git := func(args ...string) string {
				t.Helper()
				command := exec.Command("git", append([]string{"-C", repo}, args...)...)
				command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("fixture git: %v\n%s", err, output)
				}
				return strings.TrimSpace(string(output))
			}
			git("init", "-q")
			git("add", ".")
			git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
			git("tag", releaseTag)
			packagePath := releasePackage(t, base, false)
			writeFile(t, filepath.Join(bin, "make"), `#!/bin/bash
set -eu
[[ $CHECK_FAIL != true ]]
if [[ $CHECK_MODIFY == true ]]; then
  printf 'changed\n' > "$CHECK_SOURCE/dirty"
fi
`, 0700)
			writeFile(t, filepath.Join(bin, "makepkg"), `#!/bin/bash
set -euo pipefail
[[ $SANDBOXCTL_RELEASE_VERSION == 0.2.0 ]]
[[ -d $SANDBOXCTL_PACKAGE_SOURCE/.git ]]
[[ $SOURCE_DATE_EPOCH =~ ^[0-9]+$ ]]
source PKGBUILD
[[ $pkgname == sandboxctl && $pkgver == 0.2.0 ]]
srcdir="$PWD/src"
mkdir "$srcdir"
prepare
cmp "$SANDBOXCTL_PACKAGE_SOURCE/scripts/build-release.sh" "$srcdir/sandboxctl/scripts/build-release.sh"
[[ $1 == --config ]]
source "$2"
cp "$FIXTURE_PACKAGE" "$PKGDEST/"
`, 0700)
			tag := releaseTag
			switch kind {
			case "invalid-tag":
				tag = "v01.2.0"
			case "dirty":
				writeFile(t, filepath.Join(repo, "dirty"), "changes", 0600)
			case "wrong-head":
				git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "later")
			case "existing-output":
				if err := os.Mkdir(outputDir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command("bash", filepath.Join(repo, "scripts", "build-release.sh"), tag, outputDir)
			command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "FIXTURE_PACKAGE="+packagePath, "CHECK_SOURCE="+repo, fmt.Sprintf("CHECK_FAIL=%t", kind == "check-fails"), fmt.Sprintf("CHECK_MODIFY=%t", kind == "check-modifies"))
			output, err := command.CombinedOutput()
			if (err == nil) != (kind == "valid") {
				t.Fatalf("release build %s: %v\n%s", kind, err, output)
			}
			if kind == "valid" {
				if output, err := artifactCommand(t, "verify", outputDir, releaseTag); err != nil {
					t.Fatalf("built files: %v\n%s", err, output)
				}
			} else if _, err := os.Stat(filepath.Join(outputDir, "release.json")); !os.IsNotExist(err) {
				t.Fatalf("failed build produced release metadata: %v", err)
			}
		})
	}
}

func TestReleasePublisher(t *testing.T) {
	for _, kind := range []string{"new", "annotated", "draft", "published", "wrong-commit", "tampered", "missing-tag"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			packagePath := releasePackage(t, directory, false)
			commit := strings.Repeat("a", 40)
			if output, err := artifactCommand(t, "create", directory, releaseTag, commit); err != nil {
				t.Fatalf("create: %v\n%s", err, output)
			}
			bin := filepath.Join(t.TempDir(), "tools")
			trace := filepath.Join(t.TempDir(), "gh-trace")
			writeFile(t, filepath.Join(bin, "gh"), `#!/bin/bash
set -eu
printf '%s\n' "$*" >> "$GH_TRACE"
case "$1 $2" in
 'api repos/example/sandboxctl/git/ref/tags/v0.2.0')
   case "$RELEASE_STATE" in
     missing-tag) exit 1 ;;
     annotated) printf 'tag\t%s\n' "$ANNOTATION" ;;
     *) printf 'commit\t%s\n' "$REMOTE_COMMIT" ;;
   esac ;;
 api*) [[ $2 == "repos/example/sandboxctl/git/tags/$ANNOTATION" ]]; printf 'commit\t%s\n' "$REMOTE_COMMIT" ;;
 'release view') case "$RELEASE_STATE" in new|annotated) exit 1 ;; draft) echo true ;; *) echo false ;; esac ;;
 'release create') [[ " $* " == *' --verify-tag '* && " $* " == *' --draft '* ]] ;;
 'release upload') [[ " $* " == *' --clobber '* ]] ;;
 *) exit 42 ;;
esac
`, 0700)
			if kind == "wrong-commit" {
				commit = strings.Repeat("b", 40)
			}
			if kind == "tampered" {
				writeFile(t, packagePath, "tampered", 0600)
			}
			command := exec.Command("bash", filepath.Join(repositoryRoot(t), "scripts", "publish-release.sh"), releaseTag, directory, "example/sandboxctl")
			command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "GH_TRACE="+trace, "REMOTE_COMMIT="+commit, "ANNOTATION="+strings.Repeat("c", 40), "RELEASE_STATE="+kind)
			output, err := command.CombinedOutput()
			valid := kind == "new" || kind == "draft" || kind == "annotated"
			if (err == nil) != valid {
				t.Fatalf("publish %s: %v\n%s", kind, err, output)
			}
			calls, _ := os.ReadFile(trace)
			if valid {
				operation := "release create"
				if kind == "draft" {
					operation = "release upload"
				}
				if !strings.Contains(string(calls), operation) {
					t.Fatalf("missing draft operation: %s", calls)
				}
			} else if strings.Contains(string(calls), "release create") || strings.Contains(string(calls), "release upload") || (kind == "tampered" && len(calls) > 0) {
				t.Fatalf("invalid files reached upload: %s", calls)
			}
		})
	}
}
