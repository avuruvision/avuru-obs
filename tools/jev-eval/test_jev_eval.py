"""Unit tests for the typed-decisions spike. No network, no key: everything
runs against the synthetic fixtures under fixtures/."""
import json
import os
import unittest

import jev_eval as je

HERE = os.path.dirname(os.path.abspath(__file__))
SAMPLE = os.path.join(HERE, "fixtures", "sample.jsonl")
RECORDED = os.path.join(HERE, "fixtures", "recorded.jsonl")


def load(path):
    with open(path, encoding="utf-8") as f:
        return [json.loads(l) for l in f if l.strip()]


class BucketTest(unittest.TestCase):
    def test_otel_ranges(self):
        self.assertIsNone(je.bucket_of(0))
        for n, b in [(1, "TRACE"), (4, "TRACE"), (5, "DEBUG"), (9, "INFO"), (12, "INFO"),
                     (13, "WARN"), (17, "ERROR"), (20, "ERROR"), (21, "FATAL"), (24, "FATAL")]:
            self.assertEqual(je.bucket_of(n), b, n)

    def test_out_of_range_clamps_to_fatal(self):
        self.assertEqual(je.bucket_of(99), "FATAL")


class RequestShapeTest(unittest.TestCase):
    def test_single_line_state_is_the_body(self):
        req = je.build_request([{"id": "a", "body": "x " * 3000, "label": None}])
        self.assertEqual(req["model"], je.MODEL)
        self.assertIsInstance(req["state"], str)
        self.assertLessEqual(len(req["state"]), je.MAX_BODY_CHARS)
        q = req["questions"]["severity"]
        self.assertEqual(q["type"], "choice")
        self.assertEqual(tuple(q["criteria"]), je.BUCKETS)
        self.assertLessEqual(len(q["criteria"]), 255)

    def test_batched_state_has_one_question_per_line(self):
        lines = [{"id": "a", "body": "one"}, {"id": "b", "body": "two"}]
        req = je.build_request(lines)
        self.assertEqual(req["state"], {"lines": {"a": "one", "b": "two"}})
        self.assertEqual(set(req["questions"]), {"a", "b"})
        self.assertIn("'b'", req["questions"]["b"]["instructions"])

    def test_empty_rejected(self):
        with self.assertRaises(ValueError):
            je.build_request([])


class ParseTest(unittest.TestCase):
    def test_tokens_shared_across_batch(self):
        lines = [{"id": "a", "body": "one", "label": "INFO"}, {"id": "b", "body": "two", "label": None}]
        resp = {"answers": {
            "a": {"type": "choice", "choice": "INFO", "probabilities": {"INFO": 0.9}, "confidence": 0.8},
            "b": {"type": "choice", "choice": "WARN", "probabilities": {"WARN": 0.7}, "confidence": 0.6},
        }, "usage": {"input_tokens": 50, "output_tokens": 0}}
        d = je.parse_response(resp, lines, 120.0)
        self.assertEqual([x["input_tokens"] for x in d], [25, 25])
        self.assertEqual(d[0]["probability"], 0.9)
        self.assertEqual(d[1]["label"], None)

    def test_wrong_type_rejected(self):
        resp = {"answers": {"severity": {"type": "noul", "noul": 0.5}}, "usage": {}}
        with self.assertRaises(ValueError):
            je.parse_response(resp, [{"id": "a", "body": "x"}], 1.0)


class MetricsTest(unittest.TestCase):
    def test_cost_is_tokens_per_line_times_price(self):
        self.assertAlmostEqual(je.cost_per_million_lines(2500, 100), 25 * je.PRICE_USD_PER_MTOK)
        self.assertEqual(je.cost_per_million_lines(0, 0), 0.0)

    def test_p95(self):
        self.assertEqual(je.p95([]), 0.0)
        self.assertEqual(je.p95(list(range(1, 101))), 95)

    def test_floors_split_labelled_and_unlabelled(self):
        d = [
            {"label": "INFO", "choice": "INFO", "confidence": 0.9, "input_tokens": 10, "latency_ms": 100},
            {"label": "WARN", "choice": "ERROR", "confidence": 0.8, "input_tokens": 10, "latency_ms": 100},
            {"label": "INFO", "choice": "INFO", "confidence": 0.6, "input_tokens": 10, "latency_ms": 100},
            {"label": None, "choice": "ERROR", "confidence": 0.95, "input_tokens": 10, "latency_ms": 100},
            {"label": None, "choice": "INFO", "confidence": 0.4, "input_tokens": 10, "latency_ms": 100},
        ]
        r = je.evaluate(d, floors=(0.7,))
        f = r["floors"]["0.7"]
        self.assertEqual(f["labelled_accepted"], 2)
        self.assertEqual(f["accuracy"], 0.5)
        self.assertAlmostEqual(f["labelled_coverage"], 2 / 3)
        self.assertEqual(f["unlabelled_recovered"], 1)
        self.assertEqual(f["recovery"], 0.5)
        self.assertEqual(r["usd_per_million_lines"], 10 * je.PRICE_USD_PER_MTOK)


class ReplayEndToEndTest(unittest.TestCase):
    """The recorded fixture is SYNTHETIC: it exercises the harness, not the model."""

    def test_single_and_batched_agree(self):
        lines = load(SAMPLE)
        replay = je.Replay(RECORDED)
        one = je.run(lines, 1, None, replay)
        eight = je.run(lines, 8, None, replay)
        self.assertEqual([d["choice"] for d in one], [d["choice"] for d in eight])
        self.assertEqual(len(one), 24)

    def test_report_matches_fixture_design(self):
        r = je.evaluate(je.run(load(SAMPLE), 1, None, je.Replay(RECORDED)))
        self.assertEqual((r["labelled"], r["unlabelled"]), (16, 8))
        f = r["floors"]["0.7"]
        self.assertEqual(f["labelled_accepted"], 15)          # one low-confidence line dropped
        self.assertAlmostEqual(f["accuracy"], 14 / 15)          # one confident mistake
        self.assertEqual(f["unlabelled_recovered"], 6)
        self.assertEqual(f["recovery"], 0.75)
        v = je.verdict(r)
        self.assertFalse(v["accuracy>=0.95"])
        self.assertTrue(v["recovery>=0.30"])
        self.assertTrue(v["usd_per_million_lines<2"])
        self.assertIsNone(v["p95<500ms"])                       # replay has no latency

    def test_no_key_and_no_replay_is_a_clear_error(self):
        with self.assertRaises(SystemExit):
            je.run(load(SAMPLE)[:1], 1, None, None)

    def test_missing_recording_is_named(self):
        with self.assertRaises(KeyError):
            je.run([{"id": "nope", "body": "?", "label": None}], 1, None, je.Replay(RECORDED))


if __name__ == "__main__":
    unittest.main()
