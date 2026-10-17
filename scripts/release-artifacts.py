#!/usr/bin/env python3
"""Create and verify the files uploaded with a sandboxctl GitHub release."""

import hashlib
import json
import os
import pathlib
import re
import stat
import struct
import subprocess
import sys
import tempfile
from contextlib import contextmanager

TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")
COMMIT = re.compile(r"(?:[0-9a-f]{40}|[0-9a-f]{64})\Z")
DIGEST = re.compile(r"[0-9a-f]{64}\Z")
ARTIFACTS = ("vmlinux", "initramfs.cpio.gz", "root.squashfs")
PACKAGES = ("sandboxctl", "sandboxctl-builder")
KEY_NAME = "sandboxctl-release.asc"
KEY_DIRECTORY = pathlib.Path(__file__).resolve().parent.parent / "packaging/keys"
RUNTIME_FILES = (
    "usr/bin/sandboxctl", "usr/bin/sandboxd",
    "usr/lib/sandboxctl/firecracker", "usr/lib/sandboxctl/jailer",
    "usr/lib/systemd/system/sandboxd.socket", "usr/lib/systemd/system/sandboxd.service",
)


def regular(path):
    if not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError(f"Release file must be a regular file: {path.name}")


def digest(path):
    regular(path)
    value = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            value.update(chunk)
    return value.hexdigest()


def member(package, name, limit=1024 * 1024):
    result = subprocess.run(
        ["tar", "--zstd", "-xOf", str(package), name],
        check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    ).stdout
    if len(result) > limit:
        raise ValueError(f"Package member is too large: {name}")
    return result


def package_info(package, tag, name):
    info = {}
    dependencies = []
    for line in member(package, ".PKGINFO").decode().splitlines():
        if " = " in line:
            key, value = line.split(" = ", 1)
            if key in ("pkgname", "pkgver", "arch"):
                if key in info:
                    raise ValueError(f"Duplicate package field: {key}")
                info[key] = value
            elif key == "depend":
                dependencies.append(value)
    if info != {"pkgname": name, "pkgver": tag[1:] + "-1", "arch": "x86_64"}:
        raise ValueError("Package name, version, or architecture does not match the release")
    names = set(subprocess.run(
        ["tar", "--zstd", "-tf", str(package)], check=True,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    ).stdout.decode().splitlines())
    if name == "sandboxctl":
        if "sandboxctl-builder=" + tag[1:] + "-1" not in dependencies:
            raise ValueError("Application package must require the matching builder package")
        if not set(RUNTIME_FILES).issubset(names):
            raise ValueError("Application package is missing runtime files")
        if any(path.startswith("usr/share/sandboxctl/builder/") for path in names):
            raise ValueError("Application package must not contain the builder bundle")
    elif dependencies or any(path.startswith(("usr/bin/", "usr/lib/sandboxctl/", "usr/lib/systemd/system/")) for path in names):
        raise ValueError("Builder package must not contain runtime files or dependencies")


