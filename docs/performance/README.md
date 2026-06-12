# Performance benchmark: local Raspberry Pi vs VPS

Date: 2026-06-12

## Scope

Comparison of cliostore between:
- local Raspberry Pi: `http://127.0.0.1:3001`
- VPS: `https://clio.blumer.cloud` (HTTPS + Traefik)

## Method (strict)

- Same payload generator and request counts for both targets
- Warmup before measured run (`warmup-events=500`)
- 3 measured runs in total
- For each measured run:
  - local DB reset (`/tmp/clio_perf_strict.db`) before running
  - local server restarted with same settings
  - VPS uses unique run prefixes (no overlap with previous run data)
- Batch sizes: 20, 50, 100
- Total write events per batch scenario: 5000
- Read A: single-stream, 100 requests
- Read B: prefix-recursive, 20 requests
- Parallelism: write=8, read=8
- Metrics: p50/p95/p99, throughput, error rate

## Files

- `cliostore-local-vs-vps-2026-06-12.run1.json`
- `cliostore-local-vs-vps-2026-06-12.run2.json`
- `cliostore-local-vs-vps-2026-06-12.run3.json`
- `cliostore-local-vs-vps-2026-06-12.strict-agg.json` (mean over run1-3)

## Reproduce

Build benchmark tool (from `/home/pi`):

```bash
/usr/local/go/bin/go build -o /home/pi/cliostore_perf_bench /home/pi/cliostore_perf_bench.go
```

One strict run command:

```bash
/home/pi/cliostore_perf_bench \
  --targets local=http://127.0.0.1:3001,vps=https://clio.blumer.cloud \
  --local-token <LOCAL_TOKEN> \
  --vps-token <VPS_TOKEN> \
  --runs 1 \
  --warmup-events 500 \
  --total-events 5000 \
  --batches 20,50,100 \
  --write-parallel 8 \
  --read-parallel 8 \
  --read-single-reqs 100 \
  --read-prefix-reqs 20 \
  --out /home/pi/cliostore_perf_strict_runX.json
```

Important for strict comparability:
- reset local DB before each run
- restart local server before each run
- aggregate after 3 runs (mean values)
