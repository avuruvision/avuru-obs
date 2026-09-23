# jev-eval — spike for [AEP 2026-09-22, typed decisions](../../design/2026-09-22-typed-decisions-jev.md)

Measures whether a calibrated `choice` over the six OTel severity buckets can
recover log lines the sensor's OTTL cascade left at `SeverityNumber = 0`, and
at what accuracy, latency and cost. Stdlib Python only, no SDK. It runs from a
laptop against the compose ClickHouse and never touches the hub, the gateway
or the sensor.

```sh
# 1. sample 200 labelled + 200 unlabelled bodies from the compose stack
python3 jev_eval.py sample --clickhouse http://localhost:8123 --n 200 --out sample.jsonl

# 2. ask the model (needs early access); --batch 8 packs eight lines per call
TYPESAFE_API_KEY=… python3 jev_eval.py run --sample sample.jsonl --out results.jsonl

# 3. metrics and the AEP's go/no-go table at confidence 0.7
python3 jev_eval.py report --results results.jsonl
```

Without a key, `--replay fixtures/recorded.jsonl` replays answers in the
API's response shape so the harness itself can be exercised:

```sh
python3 jev_eval.py run --sample fixtures/sample.jsonl --out /tmp/r.jsonl --replay fixtures/recorded.jsonl
python3 -m unittest discover -s tools/jev-eval   # from the repo root
```

**`fixtures/recorded.jsonl` is synthetic.** It was written by hand in the
documented response shape to test the harness; it says nothing about the
model. Replace it with a real recording after the first live run.

What leaves the laptop on a live run: the log body, truncated to 2000
characters, nothing else. That is the honest cost the AEP weighs.
