"""Bundle pinned Rust dependency declarations and original license texts."""
import json
import pathlib
import re
import shutil
import subprocess
root = pathlib.Path(__file__).resolve().parent.parent
metadata = json.loads(subprocess.check_output(["cargo", "metadata", "--locked", "--format-version", "1", "--manifest-path", str(root / "desktop/Cargo.toml")], text=True))
destination = root / "desktop/generated/licenses/rust"
destination.mkdir(parents=True, exist_ok=True)
notices = []
for package in sorted(metadata["packages"], key=lambda p: (p["name"], p["version"])):
    if not package.get("source"):
        continue
    source = pathlib.Path(package["manifest_path"]).parent
    target = destination / f'{package["name"]}-{package["version"]}'
    target.mkdir(exist_ok=True)
    paths = [p for p in source.iterdir() if re.match(r"^(license|licence|copying|notice|copyright)([._-]|$)", p.name, re.I)]
    license_file = package.get("license_file")
    if license_file and (source / license_file).is_file():
        paths.append(source / license_file)
    for path in set(paths):
        if path.is_dir():
            shutil.copytree(path, target / path.name, dirs_exist_ok=True)
        else:
            shutil.copyfile(path, target / path.name)
    if not paths and not package.get("license"):
        raise RuntimeError(f'No license declaration or text for {package["name"]}')
    notices.append({"name": package["name"], "version": package["version"], "license": package.get("license"), "repository": package.get("repository"), "texts": sorted(p.name for p in set(paths))})
(destination / "NOTICES.json").write_text(json.dumps(notices, indent=2) + "\n")
print(f"Bundled Rust license declarations/texts for {len(notices)} pinned packages")
