#!/usr/bin/env python3

import argparse
import json
import statistics
import sys
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed


def _post_json(url: str, payload: dict, timeout_s: float):
    body = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=body,
        headers={"Content-Type": "application/json", "Accept": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout_s) as resp:
        raw = resp.read().decode("utf-8")
        return resp.status, json.loads(raw)


def _worker(i: int, url: str, name: str, timeout_s: float):
    started = time.perf_counter()
    try:
        status, res = _post_json(url, {"name": name}, timeout_s)
        elapsed_ms = (time.perf_counter() - started) * 1000.0

        if status != 200:
            return False, elapsed_ms, f"http_status={status}"

        if not isinstance(res, dict):
            return False, elapsed_ms, "bad_json"

        # Validate expected fields (do not enforce exact stdout content beyond string types)
        stdout = res.get("stdout")
        stderr = res.get("stderr")
        cost = res.get("cost")
        trace_id = res.get("trace_id")

        if not isinstance(stdout, str) or not isinstance(stderr, str):
            return False, elapsed_ms, "missing_stdout_stderr"
        if not isinstance(cost, str) or not cost:
            return False, elapsed_ms, "missing_cost"
        if not isinstance(trace_id, str) or not trace_id:
            return False, elapsed_ms, "missing_trace_id"

        return True, elapsed_ms, None

    except urllib.error.HTTPError as e:
        elapsed_ms = (time.perf_counter() - started) * 1000.0
        try:
            body = e.read(512)
            if isinstance(body, bytes):
                body = body.decode("utf-8", errors="replace")
            body = (body or "").replace("\n", " ").strip()
        except Exception:
            body = ""
        reason = f"HTTPError:{getattr(e, 'code', 'unknown')}"
        if body:
            reason = f"{reason}:{body}"
        return False, elapsed_ms, reason

    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as e:
        elapsed_ms = (time.perf_counter() - started) * 1000.0
        return False, elapsed_ms, type(e).__name__
    except Exception as e:
        elapsed_ms = (time.perf_counter() - started) * 1000.0
        return False, elapsed_ms, f"{type(e).__name__}: {e}"


def _percentile(sorted_values, p: float):
    if not sorted_values:
        return None
    if p <= 0:
        return sorted_values[0]
    if p >= 100:
        return sorted_values[-1]
    k = (len(sorted_values) - 1) * (p / 100.0)
    f = int(k)
    c = min(f + 1, len(sorted_values) - 1)
    if f == c:
        return sorted_values[f]
    d0 = sorted_values[f] * (c - k)
    d1 = sorted_values[c] * (k - f)
    return d0 + d1


def main():
    parser = argparse.ArgumentParser(description="Stress test booster /run endpoint")
    parser.add_argument(
        "--url",
        default="http://127.0.0.1:2678/run",
        help="POST /run URL (default: http://127.0.0.1:2678/run)",
    )
    parser.add_argument("--name", default="Sparky", help="WASM NAME env passed by booster")
    parser.add_argument("--total", type=int, default=1_000_000, help="Total requests (default: 10000)")
    parser.add_argument(
        "--concurrency",
        type=int,
        default=64,
        help="Concurrent workers (default: 64)",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=10.0,
        help="Per-request timeout seconds (default: 10)",
    )
    parser.add_argument(
        "--progress-every",
        type=int,
        default=500,
        help="Print progress every N completed requests (default: 500)",
    )

    args = parser.parse_args()

    total = args.total
    concurrency = max(1, args.concurrency)

    print(f"Target: {args.url}")
    print(f"Total: {total}")
    print(f"Concurrency: {concurrency}")
    print(f"Timeout: {args.timeout}s")

    ok = 0
    fail = 0
    lat_ms = []
    fail_reasons = {}

    wall_start = time.perf_counter()

    with ThreadPoolExecutor(max_workers=concurrency) as ex:
        futures = [
            ex.submit(_worker, i, args.url, args.name, args.timeout)
            for i in range(total)
        ]

        done = 0
        for fut in as_completed(futures):
            success, elapsed_ms, reason = fut.result()
            done += 1
            lat_ms.append(elapsed_ms)

            if success:
                ok += 1
            else:
                fail += 1
                fail_reasons[reason] = fail_reasons.get(reason, 0) + 1

            if args.progress_every > 0 and done % args.progress_every == 0:
                so_far = time.perf_counter() - wall_start
                rps = done / so_far if so_far > 0 else 0.0
                print(f"progress: {done}/{total} (ok={ok}, fail={fail}) rps={rps:.1f}")

    wall_s = time.perf_counter() - wall_start
    rps = total / wall_s if wall_s > 0 else 0.0

    lat_ms_sorted = sorted(lat_ms)

    p50 = _percentile(lat_ms_sorted, 50)
    p90 = _percentile(lat_ms_sorted, 90)
    p99 = _percentile(lat_ms_sorted, 99)
    p999 = _percentile(lat_ms_sorted, 99.9)

    print("\n=== Summary ===")
    print(f"ok: {ok}")
    print(f"fail: {fail}")
    print(f"success_rate: {ok / total * 100.0:.2f}%")
    print(f"wall_time_s: {wall_s:.3f}")
    print(f"throughput_rps: {rps:.1f}")

    if lat_ms_sorted:
        print("\n=== Latency (ms, client-side) ===")
        print(f"min: {lat_ms_sorted[0]:.2f}")
        print(f"mean: {statistics.mean(lat_ms_sorted):.2f}")
        print(f"p50: {p50:.2f}")
        print(f"p90: {p90:.2f}")
        print(f"p99: {p99:.2f}")
        print(f"p99.9: {p999:.2f}")
        print(f"max: {lat_ms_sorted[-1]:.2f}")

    if fail_reasons:
        print("\n=== Fail reasons ===")
        for k in sorted(fail_reasons.keys()):
            print(f"{k}: {fail_reasons[k]}")

    return 0 if fail == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
