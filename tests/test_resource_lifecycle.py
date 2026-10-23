"""Exercise the real Make resource wrappers with owned process trees."""

import json
import os
from pathlib import Path
import signal
import shutil
import subprocess
import tempfile
import textwrap
import time
import unittest


MAKEFILE = Path(__file__).resolve().parents[1] / "Makefile"
PROCESS_WAIT_SECONDS = 10


class CleanupIdentityError(RuntimeError):
    """The harness could not prove that a process group is still its own."""


class FixtureDirectory:
    """Preserve evidence when exact process cleanup cannot be proven."""

    def __init__(self):
        self.path = Path(tempfile.mkdtemp(prefix="omni-folio-resource-lifecycle."))

    def __enter__(self):
        return self.path

    def __exit__(self, exception_type, _exception, _traceback):
        if exception_type is CleanupIdentityError:
            print(f"PRESERVED cleanup-unverified fixture={self.path}", flush=True)
            return False
        shutil.rmtree(self.path)
        return False


WORKLOAD = r'''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


def wait_for_session_root(tmp_parent, pgid):
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        candidates = [tmp_parent]
        candidates += sorted(tmp_parent.glob("omni-folio-test.*"))
        candidates += sorted(tmp_parent.glob("omni-folio-smoke.*"))
        for candidate in candidates:
            try:
                recorded = int(candidate.joinpath(".child-pgid").read_text().strip())
            except (FileNotFoundError, ValueError):
                continue
            if recorded == pgid:
                return candidate
        time.sleep(0.01)
    raise RuntimeError(f"owned session root was not published for pgid {pgid}")


def stop(_signum, _frame):
    raise SystemExit(0)


def process_start(pid):
    return " ".join(subprocess.check_output(
        ["ps", "-p", str(pid), "-o", "lstart="], text=True
    ).split())


if len(sys.argv) > 1 and sys.argv[1] == "--descendant":
    evidence = Path(sys.argv[2])
    record_path = Path(sys.argv[3])
    payload = json.loads(sys.argv[4])
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    leaf = subprocess.Popen(["/bin/sleep", "300"])
    payload.update(
        descendant_pid=os.getpid(),
        descendant_start=process_start(os.getpid()),
        leaf_pid=leaf.pid,
        leaf_start=process_start(leaf.pid),
    )
    pending = record_path.with_suffix(".pending")
    pending.write_text(json.dumps(payload, sort_keys=True), encoding="utf-8")
    pending.replace(record_path)
    evidence.joinpath(f"ready-{payload['main_pid']}").touch()
    while True:
        signal.pause()


target = "smoke-build" if len(sys.argv) > 1 and sys.argv[1] == "build" else sys.argv[1]
evidence = Path(os.environ["LIFECYCLE_EVIDENCE"])
tmp_parent = Path(os.environ["TMPDIR"])
pgid = os.getpgid(0)
session_root = wait_for_session_root(tmp_parent, pgid)
artifact = session_root / f"workload-artifact-{os.getpid()}"
artifact.write_text(target, encoding="utf-8")
record_path = evidence / f"record-{os.getpid()}.json"
payload = {
    "artifact": str(artifact),
    "main_pid": os.getpid(),
    "main_start": process_start(os.getpid()),
    "pgid": pgid,
    "pgid_start": session_root.joinpath(".child-start").read_text().strip(),
    "session_root": str(session_root),
    "target": target,
}
subprocess.Popen(
    [
        sys.executable, __file__, "--descendant", str(evidence), str(record_path),
        json.dumps(payload, sort_keys=True),
    ],
    stdin=subprocess.DEVNULL,
    stdout=subprocess.DEVNULL,
    stderr=subprocess.DEVNULL,
    close_fds=True,
)
ready = evidence / f"ready-{os.getpid()}"
deadline = time.monotonic() + 5
while time.monotonic() < deadline and not ready.exists():
    time.sleep(0.01)
if not ready.exists():
    raise RuntimeError("owned descendant did not publish readiness")

mode = os.environ["LIFECYCLE_MODE"]
if mode == "block":
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    while True:
        signal.pause()
if mode == "fail":
    print(f"injected workload failure: {target}", file=sys.stderr, flush=True)
    raise SystemExit(42)
raise SystemExit(0)
'''


