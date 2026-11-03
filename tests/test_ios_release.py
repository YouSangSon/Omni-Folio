import json
import hashlib
from pathlib import Path
import plistlib
import subprocess
import tempfile
import shutil
import unittest
from unittest.mock import patch

from tools import ios_release


class IOSReleaseTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="omni-ios-unit-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)

    def registration(self):
        app = self.root / "client"
        (app / "ios/Runner").mkdir(parents=True)
        (app / "ios/Runner/GeneratedPluginRegistrant.m").write_text("registerWithRegistry {}")
        (app / ".flutter-plugins-dependencies").write_text(json.dumps({"plugins": {"ios": []}}))
        return app

    def bundle(self):
        bundle = self.root / "Runner.app"
        bundle.mkdir()
        (bundle / "Info.plist").write_bytes(plistlib.dumps({"CFBundleExecutable": "Runner"}))
        (bundle / "Runner").write_bytes(b"\xcf\xfa\xed\xfeunit-stub")
        return bundle

    def test_dirty_source_refuses_before_archiving(self):
        with patch.object(ios_release, "run", return_value=" M apps/client/lib/app.dart\n") as run:
            with self.assertRaisesRegex(ValueError, "client/tool-pin"):
                ios_release.source_revision(self.root)
        self.assertEqual(run.call_count, 1)

    def test_api_url_accepts_base_paths_and_rejects_embedded_secrets(self):
        ios_release.validate_api_url("http://127.0.0.1:8080")
        ios_release.validate_api_url("https://example.com/omni")
        for value in ["file:///tmp/data", "https://user:secret@example.com", "https://host/?token=secret",
                      "https://host/#secret", "https://host:99999", "http://host:0"]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                ios_release.validate_api_url(value)

    def test_runtime_projection_excludes_only_unreachable_dev_packages(self):
        graph = {"root": "app", "packages": [
            {"name": "app", "directDependencies": ["http"], "dependencies": ["http", "integration_test"]},
            {"name": "http", "version": "1", "source": "hosted", "dependencies": ["meta"]},
            {"name": "meta", "version": "2", "source": "hosted", "dependencies": []},
            {"name": "integration_test", "version": "0", "source": "sdk", "dependencies": []}]}
        lock = "packages:\n  http:\n    sha256: old\n  meta:\n    sha256: same\n  integration_test:\n    source: sdk\nsdks:\n  dart: any\n"
        before = ios_release.runtime_lock(graph, lock)
        graph["packages"].pop()
        self.assertEqual(before, ios_release.runtime_lock(graph, lock))
        self.assertNotEqual(before, ios_release.runtime_lock(graph, lock.replace("sha256: old", "sha256: tampered")))
        with self.assertRaises(KeyError):
            ios_release.runtime_lock(graph, "packages:\n")

    def test_registration_rejects_plugin_graph_and_generated_references(self):
        app = self.registration()
        ios_release.verify_registration(app)
        graph = app / ".flutter-plugins-dependencies"
        graph.write_text(json.dumps({"plugins": {"ios": [{"name": "integration_test"}]}}))
        with self.assertRaisesRegex(ValueError, "plugin graph"):
            ios_release.verify_registration(app)
        graph.write_text('{"plugins":{"ios":[]}}')
        registrant = app / "ios/Runner/GeneratedPluginRegistrant.m"
        registrant.write_bytes(ios_release.TEST_PLUGIN)
        with self.assertRaisesRegex(ValueError, "registrant"):
            ios_release.verify_registration(app)
        registrant.write_text("registerWithRegistry {}")
        manifest = app / "ios/Flutter/ephemeral/Packages/FlutterGeneratedPluginSwiftPackage/Package.swift"
        manifest.parent.mkdir(parents=True)
        manifest.write_text("integration_test")
        with self.assertRaisesRegex(ValueError, "Swift package"):
            ios_release.verify_registration(app)

    def test_binary_guard_checks_frameworks_and_chunk_boundaries(self):
        bundle = self.bundle()
        with patch.object(ios_release, "run", return_value="Mach header"):
            self.assertIn("Runner", ios_release.verify_binary(bundle))
            framework = bundle / "Frameworks/Test.framework"
            framework.mkdir(parents=True)
            (framework / "Info.plist").write_bytes(plistlib.dumps({"CFBundleExecutable": "Test"}))
            (framework / "Test").write_bytes(b"\xcf\xfa\xed\xfe" + b"x" * (1024 * 1024 - 9) + ios_release.TEST_PLUGIN)
            with self.assertRaisesRegex(ValueError, "release binary"):
                ios_release.verify_binary(bundle)

    def test_missing_escaping_or_uninspectable_binary_fails(self):
        bundle = self.bundle()
        with patch.object(ios_release, "run", side_effect=subprocess.CalledProcessError(1, "otool")):
            with self.assertRaises(subprocess.CalledProcessError):
                ios_release.verify_binary(bundle)
        with patch.object(ios_release, "run", return_value=""):
            with self.assertRaisesRegex(ValueError, "no Mach header"):
                ios_release.verify_binary(bundle)
        (bundle / "Runner").unlink()
        with self.assertRaisesRegex(ValueError, "missing or outside"):
            ios_release.verify_binary(bundle)
        (bundle / "Info.plist").write_bytes(plistlib.dumps({"CFBundleExecutable": "../outside"}))
        with self.assertRaisesRegex(ValueError, "Invalid bundle"):
            ios_release.verify_binary(bundle)

    def test_publish_preserves_existing_output_and_has_no_early_completion_receipt(self):
        bundle = self.bundle()
        output = self.root / "output"
        output.mkdir()
        metadata = {"binary_sha256": {"Runner": hashlib.sha256((bundle / "Runner").read_bytes()).hexdigest()}}
        ios_release.publish(bundle, output, metadata)
        original = (output / "build.json").read_bytes()
        self.assertFalse((output / "COMPLETE").exists())
        with self.assertRaises(FileExistsError):
            ios_release.publish(bundle, output, {"source_revision": "replacement"})
        self.assertEqual(original, (output / "build.json").read_bytes())

    def test_published_app_does_not_depend_on_deleted_staging_symlinks(self):
        bundle = self.bundle()
        data = (bundle / "Runner").read_bytes()
        (bundle / "NativeExecutable").write_bytes(data)
        (bundle / "Runner").unlink()
        (bundle / "Runner").symlink_to(bundle / "NativeExecutable")
        output = self.root / "output"
        output.mkdir()
        metadata = {"binary_sha256": {"Runner": hashlib.sha256(data).hexdigest()}}
        ios_release.publish(bundle, output, metadata)
        shutil.rmtree(bundle)
        self.assertFalse((output / "Runner.app/Runner").is_symlink())
        self.assertEqual(data, (output / "Runner.app/Runner").read_bytes())

    def test_external_resource_link_is_rejected_before_reading_it(self):
        bundle = self.bundle()
        external = self.root / "outside.plist"
        external.write_text("private fixture")
        (bundle / "outside-link").symlink_to(external)
        with self.assertRaisesRegex(ValueError, "external"):
            ios_release.verify_binary(bundle)

    def test_builder_change_prevents_publication(self):
        source = self.root / "builder.py"
        source.write_text("initial")
        original = ios_release.fingerprints([source])
        source.write_text("changed")
        with self.assertRaisesRegex(ValueError, "inputs changed"):
            ios_release.verify_fingerprints(original)

    def test_late_child_never_builds_after_cancellation_or_owner_reuse(self):
        for cancelled in [True, False]:
            with self.subTest(cancelled=cancelled):
                (self.root / ".child-starting").touch()
                (self.root / ".owner-pid").write_text("123")
                (self.root / ".owner-command").write_text("owner")
                (self.root / ".owner-start").write_text("original")
                marker = self.root / ".cancelled"
                if cancelled:
                    marker.touch()
                else:
                    marker.unlink()
                identities = [("worker", "now"), ("replacement", "later")]
                with patch.object(ios_release.os, "setsid"), patch.object(ios_release, "process_identity", side_effect=identities):
                    with self.assertRaises(SystemExit) as failure:
                        ios_release.start_owned_child(self.root)
                self.assertEqual(130, failure.exception.code)


if __name__ == "__main__":
    unittest.main()
