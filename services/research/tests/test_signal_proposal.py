from __future__ import annotations

import hashlib
import contextlib
import copy
import io
import json
import re
import tempfile
import unittest
from pathlib import Path

from omni_research.engine import canonical_hash
from omni_research.improve import run_experiment
from omni_research.signal_cli import generate_proposal, main

ROOT = Path(__file__).parents[3]


class SignalProposalTest(unittest.TestCase):
    def inputs(self, directory: Path, closes: tuple[str, ...] = ("3", "2", "1", "4")) -> tuple[Path, Path, dict]:
        research = directory / "research.csv"
        research.write_bytes((ROOT / "contracts/fixtures/strategy-market-bars.csv").read_bytes().replace(b",SMA,", b",005930,"))
        config = json.loads((ROOT / "contracts/fixtures/strategy-improvement-config.json").read_text())
        config["strategy"]["fast_windows"] = [2]
        config["strategy"]["slow_windows"] = [3]
        artifact = run_experiment(research, config)
        self.assertEqual(artifact["promotion"]["target"], "paper_candidate")
        bars = directory / "latest.csv"
        bars.write_text("bar_at,symbol,open,high,low,close,volume\n" + "".join(
            f"2026-05-{index + 1:02d}T00:00:00Z,005930,{price},{price},{price},{price},100\n"
            for index, price in enumerate(closes)
        ))
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
