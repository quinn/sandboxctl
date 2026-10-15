package packaging

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests use the real recipes and cache lookup with temporary artifacts.
// They do not download inputs, build a VM image, or install a host package.
func TestPackageRecipesReuseCachedBuilder(t *testing.T) {
	for _, profile := range []string{"local", "release"} {
		t.Run(profile, func(t *testing.T) {
			fixture := newFixture(t, "local")
			application, builder := "sandboxctl-local", "sandboxctl-builder-local"
			var env []string
			if profile == "release" {
				env = []string{"SANDBOXCTL_RELEASE_VERSION=0.2.0"}
				application, builder = "sandboxctl", "sandboxctl-builder"
			}
			output, err := fixture.run(env...)
			if err != nil {
				t.Fatalf("package recipe: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), "Builder image already staged at "+fixture.bundle) {
				t.Fatalf("builder cache was not reused:\n%s", output)
			}
			trace, err := os.ReadFile(fixture.trace)
			if err != nil {
				t.Fatal(err)
			}
			if string(trace) != "build\nstage-builder-image\n" {
				t.Fatalf("unexpected build operations: %s", trace)
			}
			for _, name := range []string{"manifest.json", "vmlinux", "initramfs.cpio.gz", "root.squashfs"} {
				source := filepath.Join(fixture.bundle, name)
				saved := filepath.Join(fixture.source, "package-data", "sandboxctl", "images", "builder", fixture.version, name)
				installed := filepath.Join(fixture.output, builder, "usr", "share", "sandboxctl", "builder", name)
				expected, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{saved, installed} {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != string(expected) {
						t.Fatalf("artifact %s: %v", path, err)
					}
				}
				info, err := os.Stat(source)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0600 {
					t.Fatalf("cache file mode changed: %o", info.Mode().Perm())
				}
			}
			for _, name := range []string{"sandboxctl", "sandboxd", "sandboxctl-forward"} {
				if _, err := os.Stat(filepath.Join(fixture.output, application, "usr", "bin", name)); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{application, builder} {
				if _, err := os.Stat(filepath.Join(fixture.output, name, "usr", "share", "licenses", name, "LICENSE")); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{filepath.Join(application, "usr/share/sandboxctl/builder"), filepath.Join(builder, "usr/bin")} {
				if _, err := os.Stat(filepath.Join(fixture.output, path)); !os.IsNotExist(err) {
					t.Fatalf("package payloads overlap at %s: %v", path, err)
				}
			}
		})
	}
}
func TestPackageRecipesDoNotReuseIncompleteBuilder(t *testing.T) {
	for _, recipe := range []string{"local"} {
		for _, kind := range []string{"missing", "empty", "symlink"} {
			t.Run(recipe+"/"+kind, func(t *testing.T) {
				fixture := newFixture(t, recipe)
				artifact := filepath.Join(fixture.bundle, "root.squashfs")
				switch kind {
				case "missing":
					if err := os.Remove(artifact); err != nil {
						t.Fatal(err)
					}
				case "empty":
					if err := os.Truncate(artifact, 0); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					target := filepath.Join(fixture.data, "rootfs-target")
					if err := os.Rename(artifact, target); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, artifact); err != nil {
						t.Fatal(err)
					}
				}
				output, err := fixture.run()
				if err == nil {
					t.Fatalf("accepted an incomplete cache:\n%s", output)
				}
				trace, err := os.ReadFile(fixture.trace)
				if err != nil {
					t.Fatal(err)
				}
				if string(trace) != "build\nstage-builder-image\ncache-miss\n" {
					t.Fatalf("did not attempt a fresh builder on cache miss: %s", trace)
				}
				if _, err := os.Stat(filepath.Join(fixture.output, "usr", "share", "sandboxctl", "builder")); !os.IsNotExist(err) {
					t.Fatalf("packaged incomplete builder: %v", err)
				}
			})
		}
	}
}

func TestPackageRecipesHaveValidShellSyntax(t *testing.T) {
	root := repositoryRoot(t)
	for _, recipe := range []string{"local"} {
		path := filepath.Join(root, "packaging", recipe, "PKGBUILD")
		if output, err := exec.Command("bash", "-n", path).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", recipe, err, output)
		}
	}
}

func TestSplitPackageSourceMetadata(t *testing.T) {
	requireTools(t, "makepkg")
	for _, profile := range []string{"local", "release"} {
		t.Run(profile, func(t *testing.T) {
			command := exec.Command("makepkg", "--printsrcinfo")
			command.Dir = filepath.Join(repositoryRoot(t), "packaging", "local")
			command.Env = append(os.Environ(), "SANDBOXCTL_RELEASE_VERSION=")
			application, builder := "sandboxctl-local", "sandboxctl-builder-local"
			if profile == "release" {
				command.Env = append(command.Env, "SANDBOXCTL_RELEASE_VERSION=0.2.0")
				application, builder = "sandboxctl", "sandboxctl-builder"
			}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("package metadata: %v\n%s", err, output)
			}
			sections := strings.Split(string(output), "\npkgname = ")
			if len(sections) != 3 || !strings.HasPrefix(sections[1], application+"\n") || !strings.HasPrefix(sections[2], builder+"\n") {
				t.Fatalf("expected exactly two split packages:\n%s", output)
			}
			if !strings.Contains(sections[1], "depends = "+builder+"=") || !strings.Contains(sections[2], "\tdepends = \n") {
				t.Fatalf("incorrect split-package dependencies:\n%s", output)
			}
		})
	}
}

