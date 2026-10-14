#!/usr/bin/env python3
"""Create and verify the files uploaded with a sandboxctl GitHub release."""

import hashlib
import json
import pathlib
import re
import stat
import struct
import subprocess
import sys

TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")
COMMIT = re.compile(r"(?:[0-9a-f]{40}|[0-9a-f]{64})\Z")
DIGEST = re.compile(r"[0-9a-f]{64}\Z")
ARTIFACTS = ("vmlinux", "initramfs.cpio.gz", "root.squashfs")


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


def package_info(package, tag):
    info = {}
    for line in member(package, ".PKGINFO").decode().splitlines():
        if " = " in line:
            key, value = line.split(" = ", 1)
            if key in ("pkgname", "pkgver", "arch"):
                if key in info:
                    raise ValueError(f"Duplicate package field: {key}")
                info[key] = value
    if info != {"pkgname": "sandboxctl", "pkgver": tag[1:] + "-1", "arch": "x86_64"}:
        raise ValueError("Package name, version, or architecture does not match the release")
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


def expected_package(directory, tag):
    packages = list(directory.glob("*.pkg.tar.zst"))
    expected = directory / f"sandboxctl-{tag[1:]}-1-x86_64.pkg.tar.zst"
    if packages != [expected]:
        raise ValueError("Release directory must contain exactly one matching package")
    regular(expected)
    return expected


def create(directory, tag, commit):
    if not COMMIT.fullmatch(commit):
        raise ValueError("Invalid release source commit")
    package = expected_package(directory, tag)
    image = package_info(package, tag)
    data = {"schema_version": 1, "tag": tag, "version": tag[1:], "source_commit": commit,
            "package": {"file": package.name, "sha256": digest(package)}, "builder": image}
    record = directory / "release.json"
    with record.open("x") as output:
        json.dump(data, output, indent=2)
        output.write("\n")
    with (directory / "SHA256SUMS").open("x") as output:
        for path in (package, record):
            output.write(f"{digest(path)}  {path.name}\n")


def verify(directory, tag):
    package = expected_package(directory, tag)
    record = directory / "release.json"
    regular(record)
    regular(directory / "SHA256SUMS")
    data = json.loads(record.read_text())
    if not isinstance(data, dict):
        raise ValueError("Release metadata must be an object")
    if data.get("schema_version") != 1 or data.get("tag") != tag or data.get("version") != tag[1:] or not COMMIT.fullmatch(data.get("source_commit", "")):
        raise ValueError("Release metadata does not match the requested tag")
    expected = "".join(f"{digest(path)}  {path.name}\n" for path in (package, record))
    if (directory / "SHA256SUMS").read_text() != expected:
        raise ValueError("Release files do not match SHA256SUMS")
    if data.get("package") != {"file": package.name, "sha256": digest(package)}:
        raise ValueError("Release package checksum or filename is invalid")
    if data.get("builder") != package_info(package, tag):
        raise ValueError("Release builder metadata differs from the package")


def main():
    if len(sys.argv) < 4 or sys.argv[1] not in ("create", "verify") or not TAG.fullmatch(sys.argv[3]):
        raise ValueError("Usage: release-artifacts.py create DIRECTORY TAG COMMIT | verify DIRECTORY TAG")
    operation, directory, tag = sys.argv[1], pathlib.Path(sys.argv[2]).resolve(), sys.argv[3]
    if operation == "create" and len(sys.argv) == 5:
        create(directory, tag, sys.argv[4])
    elif operation == "verify" and len(sys.argv) == 4:
        verify(directory, tag)
    else:
        raise ValueError("Invalid release artifact arguments")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f"release-artifacts: {error}", file=sys.stderr)
        sys.exit(1)
