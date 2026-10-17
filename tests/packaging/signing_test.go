package packaging

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type releaseSigningFixture struct {
	root, secret, passphrase string
}

func newReleaseSigningFixture(t *testing.T) releaseSigningFixture {
	t.Helper()
	requireTools(t, "gpg", "gpgconf", "python3", "tar", "zstd")
	root := t.TempDir()
	home := filepath.Join(root, "keyring")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("gpgconf", "--homedir", home, "--kill", "gpg-agent").Run()
	})
	passphrase := "fixture signing passphrase"
	gpg := func(args ...string) []byte {
		t.Helper()
		command := exec.Command("gpg", append([]string{"--no-options", "--homedir", home, "--batch", "--pinentry-mode", "loopback", "--passphrase-fd", "0"}, args...)...)
		command.Stdin = strings.NewReader(passphrase + "\n")
		output, err := command.Output()
		if err != nil {
			t.Fatalf("fixture key operation: %v", err)
		}
		return output
	}
	gpg("--quick-generate-key", "sandboxctl signing fixture", "ed25519", "cert", "1d")
	fingerprint := ""
	for _, line := range strings.Split(string(gpg("--with-colons", "--list-secret-keys")), "\n") {
		if strings.HasPrefix(line, "fpr:") {
			fingerprint = strings.Split(line, ":")[9]
			break
		}
	}
	if len(fingerprint) != 40 {
		t.Fatal("fixture key has no fingerprint")
	}
	gpg("--quick-add-key", fingerprint, "ed25519", "sign", "1d")
	for _, name := range []string{"scripts/release-artifacts.py", "scripts/publish-release.sh"} {
		data, err := os.ReadFile(filepath.Join(repositoryRoot(t), name))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, name), string(data), 0700)
	}
	writeFile(t, filepath.Join(root, "packaging/keys/sandboxctl-release.asc"), string(gpg("--armor", "--export", fingerprint)), 0600)
	writeFile(t, filepath.Join(root, "packaging/keys/sandboxctl-release.fingerprint"), fingerprint+"\n", 0600)
	return releaseSigningFixture{root: root, secret: string(gpg("--armor", "--export-secret-subkeys", fingerprint)), passphrase: passphrase}
}

func (fixture releaseSigningFixture) command(operation, directory, tag string) *exec.Cmd {
	command := exec.Command("python3", filepath.Join(fixture.root, "scripts/release-artifacts.py"), operation, directory, tag)
	command.Env = append(os.Environ(), "RELEASE_SIGNING_KEY="+fixture.secret, "RELEASE_SIGNING_PASSPHRASE="+fixture.passphrase)
	return command
}

func (fixture releaseSigningFixture) sign(t *testing.T, directory string) {
	t.Helper()
	if output, err := fixture.command("sign", directory, releaseTag).CombinedOutput(); err != nil {
		t.Fatalf("sign fixture release: %v\n%s", err, output)
	}
}

func TestReleaseSigning(t *testing.T) {
	fixture := newReleaseSigningFixture(t)
	otherKey := newReleaseSigningFixture(t)
	for _, kind := range []string{"valid", "missing-secret", "wrong-secret", "wrong-passphrase", "existing-signature", "missing-signature", "corrupt-signature", "swapped-signature", "wrong-public-key", "symlink-signature", "extra-signature", "updated-checksums"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			releasePackages(t, directory, "valid")
			if output, err := artifactCommand(t, "create", directory, releaseTag, strings.Repeat("a", 40)); err != nil {
				t.Fatalf("create: %v\n%s", err, output)
			}
			application := filepath.Join(directory, "sandboxctl-x86_64.pkg.tar.zst.sig")
			builder := filepath.Join(directory, "sandboxctl-builder-x86_64.pkg.tar.zst.sig")
			public := filepath.Join(directory, "sandboxctl-release.asc")
			command := fixture.command("sign", directory, releaseTag)
			switch kind {
			case "missing-secret":
				command.Env = append(command.Env, "RELEASE_SIGNING_KEY=")
			case "wrong-secret":
				command.Env = append(command.Env, "RELEASE_SIGNING_KEY="+otherKey.secret)
			case "wrong-passphrase":
				command.Env = append(command.Env, "RELEASE_SIGNING_PASSPHRASE=incorrect")
			case "existing-signature":
				writeFile(t, application, "keep", 0600)
			}
			output, err := command.CombinedOutput()
			if kind == "missing-secret" || kind == "wrong-secret" || kind == "wrong-passphrase" || kind == "existing-signature" {
				if err == nil {
					t.Fatalf("accepted signing input %s: %s", kind, output)
				}
				if _, err := os.Stat(public); !os.IsNotExist(err) {
					t.Fatal("failed signing published release files")
				}
				return
			}
			if err != nil {
				t.Fatalf("sign %s: %v\n%s", kind, err, output)
			}
			switch kind {
			case "missing-signature":
				if err := os.Remove(builder); err != nil {
					t.Fatal(err)
				}
			case "corrupt-signature":
				writeFile(t, builder, "invalid signature", 0600)
			case "swapped-signature":
				data, err := os.ReadFile(builder)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, application, string(data), 0600)
			case "wrong-public-key":
				data, err := os.ReadFile(filepath.Join(otherKey.root, "packaging/keys/sandboxctl-release.asc"))
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, public, string(data), 0600)
			case "symlink-signature":
				if err := os.Rename(application, application+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(application+".saved", application); err != nil {
					t.Fatal(err)
				}
			case "extra-signature":
				writeFile(t, filepath.Join(directory, "extra.sig"), "unexpected", 0600)
			case "updated-checksums":
				for _, name := range []string{"sandboxctl-x86_64.pkg.tar.zst", "sandboxctl-builder-x86_64.pkg.tar.zst", "release.json", "SHA256SUMS"} {
					if err := os.Remove(filepath.Join(directory, name)); err != nil {
						t.Fatal(err)
					}
				}
				releasePackages(t, directory, "changed-runtime")
				if output, err := artifactCommand(t, "create", directory, releaseTag, strings.Repeat("b", 40)); err != nil {
					t.Fatalf("replace checksums: %v\n%s", err, output)
				}
			}
			output, err = fixture.command("verify-signed", directory, releaseTag).CombinedOutput()
			if (err == nil) != (kind == "valid") {
				t.Fatalf("verify-signed %s: %v\n%s", kind, err, output)
			}
		})
	}
}