def builder_info(package):
    image = json.loads(member(package, "usr/share/sandboxctl/builder/manifest.json"))
    if not isinstance(image, dict):
        raise ValueError("Packaged builder manifest must be an object")
    if image.get("schema_version") != 1 or image.get("name") != "builder" or image.get("architecture") != "x86_64":
        raise ValueError("Packaged builder identity is invalid")
    artifacts = image.get("artifacts", {})
    if not isinstance(artifacts, dict) or set(artifacts) != set(ARTIFACTS):
        raise ValueError("Packaged builder must describe all three boot artifacts")
    guest = image.get("guest", {})
    fields = (image.get("firecracker_compatibility"), image.get("kernel_version"),
              guest.get("distribution") if isinstance(guest, dict) else None,
              guest.get("version") if isinstance(guest, dict) else None,
              image.get("provenance"))
    if any(not isinstance(field, str) or not field for field in fields):
        raise ValueError("Packaged builder platform fields are required")
    arguments = image.get("kernel_args")
    if not isinstance(arguments, list) or any(not isinstance(arg, str) or not arg or len(arg) > 4096 for arg in arguments):
        raise ValueError("Packaged builder kernel arguments are invalid")
    if not {"console=ttyS0", "rdinit=/guest-overlay-init"}.issubset(arguments):
        raise ValueError("Packaged builder kernel arguments are incomplete")
    total = 0
    for name in ARTIFACTS:
        expected = artifacts[name]
        if not isinstance(expected, dict):
            raise ValueError(f"Invalid builder artifact metadata: {name}")
        size, checksum = expected.get("size"), expected.get("sha256", "")
        if type(size) is not int or size < 1 or size > 32 << 30 or not DIGEST.fullmatch(checksum):
            raise ValueError(f"Invalid builder artifact metadata: {name}")
        total += size
        if total > 32 << 30:
            raise ValueError("Packaged builder exceeds the combined size limit")
        process = subprocess.Popen(
            ["tar", "--zstd", "-xOf", str(package), "usr/share/sandboxctl/builder/" + name],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        )
        value, count = hashlib.sha256(), 0
        try:
            while chunk := process.stdout.read(1024 * 1024):
                count += len(chunk)
                if count > size:
                    raise ValueError(f"Packaged builder artifact exceeds its recorded size: {name}")
                value.update(chunk)
            if process.wait() != 0 or count != size or value.hexdigest() != checksum:
                raise ValueError(f"Packaged builder artifact does not match its manifest: {name}")
        finally:
            process.stdout.close()
            if process.poll() is None:
                process.kill()
                process.wait()
    # Use the same manifest-v1 field order as the guest image staging script.
    value = hashlib.sha256()
    def write(text):
        encoded = str(text).encode()
        value.update(struct.pack(">Q", len(encoded)))
        value.update(encoded)
    write("sandboxctl-image-manifest-v1")
    for item in (image["schema_version"], image["name"], image["architecture"], image["firecracker_compatibility"], image["kernel_version"], image["guest"]["distribution"], image["guest"]["version"], image["provenance"]):
        write(item)
    write(len(image["kernel_args"]))
    for argument in image["kernel_args"]:
        write(argument)
    for name in ARTIFACTS:
        write(name)
        write(artifacts[name]["size"])
        write(artifacts[name]["sha256"])
    if image.get("version") != "sha256:" + value.hexdigest():
        raise ValueError("Packaged builder version does not match its manifest")
    return {"name": image["name"], "version": image["version"]}


def expected_packages(directory, tag):
    expected = [directory / f"{name}-x86_64.pkg.tar.zst" for name in PACKAGES]
    if set(directory.glob("*.pkg.tar.zst")) != set(expected):
        raise ValueError("Release directory must contain exactly the matching application and builder packages")
    for name, path in zip(PACKAGES, expected):
        regular(path)
        package_info(path, tag, name)
    return expected


def package_records(packages):
    return [{"name": name, "file": path.name, "sha256": digest(path)}
            for name, path in zip(PACKAGES, packages)]


def create(directory, tag, commit):
    if not COMMIT.fullmatch(commit):
        raise ValueError("Invalid release source commit")
    packages = expected_packages(directory, tag)
    image = builder_info(packages[1])
    data = {"schema_version": 2, "tag": tag, "version": tag[1:], "source_commit": commit,
            "packages": package_records(packages), "builder": image}
    record = directory / "release.json"
    with record.open("x") as output:
        json.dump(data, output, indent=2)
        output.write("\n")
    with (directory / "SHA256SUMS").open("x") as output:
        for path in (*packages, record):
            output.write(f"{digest(path)}  {path.name}\n")


def verify(directory, tag):
    packages = expected_packages(directory, tag)
    record = directory / "release.json"
    regular(record)
    regular(directory / "SHA256SUMS")
    data = json.loads(record.read_text())
    if not isinstance(data, dict):
        raise ValueError("Release metadata must be an object")
    if data.get("schema_version") != 2 or data.get("tag") != tag or data.get("version") != tag[1:] or not COMMIT.fullmatch(data.get("source_commit", "")):
        raise ValueError("Release metadata does not match the requested tag")
    expected = "".join(f"{digest(path)}  {path.name}\n" for path in (*packages, record))
    if (directory / "SHA256SUMS").read_text() != expected:
        raise ValueError("Release files do not match SHA256SUMS")
    if data.get("packages") != package_records(packages):
        raise ValueError("Release package checksums or filenames are invalid")
    if data.get("builder") != builder_info(packages[1]):
        raise ValueError("Release builder metadata differs from the package")


def small_file(path):
    regular(path)
    if path.stat().st_size > 1024 * 1024:
        raise ValueError(f"Release signing file is too large: {path.name}")
    return path.read_bytes()


@contextmanager
def keyring():
    with tempfile.TemporaryDirectory(prefix="sandboxctl-signing-") as name:
        home = pathlib.Path(name)
        try:
            yield home
        finally:
            subprocess.run(["gpgconf", "--homedir", name, "--kill", "gpg-agent"],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)


