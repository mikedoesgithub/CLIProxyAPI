# Local balance measurements

Measured 2026-10-27 on this development host: Intel Core i7-12700H, Linux x86-64,
32 GiB installed RAM. The target Dell R330 was not available. These are warm
in-process classifier timings, with varying background development load; they
exclude Go, HTTP, provider execution and cold startup. They are a small smoke
benchmark, not a statistically controlled performance or accuracy study.

Pinned s1 revision: `572c5b6cdb53b77fbb26ed33568ebbc5b71c69c0`.
NumPy 2.4.4, ONNX Runtime 1.24.4, Tokenizers 0.22.2. CPU provider, sequential
execution, two intra-op threads, one inter-op thread, no spinning, one BLAS thread.
Default service input cap is 128 tokens, longest-first truncation.

| Input profile | Samples | Warm p50 | Warm p95 | Process peak RSS |
| --- | ---: | ---: | ---: | ---: |
| Seven short example requests, repeated | 70 | 19.2 ms | 30.4 ms | 512.7 MiB |
| Same requests repeated to exceed token cap | 35 | 99.2 ms | 168.9 ms | 512.1 MiB |

Commands from the repository root, with the setup script's environment:

```bash
.balance/venv/bin/python tools/balance/benchmark.py --model-dir .balance/model --iterations 70
.balance/venv/bin/python tools/balance/benchmark.py --model-dir .balance/model --long --iterations 35
```

The default proxy routing budget is 100 ms (maximum configurable 250 ms). A
long/slow decision can therefore use the fallback instead of its classifier
prediction. Registry/capability checks and scheduling add overhead beyond that
preflight budget. When busy, both admission gates reject immediately; requests do
not build an inference queue. Benchmark the whole request path on the R330 before
choosing a larger budget or token cap.

An earlier 512-token stress run measured approximately 368 ms p50 / 405 ms p95.
That is why the smaller cap is the default. Truncation can reduce accuracy; the
model author's 512-token evaluation does not validate the 128-token setup.
The stock broad classifier also labeled a browser-shopping example as coding
with only 0.52 confidence. The browser phrase rule addresses that particular case;
it is not evidence of general specialty classification accuracy.

Disk weights/tokenizer total about 217 MB decimal (208 MiB). Peak RSS includes
Python, tokenizer, ONNX loading/optimization, weights and inference buffers; it
is not the on-disk weight size or a guarantee of steady-state memory. Lowering
threads or disabling the CPU memory arena did not materially reduce observed
loading peak in a small experiment. No target-host RAM or latency claim is made.

The optional lexical-head mode does not load ONNX or the tokenizer. It uses about
32 KiB of float32 task weights per label plus runtime/JSON overhead. No production
heads or real-traffic evaluation data were available, so no routing accuracy or
R330 speed result is claimed for that mode.

Validation completed locally:

- Full `go test ./...` and server build passed.
- Race-enabled tests passed for routing, config, base handlers and Claude catalog.
- Protocol, concurrency and training-artifact Python tests passed without weights.
- A Go request to a running pinned ONNX service selected a registered model.
- The pre-existing HTTP interrupt test race was independently reproduced from the
  untouched branch; its fixture now waits for actual cancellation before asserting.

Use the independent-label evaluator in `docs/balance.md` before treating the
starter model rankings or any locally retrained head as production-validated.