func TestMakepkgCreatesVerifiedSplitArchives(t *testing.T) {
	requireTools(t, "makepkg", "fakeroot", "tar", "zstd", "python3")
	if os.Geteuid() == 0 {
		t.Skip("makepkg requires an unprivileged user")
	}
	fixture := newFixture(t, "local")
	archiveDir := t.TempDir()
	releasePackages(t, archiveDir, "valid")
	builderArchive := filepath.Join(archiveDir, "sandboxctl-builder-0.2.0-1-x86_64.pkg.tar.zst")
	for _, name := range []string{"manifest.json", "vmlinux", "initramfs.cpio.gz", "root.squashfs"} {
		output, err := exec.Command("tar", "--zstd", "-xOf", builderArchive, "usr/share/sandboxctl/builder/"+name).CombinedOutput()
		if err != nil {
			t.Fatalf("builder fixture: %v\n%s", err, output)
		}
		writeFile(t, filepath.Join(fixture.bundle, name), string(output), 0600)
	}
	recipe, err := os.ReadFile(fixture.recipe)
	if err != nil {
		t.Fatal(err)
	}
	// Use prepared fixtures instead of downloading source archives.
	start := filepath.Dir(fixture.source)
	writeFile(t, filepath.Join(start, "PKGBUILD"), string(recipe)+"\nsource=()\nsha256sums=()\n", 0600)
	config, err := os.ReadFile("/etc/makepkg.conf")
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(start, "makepkg.conf")
	releaseDir := t.TempDir()
	// Resolve the output path through the fixture environment.
	writeFile(t, configPath, string(config)+"\nPKGEXT='.pkg.tar.zst'\nPKGDEST=\"$SANDBOXCTL_PACKAGE_TEST_OUTPUT\"\n", 0600)
	command := exec.Command("makepkg", "--config", configPath, "--nodeps", "--noextract", "--nocheck")
	command.Dir = start
	command.Env = append(os.Environ(), "PATH="+fixture.bin+":"+os.Getenv("PATH"), "XDG_DATA_HOME="+fixture.data, "SANDBOXCTL_RELEASE_VERSION=0.2.0", "SANDBOXCTL_PACKAGE_TEST_OUTPUT="+releaseDir, "SANDBOXCTL_PACKAGE_TEST_TRACE="+fixture.trace)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("makepkg split build: %v\n%s", err, output)
	}
	if output, err := artifactCommand(t, "create", releaseDir, releaseTag, strings.Repeat("a", 40)); err != nil {
		t.Fatalf("real package verification: %v\n%s", err, output)
	}
	if output, err := artifactCommand(t, "verify", releaseDir, releaseTag); err != nil {
		t.Fatalf("real release verification: %v\n%s", err, output)
	}
}

