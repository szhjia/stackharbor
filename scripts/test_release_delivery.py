"""Hermetic signed-artifact staging contracts; no Apple/GitHub operations."""
import hashlib
import importlib.util
import json
import pathlib
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("stage-macos-release.py")
SPEC = importlib.util.spec_from_file_location("stage_macos_release", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
COMMIT = "a" * 40
VERSION = "1.2.3"


class ReleaseDeliveryTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="sh-release-delivery-")
        self.root = pathlib.Path(self.temporary.name)
        self.artifacts = self.root / "artifacts"
        self.output = self.root / "dist"
        self.output.mkdir()
        (self.output / "SHA256SUMS").write_text("linux-manifest\n")
        self.builds = []
        for target, architecture in MODULE.TARGETS.items():
            directory = self.artifacts / target
            directory.mkdir(parents=True)
            self.builds.append(directory)
            metadata = {"target": target, "distribution": "release", "source_dirty": False,
                        "version": VERSION, "commit": COMMIT}
            (directory / "build.json").write_text(json.dumps(metadata))
            (directory / f"StackHarbor_{VERSION}_{architecture}.dmg").write_bytes(b"signed fixture; signature verified by upstream macOS job")
            self.manifest(directory)

    def tearDown(self):
        self.temporary.cleanup()

    def manifest(self, directory):
        entries = [item for item in directory.iterdir() if item.name != "SHA256SUMS"]
        (directory / "SHA256SUMS").write_text("".join(f"{hashlib.sha256(item.read_bytes()).hexdigest()}  {item.name}\n" for item in entries))

    def assert_refused_without_writes(self):
        with self.assertRaises((ValueError, OSError)):
            MODULE.stage(self.artifacts, self.output, VERSION, COMMIT)
        self.assertEqual([item.name for item in self.output.iterdir()], ["SHA256SUMS"])
        self.assertEqual((self.output / "SHA256SUMS").read_text(), "linux-manifest\n")

    def test_both_clean_matching_architectures_stage_with_combined_checksums(self):
        MODULE.stage(self.artifacts, self.output, VERSION, COMMIT)
        manifest = (self.output / "SHA256SUMS").read_text()
        self.assertTrue(manifest.startswith("linux-manifest\n"))
        for target, architecture in MODULE.TARGETS.items():
            for name in (f"StackHarbor_{VERSION}_{architecture}.dmg", f"desktop-{target}-build.json"):
                data = (self.output / name).read_bytes()
                self.assertIn(f"{hashlib.sha256(data).hexdigest()}  {name}\n", manifest)

    def test_desktop_builder_names_pass_public_staging_for_both_architectures(self):
        # Use the producer's naming function, rather than deriving fixture names
        # from the validator's own mapping (which hid the Intel mismatch).
        builder = SCRIPT.with_name("desktop-build.mjs").as_uri()
        result = subprocess.run(
            ["node", "--input-type=module", "-e",
             f'import {{macOSInstallerName}} from {json.dumps(builder)}; '
             'console.log(JSON.stringify(process.argv.slice(2).map(target => '
             'macOSInstallerName("StackHarbor", process.argv[1], target))));',
             VERSION, *MODULE.TARGETS],
            check=True, capture_output=True, text=True)
        names = json.loads(result.stdout)
        for directory, name in zip(self.builds, names):
            next(directory.glob("*.dmg")).rename(directory / name)
            self.manifest(directory)
        MODULE.stage(self.artifacts, self.output, VERSION, COMMIT)
        manifest = (self.output / "SHA256SUMS").read_text()
        for name in names:
            digest = hashlib.sha256((self.output / name).read_bytes()).hexdigest()
            self.assertIn(f"{digest}  {name}\n", manifest)

    def test_rejects_internal_dirty_mismatched_or_duplicate_target_before_writes(self):
        for key, value in (("distribution", "internal"), ("source_dirty", True), ("commit", "b" * 40), ("version", "0.0.1"), ("target", "unsupported")):
            with self.subTest(key=key):
                path = self.builds[1] / "build.json"
                original = path.read_text()
                metadata = json.loads(original)
                metadata[key] = value
                path.write_text(json.dumps(metadata))
                self.manifest(path.parent)
                self.assert_refused_without_writes()
                path.write_text(original)
                self.manifest(path.parent)

    def test_rejects_corruption_missing_architecture_and_unsafe_manifest(self):
        directory = self.builds[1]
        installer = next(directory.glob("*.dmg"))
        installer.write_bytes(b"corrupt")
        self.assert_refused_without_writes()
        self.manifest(directory)
        manifest = directory / "SHA256SUMS"
        manifest.write_text("0" * 64 + "  ../unowned\n")
        self.assert_refused_without_writes()
        manifest.unlink()
        (directory / "build.json").unlink()
        self.assert_refused_without_writes()

    def test_refuses_symlinked_installers_and_existing_destinations(self):
        installer = next(self.builds[1].glob("*.dmg"))
        outside = self.root / "unowned.dmg"
        outside.write_bytes(installer.read_bytes())
        installer.unlink()
        installer.symlink_to(outside)
        self.manifest(installer.parent)
        self.assert_refused_without_writes()
        installer.unlink()
        installer.write_bytes(outside.read_bytes())
        self.manifest(installer.parent)
        destination = self.output / installer.name
        destination.write_bytes(b"keep")
        with self.assertRaises(ValueError):
            MODULE.stage(self.artifacts, self.output, VERSION, COMMIT)
        self.assertEqual(destination.read_bytes(), b"keep")
        self.assertEqual((self.output / "SHA256SUMS").read_text(), "linux-manifest\n")


if __name__ == "__main__":
    unittest.main()