class ResourceLifecycleTest(unittest.TestCase):
    def test_outer_wrappers_reap_exact_process_groups_and_owned_artifacts(self):
        cases = (
            ("test", "success", None),
            ("test", "fail", None),
            ("test", "block", signal.SIGINT),
            ("test", "block", signal.SIGTERM),
            ("check", "success", None),
            ("check", "fail", None),
            ("check", "block", signal.SIGINT),
            ("check", "block", signal.SIGTERM),
            ("smoke", "fail", None),
            ("smoke", "block", signal.SIGINT),
            ("smoke", "block", signal.SIGTERM),
        )
        for target, mode, injected_signal in cases:
            with self.subTest(target=target, mode=mode, signal=injected_signal):
                self._run_case(target, mode, injected_signal)

    def test_fail_safe_refuses_child_group_without_start_identity(self):
        with tempfile.TemporaryDirectory(prefix="omni-folio-lifecycle-incomplete.") as directory:
            root = Path(directory)
            child_file = root / ".child-pgid"
            child_file.write_text("2147483647\n", encoding="utf-8")
            # Missing metadata is a filesystem guard, not another reason to
            # start a detached process. The matrix below exercises real groups.
            with self.assertRaisesRegex(CleanupIdentityError, "incomplete child identity"):
                self._read_child_identity(child_file, timeout=0.02)
            self.assertEqual(child_file.read_text(encoding="utf-8"), "2147483647\n")

    def _run_case(self, target, mode, injected_signal):
        with FixtureDirectory() as root:
            tmp_parent = root / "tmp"
            evidence = root / "evidence"
            tmp_parent.mkdir()
            evidence.mkdir()
            (root / "services/core").mkdir(parents=True)
            (root / "services/research").mkdir(parents=True)
            sentinel = root / "unrelated-sentinel"
            sentinel.write_text("preserve", encoding="utf-8")
            workload = root / "workload.py"
            workload.write_text(WORKLOAD, encoding="utf-8")
            workload.chmod(0o700)
            fixture_makefile = root / "Makefile"
            fixture_makefile.write_text(
                textwrap.dedent(
                    f"""\
                    include {MAKEFILE}

                    .PHONY: test-body format-check lint contract-check
                    test-body format-check lint contract-check:
                    \t@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(LIFECYCLE_WORKLOAD)" "$@"
                    """
                ),
                encoding="utf-8",
            )
            environment = {
                **os.environ,
                "LIFECYCLE_EVIDENCE": str(evidence),
                "LIFECYCLE_MODE": mode,
                "LIFECYCLE_WORKLOAD": str(workload),
                "MAKEFLAGS": "",
                "MFLAGS": "",
                "TMPDIR": str(tmp_parent),
            }
            command = ["/usr/bin/make", "--no-print-directory", target]
            if target == "smoke":
                command.append(f"GO={workload}")

            process = None
            outer_start = None
            try:
                process = subprocess.Popen(
                    command,
                    cwd=root,
                    env=environment,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                    start_new_session=True,
                )
                startup_probe = os.environ.pop("LIFECYCLE_STARTUP_PROBE", None)
                if startup_probe:
                    Path(startup_probe).touch()
                    signal.pause()
                outer_identity = self._process_rows().get(process.pid)
                if outer_identity is None or outer_identity[0] != process.pid:
                    raise CleanupIdentityError(f"cannot verify outer make identity: {process.pid}")
                outer_start = outer_identity[1]
                if injected_signal is not None:
                    self._wait_for_ready(evidence, process)
                    self._assert_ready_process_tree(self._records(evidence))
                    owner_pid = self._single_owner_pid(tmp_parent)
                    os.kill(owner_pid, injected_signal)
                stdout, stderr = process.communicate(timeout=PROCESS_WAIT_SECONDS)
                records = self._records(evidence)
                self.assertTrue(records, stdout + stderr)
                expected_targets = {
                    "test": ["test-body"],
                    "check": ["format-check", "lint", "test-body", "contract-check"]
                    if mode == "success"
                    else ["format-check"],
                    "smoke": ["smoke-build"],
                }[target]
                self.assertCountEqual([record["target"] for record in records], expected_targets)
                if mode == "success":
                    self.assertEqual(process.returncode, 0, stdout + stderr)
                elif mode == "fail":
                    self.assertEqual(process.returncode, 2, stdout + stderr)
                    self.assertIn("injected workload failure", stderr)
                else:
                    expected = 128 + injected_signal
                    self.assertEqual(process.returncode, 2, stdout + stderr)
                    self.assertIn(f"Error {expected}", stderr)

                self._wait_for_process_absence(records)
                self.assertEqual(
                    list(tmp_parent.glob("omni-folio-test.*"))
                    + list(tmp_parent.glob("omni-folio-smoke.*")),
                    [],
                    stdout + stderr,
                )
                for record in records:
                    self.assertFalse(Path(record["artifact"]).exists(), record)
                    self.assertFalse(Path(record["session_root"]).exists(), record)
                self.assertEqual(sentinel.read_text(encoding="utf-8"), "preserve")
                unique_pgids = sorted({record["pgid"] for record in records})
                print(
                    f"CASE target={target} mode={mode} "
                    f"signal={injected_signal or '-'} outer_status={process.returncode} "
                    f"records={len(records)} pgids={unique_pgids} residue=0 sentinel=preserved",
                    flush=True,
                )
            except BaseException as error:
                if process is not None:
                    if outer_start is None:
                        recovered = self._process_rows().get(process.pid)
                        if recovered is None or recovered[0] != process.pid:
                            raise CleanupIdentityError(
                                f"outer make identity unavailable during cleanup: {process.pid}"
                            ) from error
                        outer_start = recovered[1]
                    self._terminate_owned(process, outer_start, tmp_parent, evidence)
                raise

    def _wait_for_ready(self, evidence, process):
        deadline = time.monotonic() + PROCESS_WAIT_SECONDS
        while time.monotonic() < deadline:
            if list(evidence.glob("ready-*")):
                return
            if process.poll() is not None:
                stdout, stderr = process.communicate()
                self.fail(f"wrapper exited before workload readiness\n{stdout}{stderr}")
            time.sleep(0.01)
        self.fail("timed out waiting for workload readiness")

    def _single_owner_pid(self, tmp_parent):
        owner_files = list(tmp_parent.glob("omni-folio-test.*/.owner-pid"))
        owner_files += list(tmp_parent.glob("omni-folio-smoke.*/.owner-pid"))
        self.assertEqual(len(owner_files), 1, owner_files)
        return int(owner_files[0].read_text(encoding="utf-8").strip())

    @staticmethod
    def _records(evidence):
        return [json.loads(path.read_text(encoding="utf-8")) for path in sorted(evidence.glob("record-*.json"))]

    def _assert_ready_process_tree(self, records):
        self.assertEqual(len(records), 1, records)
        record = records[0]
        self.assertTrue(Path(record["artifact"]).is_file(), record)
        rows = self._process_rows()
        for role in ("main", "descendant", "leaf"):
            self.assertEqual(rows.get(record[f"{role}_pid"]), (record["pgid"], record[f"{role}_start"]))

    def _wait_for_process_absence(self, records):
        pids = sorted({pid for row in records for pid in (row["main_pid"], row["descendant_pid"], row["leaf_pid"])})
        pgids = sorted({row["pgid"] for row in records})
        deadline = time.monotonic() + PROCESS_WAIT_SECONDS
        live_pids = pids
        live_pgids = pgids
        while time.monotonic() < deadline:
            rows = self._process_rows()
            live_pids = sorted(set(pids) & set(rows))
            live_pgids = sorted(set(pgids) & {pgid for pgid, _start in rows.values()})
            if not live_pids and not live_pgids:
                return
            time.sleep(0.02)
        self.fail(f"owned processes remain: pids={live_pids}, pgids={live_pgids}")

    def _terminate_owned(self, process, outer_start, tmp_parent, evidence):
        identities = {}
        if process.poll() is None:
            identities.setdefault(process.pid, {})[process.pid] = outer_start
        for child_file in list(tmp_parent.glob("omni-folio-test.*/.child-pgid")) + list(
            tmp_parent.glob("omni-folio-smoke.*/.child-pgid")
        ):
            child_identity = self._read_child_identity(child_file)
            if child_identity is not None:
                pgid, start = child_identity
                identities.setdefault(pgid, {})[pgid] = start
        for record in self._records(evidence):
            group = identities.setdefault(record["pgid"], {})
            group[record["pgid"]] = record["pgid_start"]
            for role in ("main", "descendant", "leaf"):
                group[record[f"{role}_pid"]] = record[f"{role}_start"]

        live_groups = self._validated_live_groups(identities)
        for pgid in live_groups:
            try:
                os.killpg(pgid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        try:
            process.communicate(timeout=2)
        except subprocess.TimeoutExpired:
            pass
        remaining = self._wait_for_groups(live_groups, timeout=2)
        if remaining:
            confirmed = self._validated_live_groups(identities, only=remaining)
            kill_errors = {}
            for pgid in confirmed:
                try:
                    os.killpg(pgid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                except PermissionError as error:
                    # macOS may report EPERM while the last group members are
                    # already exiting. Decide from a fresh bounded ps check.
                    kill_errors[pgid] = error
            remaining = self._wait_for_groups(confirmed, timeout=2)
            denied = remaining & set(kill_errors)
            if denied:
                raise CleanupIdentityError(f"cannot KILL owned groups: {sorted(denied)}") from kill_errors[
                    min(denied)
                ]
        if remaining:
            raise CleanupIdentityError(f"owned groups remain after KILL: {sorted(remaining)}")
        if process.poll() is None:
            raise CleanupIdentityError(f"outer make remains after cleanup: {process.pid}")

    @staticmethod
    def _read_child_identity(child_file, timeout=0.5):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if not child_file.exists():
                return None
            try:
                pgid = int(child_file.read_text(encoding="utf-8").strip())
            except ValueError as error:
                raise CleanupIdentityError(f"invalid child group identity: {child_file.parent}") from error
            start_file = child_file.with_name(".child-start")
            try:
                start = start_file.read_text(encoding="utf-8").strip()
            except FileNotFoundError:
                time.sleep(0.01)
                continue
            if not start:
                raise CleanupIdentityError(f"empty child start identity: {child_file.parent}")
            return pgid, start
        raise CleanupIdentityError(f"incomplete child identity: {child_file.parent}")

    @staticmethod
    def _process_rows():
        result = subprocess.run(
            ["ps", "-axo", "pid=,pgid=,lstart="], capture_output=True, text=True, check=False
        )
        if result.returncode != 0:
            raise CleanupIdentityError(f"cannot observe cleanup identities: {result.stderr.strip()}")
        rows = {}
        for line in result.stdout.splitlines():
            fields = line.split(maxsplit=2)
            if len(fields) == 3:
                rows[int(fields[0])] = (int(fields[1]), " ".join(fields[2].split()))
        return rows

    def _validated_live_groups(self, identities, only=None):
        rows = self._process_rows()
        selected = set(identities) if only is None else set(only)
        live_groups = set()
        for pgid in selected:
            members = {pid for pid, (current_pgid, _start) in rows.items() if current_pgid == pgid}
            if not members:
                continue
            anchors = identities[pgid]
            matching = {
                pid for pid, expected_start in anchors.items()
                if rows.get(pid) == (pgid, expected_start)
            }
            mismatched = {
                pid for pid, expected_start in anchors.items()
                if pid in rows and rows[pid] != (pgid, expected_start)
            }
            if mismatched or not matching:
                raise CleanupIdentityError(
                    f"refusing unverified group {pgid}: members={sorted(members)} mismatched={sorted(mismatched)}"
                )
            live_groups.add(pgid)
        return live_groups

    def _wait_for_groups(self, pgids, timeout):
        remaining = set(pgids)
        deadline = time.monotonic() + timeout
        while remaining and time.monotonic() < deadline:
            rows = self._process_rows()
            present = {pgid for pgid, _start in rows.values()}
            remaining &= present
            if remaining:
                time.sleep(0.02)
        return remaining


if __name__ == "__main__":
    termination_status = None

    def terminate(_signal, _frame):
        global termination_status
        termination_status = 143
        # unittest records SystemExit as a subtest error. KeyboardInterrupt is
        # deliberately propagated after the active case's cleanup unwinds.
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, terminate)
    try:
        program = unittest.main(verbosity=2, exit=False)
    except KeyboardInterrupt:
        raise SystemExit(termination_status or 130)
    raise SystemExit(not program.result.wasSuccessful())
