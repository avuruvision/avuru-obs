#!/usr/bin/env python3
"""Offline spike for AEP 2026-09-22 (typed decisions): can a calibrated
`choice` over the six OTel severity buckets recover log lines the sensor's
OTTL cascade left at SeverityNumber 0, and at what accuracy, latency and cost?

Stdlib only. Runs from a laptop against the compose ClickHouse; the hub is
never touched. With no TYPESAFE_API_KEY it replays recorded answers so the
harness is testable before early access is granted.

  python3 jev_eval.py sample --clickhouse http://localhost:8123 --n 200 --out sample.jsonl
  python3 jev_eval.py run --sample sample.jsonl --out results.jsonl [--batch 8] [--replay fixtures/recorded.jsonl]
  python3 jev_eval.py report --results results.jsonl
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import statistics
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

API_URL = "https://api.typesafe.ai/v1/systemone"
MODEL = "jev-latest"
PRICE_USD_PER_MTOK = 0.042  # input; output is free (vendor pricing, 2026-09-22)
BUCKETS = ("TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL")
FLOORS = (0.5, 0.7, 0.9)
MAX_BODY_CHARS = 2000  # the vendor's own advice: accuracy falls with irrelevant state

CRITERIA = {
    "TRACE": "finest-grained diagnostic detail, function entry/exit, raw payloads",
    "DEBUG": "developer diagnostic detail useful only while debugging",
    "INFO": "normal operation: a request served, a job started or finished, a state change",
    "WARN": "something unexpected or degraded that the process handled and continued",
    "ERROR": "an operation failed; the process continues but the request or task did not succeed",
    "FATAL": "the process cannot continue: panic, crash, abort, unrecoverable failure",
}
INSTRUCTIONS = (
    "This is one line from an application log. Classify its severity as the "
    "application's author would have set it. Judge the event described, not "
    "the words used: 'user info updated' is INFO, 'failed to connect, retrying' is WARN."
)


def bucket_of(severity_number: int) -> str | None:
    """OTel SeverityNumber (1..24) -> bucket; 0 (unset) -> None."""
    if severity_number <= 0:
        return None
    return BUCKETS[min((severity_number - 1) // 4, len(BUCKETS) - 1)]


def line_id(body: str) -> str:
    return hashlib.sha1(body.encode("utf-8")).hexdigest()[:12]


# ── ClickHouse ────────────────────────────────────────────────────────────


def fetch_sample(clickhouse_url: str, n: int, labelled: bool, timeout: float = 30) -> list[dict]:
    """Sample bodies from otel_logs: labelled (cascade set a severity) or not."""
    where = "SeverityNumber != 0" if labelled else "SeverityNumber = 0"
    sql = (
        f"SELECT Body, SeverityNumber FROM otel_logs WHERE {where} AND Body != '' "
        f"ORDER BY rand() LIMIT {int(n)} FORMAT JSONEachRow"
    )
    url = clickhouse_url.rstrip("/") + "/?" + urllib.parse.urlencode({"query": sql})
    with urllib.request.urlopen(url, timeout=timeout) as r:
        rows = [json.loads(l) for l in r.read().decode("utf-8").splitlines() if l.strip()]
    return [
        {"id": line_id(row["Body"]), "body": row["Body"], "label": bucket_of(int(row["SeverityNumber"]))}
        for row in rows
    ]


# ── Request / response ───────────────────────────────────────────────────


def _question(target: str | None) -> dict:
    instructions = INSTRUCTIONS if target is None else f"{INSTRUCTIONS} Answer for line '{target}' only."
    return {"type": "choice", "instructions": instructions, "criteria": dict(CRITERIA)}


def build_request(lines: list[dict]) -> dict:
    """One line: state is the body. Several: state maps id -> body, one question per id."""
    if not lines:
        raise ValueError("no lines")
    if len(lines) == 1:
        return {"model": MODEL, "state": lines[0]["body"][:MAX_BODY_CHARS], "questions": {"severity": _question(None)}}
    state = {"lines": {l["id"]: l["body"][:MAX_BODY_CHARS] for l in lines}}
    return {"model": MODEL, "state": state, "questions": {l["id"]: _question(l["id"]) for l in lines}}


def parse_response(resp: dict, lines: list[dict], latency_ms: float) -> list[dict]:
    """API answers -> one decision per line. Input tokens are shared evenly across the batch."""
    answers = resp["answers"]
    tokens = int(resp.get("usage", {}).get("input_tokens", 0))
    share = tokens / len(lines) if lines else 0
    out = []
    for l in lines:
        a = answers["severity"] if len(lines) == 1 else answers[l["id"]]
        if a.get("type") != "choice":
            raise ValueError(f"unexpected answer type {a.get('type')!r} for {l['id']}")
        out.append({
            "id": l["id"], "label": l.get("label"), "choice": a["choice"],
            "probability": float(a.get("probabilities", {}).get(a["choice"], 0.0)),
            "confidence": float(a["confidence"]), "input_tokens": share, "latency_ms": latency_ms,
        })
    return out


def call_api(req: dict, key: str, timeout: float = 30) -> tuple[dict, float]:
    data = json.dumps(req).encode("utf-8")
    http = urllib.request.Request(
        API_URL, data=data, method="POST",
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
    )
    t0 = time.perf_counter()
    with urllib.request.urlopen(http, timeout=timeout) as r:
        body = json.loads(r.read().decode("utf-8"))
    return body, (time.perf_counter() - t0) * 1000


class Replay:
    """Answers from a recorded file, in the API's response shape, so the same
    parse path runs with no key. Rows: {"id","choice","probabilities","confidence","input_tokens"}."""

    def __init__(self, path: str):
        with open(path, encoding="utf-8") as f:
            self.rows = {json.loads(l)["id"]: json.loads(l) for l in f if l.strip()}

    def __call__(self, req: dict, lines: list[dict]) -> tuple[dict, float]:
        answers, tokens = {}, 0
        for l in lines:
            row = self.rows.get(l["id"])
            if row is None:
                raise KeyError(f"no recorded answer for line {l['id']}")
            key = "severity" if len(lines) == 1 else l["id"]
            answers[key] = {"type": "choice", "choice": row["choice"],
                            "probabilities": row["probabilities"], "confidence": row["confidence"]}
            tokens += int(row.get("input_tokens", 0))
        return {"model": MODEL, "answers": answers, "usage": {"input_tokens": tokens, "output_tokens": 0}}, 0.0


def run(lines: list[dict], batch: int, key: str | None, replay: Replay | None) -> list[dict]:
    if replay is None and not key:
        raise SystemExit("TYPESAFE_API_KEY is not set and no --replay file given")
    decisions = []
    for i in range(0, len(lines), max(1, batch)):
        chunk = lines[i:i + max(1, batch)]
        req = build_request(chunk)
        resp, ms = replay(req, chunk) if replay else call_api(req, key)
        decisions.extend(parse_response(resp, chunk, ms))
    return decisions


# ── Metrics ──────────────────────────────────────────────────────────────


def p95(values: list[float]) -> float:
    if not values:
        return 0.0
    s = sorted(values)
    return s[min(len(s) - 1, int(round(0.95 * (len(s) - 1))))]


def cost_per_million_lines(total_input_tokens: float, n_lines: int) -> float:
    if n_lines == 0:
        return 0.0
    # tokens/line x 1M lines x $/MTok = tokens/line x $/MTok
    return (total_input_tokens / n_lines) * PRICE_USD_PER_MTOK


def evaluate(decisions: list[dict], floors=FLOORS) -> dict:
    labelled = [d for d in decisions if d["label"]]
    unlabelled = [d for d in decisions if not d["label"]]
    per_floor = {}
    for f in floors:
        acc_l = [d for d in labelled if d["confidence"] >= f]
        correct = sum(1 for d in acc_l if d["choice"] == d["label"])
        acc_u = [d for d in unlabelled if d["confidence"] >= f]
        per_floor[str(f)] = {
            "labelled_accepted": len(acc_l),
            "accuracy": correct / len(acc_l) if acc_l else None,
            "labelled_coverage": len(acc_l) / len(labelled) if labelled else None,
            "unlabelled_recovered": len(acc_u),
            "recovery": len(acc_u) / len(unlabelled) if unlabelled else None,
        }
    tokens = sum(d["input_tokens"] for d in decisions)
    return {
        "lines": len(decisions), "labelled": len(labelled), "unlabelled": len(unlabelled),
        "input_tokens": tokens, "tokens_per_line": tokens / len(decisions) if decisions else 0,
        "usd_per_million_lines": cost_per_million_lines(tokens, len(decisions)),
        "p95_latency_ms": p95([d["latency_ms"] for d in decisions]),
        "floors": per_floor,
    }


def verdict(report: dict, floor: str = "0.7") -> dict:
    """The AEP's go / no-go table, as booleans (None where the sample cannot answer)."""
    f = report["floors"][floor]
    return {
        "accuracy>=0.95": None if f["accuracy"] is None else f["accuracy"] >= 0.95,
        "recovery>=0.30": None if f["recovery"] is None else f["recovery"] >= 0.30,
        "p95<500ms": report["p95_latency_ms"] < 500 if report["p95_latency_ms"] else None,
        "usd_per_million_lines<2": report["usd_per_million_lines"] < 2,
    }