type packageFixture struct{ recipe, source, output, bundle, version, trace, bin, data string }

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func newFixture(t *testing.T, recipe string) packageFixture {
	t.Helper()
	root := repositoryRoot(t)
	temporary := filepath.Join(t.TempDir(), "package build with spaces")
	fixture := packageFixture{recipe: filepath.Join(root, "packaging", recipe, "PKGBUILD"), source: filepath.Join(temporary, "src"), output: filepath.Join(temporary, "pkg"), version: "sha256:" + strings.Repeat("a", 64), trace: filepath.Join(temporary, "trace"), bin: filepath.Join(temporary, "tools"), data: filepath.Join(temporary, "data")}
	fixture.bundle = filepath.Join(fixture.data, "sandboxctl", "images", "builder", fixture.version)
	repo := filepath.Join(fixture.source, "sandboxctl")
	for _, path := range []string{fixture.bundle, fixture.bin, fixture.output, filepath.Join(repo, "bin"), filepath.Join(repo, "scripts"), filepath.Join(repo, "packaging", "systemd"), filepath.Join(repo, "third_party", "licenses"), filepath.Join(fixture.source, "release-v1.17.0-x86_64")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"vmlinux", "initramfs.cpio.gz", "root.squashfs"} {
		write(filepath.Join(fixture.bundle, name), "cached "+name, 0600)
	}
	write(filepath.Join(fixture.bundle, "manifest.json"), fmt.Sprintf(`{"schema_version":1,"name":"builder","version":"%s"}`, fixture.version), 0600)
	stage, err := os.ReadFile(filepath.Join(root, "scripts", "stage-builder-image.sh"))
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(repo, "scripts", "stage-builder-image.sh"), string(stage), 0700)
	// A cache hit must return before this downloader or any image build inputs.
	write(filepath.Join(repo, "scripts", "download-builder-base-assets.sh"), "#!/bin/sh\nprintf '%s\\n' cache-miss >> \"$SANDBOXCTL_PACKAGE_TEST_TRACE\"\nexit 42\n", 0700)
	write(filepath.Join(fixture.bin, "make"), `#!/bin/sh
set -eu
printf '%s\n' "$1" >> "$SANDBOXCTL_PACKAGE_TEST_TRACE"
case "$1" in
 build) exit 0 ;;
 stage-builder-image) exec ./scripts/stage-builder-image.sh ;;
 *) exit 1 ;;
esac
`, 0700)
	for _, name := range []string{"sandboxctl", "sandboxd", "sandboxctl-forward"} {
		write(filepath.Join(repo, "bin", name), "fixture executable", 0700)
	}
	for _, name := range []string{"sandboxd.service", "sandboxd.socket"} {
		write(filepath.Join(repo, "packaging", "systemd", name), "fixture unit", 0600)
	}
	write(filepath.Join(repo, "README.md"), "fixture documentation", 0600)
	write(filepath.Join(repo, "LICENSE"), "fixture license", 0600)
	write(filepath.Join(repo, "third_party", "licenses", "fixture.txt"), "fixture dependency license", 0600)
	for _, name := range []string{"firecracker", "jailer"} {
		write(filepath.Join(fixture.source, "release-v1.17.0-x86_64", name+"-v1.17.0-x86_64"), "fixture runtime", 0700)
	}
	return fixture
}
func (f packageFixture) run(env ...string) ([]byte, error) {
	script := `source "$SANDBOXCTL_PACKAGE_TEST_RECIPE"
srcdir="$SANDBOXCTL_PACKAGE_TEST_SOURCE"
pkgdir="$SANDBOXCTL_PACKAGE_TEST_OUTPUT"
build
packages=("${pkgname[@]}")
for name in "${packages[@]}"; do
  (
    pkgname=$name
    pkgdir="$SANDBOXCTL_PACKAGE_TEST_OUTPUT/$name"
    mkdir -p "$pkgdir"
    "package_$name"
  )
done
`
	command := exec.Command("bash", "-e", "-u", "-o", "pipefail", "-c", script)
	command.Env = append(os.Environ(), "PATH="+f.bin+":"+os.Getenv("PATH"), "XDG_DATA_HOME="+f.data, "IMAGE_NAME=builder", "SANDBOXCTL_PACKAGE_TEST_RECIPE="+f.recipe, "SANDBOXCTL_PACKAGE_TEST_SOURCE="+f.source, "SANDBOXCTL_PACKAGE_TEST_OUTPUT="+f.output, "SANDBOXCTL_PACKAGE_TEST_TRACE="+f.trace)
	command.Env = append(command.Env, env...)
	return command.CombinedOutput()
}
