#!/usr/bin/env python3
"""Validate the two signed CI artifact sets before staging public release files."""
import argparse
import hashlib
import json
import pathlib
import re
import shutil


TARGETS = {"aarch64-apple-darwin": "aarch64", "x86_64-apple-darwin": "x64"}


def stage(directory, output, version, commit):
    if not re.fullmatch(r"\d+\.\d+\.\d+", version) or not re.fullmatch(r"[a-f0-9]{40}", commit):
        raise ValueError("Expected stable version and exact source commit")
    metadata = list(directory.rglob("build.json"))
    if len(metadata) != 2:
        raise ValueError("Expected exactly two signed macOS build metadata files")
    pending = []
    seen = set()
    for path in metadata:
        if path.is_symlink():
            raise ValueError("Symlinked build metadata is not a release artifact")
        data = json.loads(path.read_text())
        target = data.get("target")
        if target not in TARGETS or target in seen:
            raise ValueError("Expected one artifact for each supported macOS architecture")
        if data.get("distribution") != "release" or data.get("source_dirty") is not False or data.get("version") != version or data.get("commit") != commit:
            raise ValueError("macOS artifact is not a clean matching public release")
        seen.add(target)
        manifest = path.with_name("SHA256SUMS")
        if manifest.is_symlink() or not manifest.is_file():
            raise ValueError("Expected regular macOS checksum manifest")
        checked = set()
        for line in manifest.read_text().splitlines():
            digest, name = line.split(maxsplit=1)
            name = name.lstrip("*")
            item = path.parent / name
            if not re.fullmatch(r"[a-f0-9]{64}", digest) or pathlib.Path(name).name != name or name in checked or item.is_symlink() or not item.is_file():
                raise ValueError("Unsafe or invalid macOS checksum entry")
            if hashlib.sha256(item.read_bytes()).hexdigest() != digest:
                raise ValueError("macOS artifact checksum mismatch")
            checked.add(name)
        installers = list(path.parent.glob("*.dmg"))
        if len(installers) != 1 or installers[0].name not in checked or "build.json" not in checked:
            raise ValueError("Expected checksummed installer and build metadata")
        installer = installers[0]
        if installer.name != f"StackHarbor_{version}_{TARGETS[target]}.dmg":
            raise ValueError("DMG filename does not match version/architecture")
        pending.extend([(installer, output / installer.name), (path, output / f"desktop-{target}-build.json")])
    if seen != set(TARGETS):
        raise ValueError("Incomplete macOS architecture set")
    # Validate everything before the first external write; preserve existing files.
    checksums = output / "SHA256SUMS"
    if not checksums.is_file() or checksums.is_symlink():
        raise ValueError("Linux checksum manifest must already exist")
    for _, destination in pending:
        if destination.exists() or destination.is_symlink():
            raise ValueError(f"Refusing existing release destination: {destination.name}")
    lines = checksums.read_text().rstrip("\n")
    for source, destination in pending:
        shutil.copyfile(source, destination)
        lines += f"\n{hashlib.sha256(source.read_bytes()).hexdigest()}  {destination.name}"
    checksums.write_text(lines + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    args = parser.parse_args()
    stage(args.directory, args.output, args.version, args.commit)


if __name__ == "__main__":
    main()