# ── CLI ──────────────────────────────────────────────────────────────────


def _read_jsonl(path: str) -> list[dict]:
    with open(path, encoding="utf-8") as f:
        return [json.loads(l) for l in f if l.strip()]


def _write_jsonl(path: str, rows: list[dict]) -> None:
    with open(path, "w", encoding="utf-8") as f:
        for r in rows:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("sample", help="sample labelled + unlabelled bodies from ClickHouse")
    s.add_argument("--clickhouse", default="http://localhost:8123")
    s.add_argument("--n", type=int, default=200, help="lines per population")
    s.add_argument("--out", required=True)
    r = sub.add_parser("run", help="ask one choice question per line")
    r.add_argument("--sample", required=True)
    r.add_argument("--out", required=True)
    r.add_argument("--batch", type=int, default=1, help="lines per call (1 = the vendor-recommended shape)")
    r.add_argument("--replay", help="recorded answers; no key needed")
    p = sub.add_parser("report", help="metrics and the go/no-go table")
    p.add_argument("--results", required=True)
    a = ap.parse_args(argv)

    if a.cmd == "sample":
        rows = fetch_sample(a.clickhouse, a.n, True) + fetch_sample(a.clickhouse, a.n, False)
        _write_jsonl(a.out, rows)
        print(f"{len(rows)} lines -> {a.out}")
    elif a.cmd == "run":
        replay = Replay(a.replay) if a.replay else None
        decisions = run(_read_jsonl(a.sample), a.batch, os.environ.get("TYPESAFE_API_KEY"), replay)
        _write_jsonl(a.out, decisions)
        print(f"{len(decisions)} decisions -> {a.out}")
    else:
        rep = evaluate(_read_jsonl(a.results))
        rep["verdict@0.7"] = verdict(rep)
        print(json.dumps(rep, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
