"""Build an unsigned iOS release without the SDK's native test plugin."""

import hashlib
import io
import json
import os
from pathlib import Path
import plistlib
import re
import shutil
import subprocess
import sys
import tarfile
from urllib.parse import urlsplit


ROOT = Path(__file__).resolve().parents[1]
TEST_PLUGIN = b"IntegrationTestPlugin"


def process_identity(pid):
    command = subprocess.check_output(["ps", "-p", str(pid), "-o", "command="], text=True).strip()
    started = " ".join(subprocess.check_output(["ps", "-p", str(pid), "-o", "lstart="], text=True).split())
    if not command or not started:
        raise ValueError("Cannot observe process ownership.")
    return command, started


def start_owned_child(stage):
    os.setsid()
    (stage / ".child-pgid").write_text(f"{os.getpid()}\n")
    _, started = process_identity(os.getpid())
    (stage / ".child-start").write_text(started + "\n")
    (stage / ".child-starting").unlink()
    if (stage / ".cancelled").exists():
        raise SystemExit(130)
    owner = int((stage / ".owner-pid").read_text())
    expected = ((stage / ".owner-command").read_text().strip(),
                (stage / ".owner-start").read_text().strip())
    if process_identity(owner) != expected or (stage / ".cancelled").exists():
        raise SystemExit(130)


def fingerprints(paths):
    return {p: hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}


def verify_fingerprints(expected):
    if fingerprints(expected) != expected:
        raise ValueError("Build inputs changed during packaging.")


def run(args, cwd, *, capture=False):
    result = subprocess.run(args, cwd=cwd, check=True, text=True,
                            stdout=subprocess.PIPE if capture else None)
    return result.stdout if capture else None


def source_revision(repo):
    dirty = run(["git", "status", "--porcelain", "--untracked-files=all", "--",
                 "apps/client", ".tool-versions"], repo, capture=True)
    if dirty:
        raise ValueError("Commit or preserve client/tool-pin changes before packaging.")
    return run(["git", "rev-parse", "HEAD"], repo, capture=True).strip()


def validate_api_url(value):
    url = urlsplit(value)
    if (url.scheme not in {"http", "https"} or not url.hostname or
            url.username or url.password or url.query or url.fragment):
        raise ValueError("API_URL must be an HTTP(S) base URL without credentials/query/fragment.")
    if url.port is not None and url.port <= 0:
        raise ValueError("API_URL has an invalid port.")


def runtime_lock(graph, lock):
    packages = {p["name"]: p for p in graph["packages"]}
    root = packages[graph["root"]]
    pending = list(root["directDependencies"])
    names = set()
    while pending:
        name = pending.pop()
        if name not in names:
            names.add(name)
            pending.extend(packages[name]["dependencies"])
    blocks = dict(re.findall(r"^  (\w+):\n(.*?)(?=^  \w+:\n|^sdks:|\Z)",
                             lock, re.MULTILINE | re.DOTALL))
    return {name: {"version": packages[name]["version"],
                   "source": packages[name]["source"], "lock": blocks[name]}
            for name in sorted(names)}


def verify_registration(app):
    graph = app / ".flutter-plugins-dependencies"
    if graph.exists():
        data = json.loads(graph.read_text())
        if any(p["name"] == "integration_test"
               for entries in data["plugins"].values() for p in entries):
            raise ValueError("Native integration_test remains in the plugin graph.")
    registrant = app / "ios/Runner/GeneratedPluginRegistrant.m"
    if TEST_PLUGIN in registrant.read_bytes():
        raise ValueError("IntegrationTestPlugin remains in the iOS registrant.")
    manifest = app / "ios/Flutter/ephemeral/Packages/FlutterGeneratedPluginSwiftPackage/Package.swift"
    if manifest.exists() and b"integration_test" in manifest.read_bytes():
        raise ValueError("integration_test remains in the Swift package manifest.")


def contains_test_plugin(path):
    tail = b""
    with path.open("rb") as stream:
        while chunk := stream.read(1024 * 1024):
            data = tail + chunk
            if TEST_PLUGIN in data:
                return True
            tail = data[-len(TEST_PLUGIN):]
    return False


def verify_bundle_links(bundle):
    if not bundle.is_dir() or bundle.is_symlink():
        raise ValueError("The release app must be a real directory.")
    for path in bundle.rglob("*"):
        if path.is_symlink() and (not path.resolve().is_relative_to(bundle.resolve()) or path.is_dir()):
            raise ValueError("The app has an external or directory symlink.")


def verify_binary(bundle):
    verify_bundle_links(bundle)
    binaries = {}
    main_info = bundle / "Info.plist"
    if not main_info.is_file():
        raise ValueError("The release app Info.plist is missing.")
    for info in sorted(bundle.rglob("Info.plist")):
        data = plistlib.loads(info.read_bytes())
        name = data.get("CFBundleExecutable")
        if not name:
            if info == main_info:
                raise ValueError("CFBundleExecutable is missing.")
            continue
        if not isinstance(name, str) or Path(name).name != name:
            raise ValueError("Invalid bundle executable name.")
        binary = info.parent / name
        if not binary.resolve().is_relative_to(bundle.resolve()) or not binary.is_file():
            raise ValueError("A bundle executable is missing or outside the app.")
        with binary.open("rb") as stream:
            magic = stream.read(4)
        if magic not in {b"\xcf\xfa\xed\xfe", b"\xfe\xed\xfa\xcf", b"\xca\xfe\xba\xbe",
                         b"\xbe\xba\xfe\xca", b"\xca\xfe\xba\xbf", b"\xbf\xba\xfe\xca"}:
            raise ValueError("A bundle executable is not a supported Mach-O binary.")
        inspected = run(["xcrun", "otool", "-hv", str(binary)], bundle, capture=True)
        if "Mach header" not in inspected:
            raise ValueError("Native inspection returned no Mach header.")
        if contains_test_plugin(binary):
            raise ValueError("IntegrationTestPlugin remains in the release binary.")
        with binary.open("rb") as stream:
            binaries[str(binary.relative_to(bundle))] = hashlib.file_digest(stream, "sha256").hexdigest()
    return binaries


