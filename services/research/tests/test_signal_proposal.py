from __future__ import annotations

import hashlib
import contextlib
import copy
import io
import json
import os
import re
import selectors
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path

from omni_research.engine import canonical_hash
from omni_research.improve import run_experiment
from omni_research.signal_cli import generate_paper_input_bundle, generate_proposal, generate_proposal_from_bytes, main

ROOT = Path(__file__).parents[3]


class SignalProposalTest(unittest.TestCase):
    def test_cli_closed_output_exits_without_shutdown_traceback(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            bars, research, artifact = self.inputs(directory)
            artifact_path = directory / 'artifact.json'
            artifact_path.write_text(json.dumps(artifact))
            args = ['--bars', str(bars), '--research-bars', str(research), '--artifact', str(artifact_path), '--watch']
            read_fd, write_fd = os.pipe()
            os.close(read_fd)
            try:
                result = subprocess.run([sys.executable, '-B', '-m', 'omni_research.signal_cli', *args],
                                        stdout=write_fd, stderr=subprocess.PIPE, text=True, timeout=3)
            finally:
                os.close(write_fd)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(result.stderr, 'Paper signal proposal output is closed; producer stopped.\n')

    def test_cli_rejects_nonregular_and_oversized_inputs_without_waiting(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            bars, research, artifact = self.inputs(directory)
            artifact_path = directory / 'artifact.json'
            artifact_path.write_text(json.dumps(artifact))
            oversized = directory / 'oversized'
            oversized.write_bytes(b' ' * 1_048_577)
            large_field = directory / 'large-field'
            large_field.write_bytes(b'x' * 262_144)
            fifo = directory / 'fifo'
            os.mkfifo(fifo)
            link = directory / 'link'
            link.symlink_to(fifo)
            for flag in ('--bars', '--research-bars', '--artifact'):
                for invalid in (directory, oversized, large_field, fifo, link):
                    with self.subTest(flag=flag, kind=invalid.name):
                        inputs = {'--bars': bars, '--research-bars': research, '--artifact': artifact_path}
                        inputs[flag] = invalid
                        args = [item for key, path in inputs.items() for item in (key, str(path))]
                        result = subprocess.run([sys.executable, '-B', '-m', 'omni_research.signal_cli', *args, '--watch'],
                                                capture_output=True, text=True, timeout=3)
                        self.assertEqual(result.returncode, 1)
                        self.assertEqual(result.stdout, '')
                        self.assertEqual(result.stderr, 'Paper signal proposal input is invalid; no proposal was produced.\n')

    def test_watch_real_signals_and_invalid_update_leave_no_owned_resources(self) -> None:
        for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGKILL, None):
            with self.subTest(signal=sig), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                bars, research, artifact = self.inputs(directory)
                artifact_path = directory / 'artifact.json'
                artifact_path.write_text(json.dumps(artifact))
                args = ['--bars', str(bars), '--research-bars', str(research), '--artifact', str(artifact_path), '--watch']
                before = {p.name: p.read_bytes() for p in directory.iterdir()}
                child = subprocess.Popen([sys.executable, '-B', '-m', 'omni_research.signal_cli', *args],
                                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                try:
                    with selectors.DefaultSelector() as ready:
                        ready.register(child.stdout, selectors.EVENT_READ)
                        self.assertTrue(ready.select(timeout=5), 'watcher did not flush its first proposal')
                    self.assertEqual(json.loads(child.stdout.readline())['signal'], 'golden_cross')
                    if sig is None:
                        bars.write_text('DO_NOT_ECHO')
                        before[bars.name] = bars.read_bytes()
                    else:
                        child.send_signal(sig)
                    out, err = child.communicate(timeout=5)
                    self.assertEqual(child.returncode, 1 if sig is None else 130 if sig == signal.SIGINT else -sig)
                    self.assertEqual(out, '')
                    self.assertEqual(err, 'Paper signal proposal input is invalid; no proposal was produced.\n' if sig is None else '')
                finally:
                    if child.poll() is None:
                        child.kill()
                    child.communicate(timeout=5)
                self.assertEqual(before, {p.name: p.read_bytes() for p in directory.iterdir()})

    def test_watch_rejects_rewritten_anchor_regression_and_changed_research(self) -> None:
        for change in ('rewrite', 'regress', 'artifact', 'research', 'invalid'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                bars, research, artifact = self.inputs(directory)
                artifact_path = directory / 'artifact.json'
                artifact_path.write_text(json.dumps(artifact))
                args = ['--bars', str(bars), '--research-bars', str(research), '--artifact', str(artifact_path), '--watch']
                ticks = 0

                def wait(_: float) -> None:
                    nonlocal ticks
                    ticks += 1
                    if ticks > 1:
                        raise KeyboardInterrupt  # bound a regressed implementation
                    if change == 'rewrite':
                        bars.write_text(bars.read_text() + '\n')
                    elif change == 'regress':
                        bars.write_text(bars.read_text().replace('2026-05-04T00:00:00Z', '2026-05-03T12:00:00Z'))
                    elif change == 'invalid':
                        bars.write_text('DO_NOT_ECHO')
                    else:
                        path = artifact_path if change == 'artifact' else research
                        path.write_bytes(path.read_bytes() + b'\n')

                stdout, stderr = io.StringIO(), io.StringIO()
                with patch('time.sleep', side_effect=wait), contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                    self.assertEqual(main(args), 1)
                self.assertEqual(len(stdout.getvalue().splitlines()), 1)
                self.assertEqual(stderr.getvalue(), 'Paper signal proposal input is invalid; no proposal was produced.\n')

    def test_watch_emits_only_changed_snapshot_and_stops_without_writes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            bars, research, artifact = self.inputs(directory)
            artifact_path = directory / 'artifact.json'
            artifact_path.write_text(json.dumps(artifact))
            args = ['--bars', str(bars), '--research-bars', str(research), '--artifact', str(artifact_path), '--watch']
            ticks = 0

            def wait(_: float) -> None:
                nonlocal ticks
                ticks += 1
                if ticks == 2:
                    bars.write_text(bars.read_text() + '2026-05-05T00:00:00Z,005930,4,4,4,4,100\n')
                if ticks == 3:
                    raise KeyboardInterrupt

            stdout, stderr = io.StringIO(), io.StringIO()
            with patch('time.sleep', side_effect=wait), contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                self.assertEqual(main(args), 130)
            proposals = [json.loads(line) for line in stdout.getvalue().splitlines()]
            self.assertEqual([(p['data_as_of'], p['signal'], p['target_quantity']) for p in proposals], [
                ('2026-05-04T00:00:00Z', 'golden_cross', '10'),
                ('2026-05-05T00:00:00Z', 'none', None),
            ])
            self.assertEqual(stderr.getvalue(), '')
            self.assertEqual({p.name for p in directory.iterdir()}, {'latest.csv', 'research.csv', 'artifact.json'})

    def test_cli_bundle_round_trips_exact_crlf_bytes_from_captured_reads(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            bars, research, artifact = self.inputs(directory, line_ending=b'\r\n')
            artifact_path = directory / 'artifact.json'
            artifact_path.write_text(json.dumps(artifact))
            captured_bars, captured_research = bars.read_bytes(), research.read_bytes()
            original_read = __import__('omni_research.signal_cli', fromlist=['read_signal_input']).read_signal_input

            def read_then_mutate(path: Path) -> bytes:
                raw = original_read(path)
                if path == bars:
                    bars.write_bytes(b'MUTATED')
                    research.write_bytes(b'MUTATED')
                return raw

            stdout, stderr = io.StringIO(), io.StringIO()
            args = ['--bars', str(bars), '--research-bars', str(research), '--artifact', str(artifact_path), '--bundle']
            with patch('omni_research.signal_cli.read_signal_input', side_effect=read_then_mutate), contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                self.assertEqual(main(args), 0)
            proposal = generate_proposal_from_bytes(captured_bars, captured_research, artifact)
            self.assertEqual(json.loads(stdout.getvalue()), generate_paper_input_bundle(proposal, captured_bars, captured_research))
            bundle = json.loads(stdout.getvalue())
            self.assertEqual(bundle['bars_csv'].encode('utf-8'), captured_bars)
            self.assertEqual(bundle['research_csv'].encode('utf-8'), captured_research)
            self.assertEqual(bundle['proposal']['input_sha256'], hashlib.sha256(captured_bars).hexdigest())
            self.assertEqual(stderr.getvalue(), '')

    def test_bundle_watch_deduplicates_on_proposal(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            bars, research, artifact = self.inputs(directory)
            artifact_path = directory / 'artifact.json'
            artifact_path.write_text(json.dumps(artifact))
            ticks = 0

            def wait(_: float) -> None:
                nonlocal ticks
                ticks += 1
                if ticks == 2:
                    raise KeyboardInterrupt

            stdout, stderr = io.StringIO(), io.StringIO()
            args = ['--bars', str(bars), '--research-bars', str(research), '--artifact', str(artifact_path), '--bundle', '--watch']
            with patch('time.sleep', side_effect=wait), contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                self.assertEqual(main(args), 130)
            self.assertEqual(len(stdout.getvalue().splitlines()), 1)
            self.assertEqual(stderr.getvalue(), '')

    def test_bundle_schema_agrees_with_producer_and_aggregate_cap(self) -> None:
        schema = json.loads((ROOT / 'contracts/paper-input-bundle.schema.json').read_text())
        with tempfile.TemporaryDirectory() as temporary:
            bars, research, artifact = self.inputs(Path(temporary))
            proposal = generate_proposal(bars, research, artifact)
            bundle = generate_paper_input_bundle(proposal, bars.read_bytes(), research.read_bytes())
            self.assertIs(schema['additionalProperties'], False)
            self.assertEqual(set(schema['required']), set(bundle))
            self.assertEqual(set(schema['properties']), set(bundle))
            self.assertEqual(schema['properties']['proposal']['$ref'], 'paper-signal-proposal.schema.json')
            self.assertEqual(schema['properties']['bars_csv']['maxLength'], 1_048_576)
            self.assertEqual(schema['properties']['research_csv']['maxLength'], 1_048_576)
            with self.assertRaisesRegex(ValueError, 'bundle exceeds 4 MiB'):
                generate_paper_input_bundle(proposal, b'"' * 1_048_576, b'"' * 1_048_576)
            empty = {**bundle, 'bars_csv': '', 'research_csv': ''}
            overhead = len(json.dumps(empty, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode('utf-8'))
            remaining = 4 * 1_048_576 - 1 - overhead - 2 * 1_048_576
            bars_bytes = b'"' * 1_048_576
            research_bytes = b'"' * (remaining // 2) + b'x' * (remaining % 2)
            boundary = generate_paper_input_bundle(proposal, bars_bytes, research_bytes)
            encoded = json.dumps(boundary, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode('utf-8')
            self.assertEqual(len(encoded) + 1, 4 * 1_048_576)
            with self.assertRaisesRegex(ValueError, 'bundle exceeds 4 MiB'):
                generate_paper_input_bundle(proposal, bars_bytes, research_bytes + b'x')

    def inputs(self, directory: Path, closes: tuple[str, ...] = ("3", "2", "1", "4"), line_ending: bytes = b'\n') -> tuple[Path, Path, dict]:
        research = directory / "research.csv"
        research.write_bytes((ROOT / "contracts/fixtures/strategy-market-bars.csv").read_bytes().replace(b",SMA,", b",005930,").replace(b'\n', line_ending))
        config = json.loads((ROOT / "contracts/fixtures/strategy-improvement-config.json").read_text())
        config["strategy"]["fast_windows"] = [2]
        config["strategy"]["slow_windows"] = [3]
        artifact = run_experiment(research, config)
        self.assertEqual(artifact["promotion"]["target"], "paper_candidate")
        bars = directory / "latest.csv"
        bars.write_bytes(("bar_at,symbol,open,high,low,close,volume\n" + "".join(
            f"2026-05-{index + 1:02d}T00:00:00Z,005930,{price},{price},{price},{price},100\n"
            for index, price in enumerate(closes)
        )).replace('\n', line_ending.decode()).encode())
        return bars, research, artifact

    def test_proposal_binds_actual_inputs_without_order_authority(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            bars, research, artifact = self.inputs(Path(temporary))
            proposal = generate_proposal(bars, research, artifact)
            self.assertEqual(proposal, generate_proposal(bars, research, artifact))
            self.assertEqual(proposal["signal"], "golden_cross")
            self.assertEqual(proposal["target_quantity"], "10")
            self.assertEqual(proposal["symbol"], "005930")
            self.assertEqual(proposal["data_as_of"], "2026-05-04T00:00:00Z")
            self.assertEqual(proposal["mode"], "paper_proposal_only")
            self.assertEqual(proposal["strategy_result_sha256"], artifact["result_sha256"])
            self.assertEqual(proposal["strategy_parameter_sha256"], artifact["manifest"]["strategy"]["parameter_hash"])
            self.assertEqual(proposal["input_sha256"], hashlib.sha256(bars.read_bytes()).hexdigest())
            self.assertEqual(proposal["proposal_sha256"], canonical_hash({key: value for key, value in proposal.items() if key != "proposal_sha256"}))
            self.assertEqual(set(proposal), {"schema_version", "mode", "strategy_result_sha256", "strategy_parameter_sha256", "input_sha256", "symbol", "data_as_of", "signal", "target_quantity", "proposal_sha256"})

    def test_exit_target_and_no_signal_are_distinct(self) -> None:
        for closes, signal, target in ((('2', '3', '4', '1'), 'death_cross', '0'), (('1', '1', '1', '1'), 'none', None)):
            with self.subTest(signal=signal), tempfile.TemporaryDirectory() as temporary:
                bars, research, artifact = self.inputs(Path(temporary), closes)
                proposal = generate_proposal(bars, research, artifact)
                self.assertEqual((proposal['signal'], proposal['target_quantity']), (signal, target))

    def test_schema_fields_and_target_branches_match_producer(self) -> None:
        schema = json.loads((ROOT / 'contracts/paper-signal-proposal.schema.json').read_text())
        with tempfile.TemporaryDirectory() as temporary:
            bars, research, artifact = self.inputs(Path(temporary))
            proposal = generate_proposal(bars, research, artifact)
            self.assertIs(schema['additionalProperties'], False)
            self.assertEqual(set(schema['required']), set(proposal))
            self.assertEqual(set(schema['properties']), set(proposal))
            for field, definition in schema['properties'].items():
                if '$ref' in definition:
                    definition = schema['$defs'][definition['$ref'].rsplit('/', 1)[1]]
                if 'pattern' in definition:
                    self.assertIsNotNone(re.fullmatch(definition['pattern'], proposal[field]))
                if 'const' in definition:
                    self.assertEqual(proposal[field], definition['const'])
            targets = {branch['properties']['signal']['const']: branch['properties']['target_quantity'] for branch in schema['oneOf']}
            self.assertEqual(targets['none'], {'type': 'null'})
            self.assertEqual(targets['death_cross'], {'const': '0'})
            for value in ('1', '10', '9' * 64):
                self.assertIsNotNone(re.fullmatch(targets['golden_cross']['pattern'], value))
            for value in ('0', '01', '-1', '0.1', '9' * 65):
                self.assertIsNone(re.fullmatch(targets['golden_cross']['pattern'], value))

    def test_rejects_rehashed_unsupported_or_unqualified_artifact(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            bars, research, original = self.inputs(Path(temporary))
            mutations = (
                lambda a: a.update(mode='live'),
                lambda a: a['manifest']['strategy'].update(version='2.0.0'),
                lambda a: a['manifest']['strategy']['parameters'].update(quantity='0.5'),
                lambda a: a['manifest']['strategy']['parameters'].update(fast_window=True),
                lambda a: a['manifest']['strategy']['parameters'].update(slow_window=2),
                lambda a: a['promotion'].update(target='no_promotion'),
                lambda a: a['promotion'].update(walk_forward_gate_passed=1),
                lambda a: a['promotion'].update(failed_gates=['drawdown']),
                lambda a: a['execution'].update(fill_price='bar_close'),
                lambda a: a['manifest']['data'].update(input_sha256='0' * 64),
                lambda a: a['challenger'].update(parameters={'fast_window': 1, 'slow_window': 3}),
            )
            for index, mutate in enumerate(mutations):
                with self.subTest(index=index):
                    artifact = copy.deepcopy(original)
                    mutate(artifact)
                    strategy = artifact['manifest']['strategy']
                    strategy['parameter_hash'] = canonical_hash(strategy['parameters'])
                    artifact['result_sha256'] = canonical_hash({k: v for k, v in artifact.items() if k != 'result_sha256'})
                    with self.assertRaises(ValueError):
                        generate_proposal(bars, research, artifact)
            original['experiment_id'] = 'tampered'
            with self.assertRaises(ValueError):
                generate_proposal(bars, research, original)

    def test_rejects_unbound_symbol_history_and_ambiguous_csv(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            bars, research, artifact = self.inputs(Path(temporary))
            raw = bars.read_text()
            mutations = (
                raw.replace('005930', '000660'),
                raw.replace('005930', '000660', 1),
                '\n'.join(raw.splitlines()[:-1]) + '\n',
                raw.replace('2026-05-', '2020-05-'),
                raw.replace('00:00:00Z', '00:00:00+00:00'),
                raw.replace('bar_at,symbol,', 'bar_at,symbol,symbol,').replace(',005930,', ',OTHER,005930,'),
            )
            for index, changed in enumerate(mutations):
                with self.subTest(index=index):
                    bars.write_text(changed)
                    with self.assertRaises(ValueError):
                        generate_proposal(bars, research, artifact)
            bars.write_text(raw)
            research.write_bytes(research.read_bytes() + b'\n')
            with self.assertRaises(ValueError):
                generate_proposal(bars, research, artifact)

    def test_cli_success_and_redacted_failure_leave_inputs_unchanged(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            bars, research, artifact = self.inputs(directory)
            artifact_path = directory / 'artifact.json'
            artifact_path.write_text(json.dumps(artifact))
            args = ['--bars', str(bars), '--research-bars', str(research), '--artifact', str(artifact_path)]
            before = {p.name: p.read_bytes() for p in directory.iterdir()}
            stdout, stderr = io.StringIO(), io.StringIO()
            with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                self.assertEqual(main(args), 0)
            self.assertEqual(json.loads(stdout.getvalue()), generate_proposal(bars, research, artifact))
            self.assertEqual(stderr.getvalue(), '')
            self.assertEqual(before, {p.name: p.read_bytes() for p in directory.iterdir()})
            invalid = ('{"secret":"DO_NOT_ECHO","secret":1}', '{"x":NaN}', '{"x":1.0}', '[]', '{', '{"x":' + '[' * 1100 + '0' + ']' * 1100 + '}')
            for raw in invalid:
                artifact_path.write_text(raw)
                stdout, stderr = io.StringIO(), io.StringIO()
                with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                    self.assertEqual(main(args), 1)
                self.assertEqual(stdout.getvalue(), '')
                self.assertEqual(stderr.getvalue(), 'Paper signal proposal input is invalid; no proposal was produced.\n')


if __name__ == "__main__":
    unittest.main()