def gpg(home, *arguments, input=None):
    return subprocess.run(
        ["gpg", "--no-options", "--homedir", str(home), "--batch",
         "--no-auto-key-retrieve", "--no-auto-key-import", *arguments],
        input=input, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    ).stdout


def public_key(home):
    fingerprint = small_file(KEY_DIRECTORY / "sandboxctl-release.fingerprint").decode().strip()
    if not re.fullmatch(r"[0-9A-F]{40}", fingerprint):
        raise ValueError("The project signing fingerprint is invalid")
    public = small_file(KEY_DIRECTORY / KEY_NAME)
    gpg(home, "--import", input=public)
    records = gpg(home, "--with-colons", "--fingerprint", "--list-keys").decode().splitlines()
    fingerprints = [line.split(":")[9] for line in records if line.startswith("fpr:")]
    if sum(line.startswith("pub:") for line in records) != 1 or not fingerprints or fingerprints[0] != fingerprint:
        raise ValueError("The project public key does not match its pinned fingerprint")
    return public, fingerprint


def verify_signatures(directory, signatures):
    expected = {signatures / f"{name}-x86_64.pkg.tar.zst.sig" for name in PACKAGES}
    if set(signatures.glob("*.sig")) != expected:
        raise ValueError("Release files must include exactly two package signatures")
    with keyring() as home:
        public, fingerprint = public_key(home)
        if small_file(signatures / KEY_NAME) != public:
            raise ValueError("The release public key differs from the project public key")
        for signature in sorted(expected):
            small_file(signature)
            package = directory / signature.name.removesuffix(".sig")
            status = gpg(home, "--status-fd", "1", "--verify", str(signature), str(package))
            valid = [line.split() for line in status.decode().splitlines() if line.startswith("[GNUPG:] VALIDSIG ")]
            if len(valid) != 1 or fingerprint not in (valid[0][2], valid[0][-1]):
                raise ValueError(f"Package signature does not use the project key: {package.name}")


def verify_signed(directory, tag):
    os.environ.pop("RELEASE_SIGNING_KEY", None)
    os.environ.pop("RELEASE_SIGNING_PASSPHRASE", None)
    verify(directory, tag)
    verify_signatures(directory, directory)


def sign(directory, tag):
    secret = os.environ.pop("RELEASE_SIGNING_KEY", "")
    passphrase = os.environ.pop("RELEASE_SIGNING_PASSPHRASE", "")
    verify(directory, tag)
    if not secret or len(secret) > 1024 * 1024:
        raise ValueError("Set RELEASE_SIGNING_KEY to the armored project signing subkey")
    names = [f"{name}-x86_64.pkg.tar.zst.sig" for name in PACKAGES] + [KEY_NAME]
    if any((directory / name).exists() or (directory / name).is_symlink() for name in names):
        raise ValueError("Release signing files already exist")
    with keyring() as home, tempfile.TemporaryDirectory(prefix=".signing-", dir=directory) as name:
        staging = pathlib.Path(name)
        public, fingerprint = public_key(home)
        gpg(home, "--import", input=secret.encode())
        for package in PACKAGES:
            archive = directory / f"{package}-x86_64.pkg.tar.zst"
            gpg(home, "--pinentry-mode", "loopback", "--passphrase-fd", "0",
                "--local-user", fingerprint, "--output", str(staging / (archive.name + ".sig")),
                "--detach-sign", str(archive), input=(passphrase + "\n").encode())
        (staging / KEY_NAME).write_bytes(public)
        for name in names:
            (staging / name).chmod(0o600)
        verify_signatures(directory, staging)
        for name in names:
            # Refuse a destination created after the initial existence check.
            os.link(staging / name, directory / name)


def main():
    operations = {"verify": verify, "sign": sign, "verify-signed": verify_signed}
    if len(sys.argv) < 4 or sys.argv[1] not in ("create", *operations) or not TAG.fullmatch(sys.argv[3]):
        raise ValueError("Usage: release-artifacts.py create DIRECTORY TAG COMMIT | verify|sign|verify-signed DIRECTORY TAG")
    operation, directory, tag = sys.argv[1], pathlib.Path(sys.argv[2]).resolve(), sys.argv[3]
    if operation == "create" and len(sys.argv) == 5:
        create(directory, tag, sys.argv[4])
    elif operation in operations and len(sys.argv) == 4:
        operations[operation](directory, tag)
    else:
        raise ValueError("Invalid release artifact arguments")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f"release-artifacts: {error}", file=sys.stderr)
        sys.exit(1)
