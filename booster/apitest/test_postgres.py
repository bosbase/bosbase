#!/usr/bin/env python3

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request


def post_json(url: str, payload: dict, timeout_s: float) -> dict:
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=data,
        headers={"content-type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout_s) as resp:
        body = resp.read().decode("utf-8", errors="replace")
        return json.loads(body)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="API test for booster Postgres WASM imports via /run endpoint"
    )
    parser.add_argument("--url", default="http://127.0.0.1:2678/run")
    parser.add_argument("--name", default="Sparky")
    parser.add_argument("--timeout", type=float, default=15.0)
    parser.add_argument("--retries", type=int, default=15)
    parser.add_argument("--retry-delay", type=float, default=0.25)
    args = parser.parse_args()

    if "POSTGRES_URL" not in os.environ and "SASSPB_POSTGRES_URL" not in os.environ:
        print(
            "WARN: POSTGRES_URL (or SASSPB_POSTGRES_URL) is not set in this shell. "
            "That's OK as long as the running booster process has it set.",
            file=sys.stderr,
        )

    last_err: Exception | None = None
    for _ in range(max(args.retries, 1)):
        try:
            res = post_json(args.url, {"name": args.name}, args.timeout)
            break
        except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as e:
            last_err = e
            time.sleep(args.retry_delay)
    else:
        print(f"ERROR: failed to call {args.url}: {last_err}", file=sys.stderr)
        return 3

    if not isinstance(res, dict):
        print(f"ERROR: response is not a JSON object: {res!r}", file=sys.stderr)
        return 4

    stdout = res.get("stdout")
    stderr = res.get("stderr")
    trace_id = res.get("trace_id")
    cost = res.get("cost")

    if not isinstance(stdout, str) or not isinstance(stderr, str):
        print(
            f"ERROR: expected keys 'stdout' and 'stderr' as strings, got: {res!r}",
            file=sys.stderr,
        )
        return 5

    if not isinstance(trace_id, str) or not trace_id:
        print(f"ERROR: expected key 'trace_id' as non-empty string, got: {res!r}", file=sys.stderr)
        return 5

    if not isinstance(cost, str) or not cost:
        print(f"ERROR: expected key 'cost' as non-empty string, got: {res!r}", file=sys.stderr)
        return 5

    # Behavioral checks: the WASM demo prints these markers.
    required_markers = [
        "pg_exec create rc=",
        "pg_exec insert rc=",
        "pg_exec update rc=",
        "pg_query ",
        "pg_exec delete rc=",
    ]
    missing = [m for m in required_markers if m not in stdout]
    if missing:
        print("ERROR: missing expected stdout markers", file=sys.stderr)
        print(f"missing={missing!r}")
        print(f"stdout={stdout!r}")
        print(f"stderr={stderr!r}")
        return 6

    # Should show the provided name somewhere (the demo prints NAME).
    if args.name not in stdout:
        print("ERROR: expected name to appear in stdout", file=sys.stderr)
        print(f"name={args.name!r}")
        print(f"stdout={stdout!r}")
        return 7

    print("OK")
    print(f"trace_id: {trace_id}")
    print(f"cost: {cost}")
    print("stdout:\n" + stdout)
    if stderr:
        print("stderr:\n" + stderr)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