def publish(bundle, output, metadata):
    if any(output.iterdir()):
        raise FileExistsError("Release output is not empty; existing artifacts are preserved.")
    verify_bundle_links(bundle)
    copied = output / "Runner.app"
    shutil.copytree(bundle, copied)
    for relative, digest in metadata["binary_sha256"].items():
        with (copied / relative).open("rb") as stream:
            if hashlib.file_digest(stream, "sha256").hexdigest() != digest:
                raise ValueError("Published executable does not match the verified binary.")
    (output / "build.json").write_text(json.dumps(metadata, indent=2) + "\n")


def build(stage, output, api_url, flutter):
    builder_inputs = fingerprints([Path(__file__), ROOT / "Makefile"])
    validate_api_url(api_url)
    revision = source_revision(ROOT)
    if output.name != f"ios-release-{revision}":
        raise ValueError("Source revision changed after the output was reserved.")
    archive = subprocess.check_output(["git", "archive", revision, ".tool-versions", "apps/client"], cwd=ROOT)
    with tarfile.open(fileobj=io.BytesIO(archive)) as tree:
        tree.extractall(stage, filter="data")
    app = stage / "apps/client"
    original = {p: hashlib.sha256(p.read_bytes()).hexdigest()
                for p in stage.rglob("*") if p.is_file()}
    expected_flutter = next(line.split()[1] for line in (stage / ".tool-versions").read_text().splitlines()
                            if line.startswith("flutter ")).removesuffix("-stable")
    version = json.loads(run([flutter, "--version", "--machine"], app, capture=True))
    if version["frameworkVersion"] != expected_flutter or version["channel"] != "stable":
        raise ValueError("Flutter does not match the archived stable tool pin.")
    run([flutter, "pub", "get", "--offline", "--enforce-lockfile"], app)
    before = json.loads(run([flutter, "pub", "deps", "--json"], app, capture=True))
    locked = runtime_lock(before, (app / "pubspec.lock").read_text())
    run([flutter, "pub", "remove", "--offline", "integration_test"], app)
    after = json.loads(run([flutter, "pub", "deps", "--json"], app, capture=True))
    if locked != runtime_lock(after, (app / "pubspec.lock").read_text()):
        raise ValueError("Production dependencies changed while removing the test dependency.")
    if any(p["name"] == "integration_test" for p in after["packages"]):
        raise ValueError("integration_test remains in the resolved dependency graph.")
    run([flutter, "build", "ios", "--release", "--no-codesign", "--config-only", "--no-pub",
         f"--dart-define=OMNI_API_URL={api_url}"], app)
    verify_registration(app)
    allowed = {app / "pubspec.yaml", app / "pubspec.lock"}

    def unchanged_source():
        if any(not p.is_file() or hashlib.sha256(p.read_bytes()).hexdigest() != digest
               for p, digest in original.items() if p not in allowed):
            raise ValueError("Tracked client source/assets/native settings changed in staging.")

    unchanged_source()
    args = ["xcrun", "xcodebuild", "-workspace", "ios/Runner.xcworkspace", "-scheme", "Runner",
            "-configuration", "Release", "-sdk", "iphoneos", "-destination", "generic/platform=iOS",
            "-jobs", "1", "-derivedDataPath", str(stage / "DerivedData"),
            "-clonedSourcePackagesDirPath", str(stage / "SourcePackages"),
            f"CONFIGURATION_BUILD_DIR={stage / 'products'}", "CODE_SIGNING_ALLOWED=NO",
            "CODE_SIGNING_REQUIRED=NO", "CODE_SIGN_IDENTITY=", "build"]
    print("IOS_XCODEBUILD " + json.dumps(args), flush=True)
    run(args, app)
    unchanged_source()
    verify_registration(app)
    bundle = stage / "products/Runner.app"
    binaries = verify_binary(bundle)
    verify_fingerprints(builder_inputs)
    metadata = {"source_revision": revision,
                "client_tree": run(["git", "rev-parse", f"{revision}:apps/client"], ROOT, capture=True).strip(),
                "builder_sha256": builder_inputs[Path(__file__)],
                "makefile_sha256": builder_inputs[ROOT / "Makefile"],
                "api_url": api_url, "flutter": version,
                "xcode": run(["xcrun", "xcodebuild", "-version"], app, capture=True).strip(),
                "removed_dev_dependency": "integration_test", "runtime_dependencies": locked,
                "xcodebuild_args": args, "binary_sha256": binaries, "codesigned": False,
                "staged_pubspec": (app / "pubspec.yaml").read_text(),
                "staged_lock": (app / "pubspec.lock").read_text()}
    publish(bundle, output, metadata)


if __name__ == "__main__":
    stage = Path(sys.argv[1])
    start_owned_child(stage)
    build(stage, Path(sys.argv[2]), os.environ["IOS_RELEASE_API_URL"],
          os.environ["IOS_RELEASE_FLUTTER"])
