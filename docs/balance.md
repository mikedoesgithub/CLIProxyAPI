# `balance`: local task-aware routing

`balance` is an opt-in virtual model for stateless HTTP chat. A persistent local
CPU classifier predicts task, difficulty and stakes. A configurable policy scores
registered model aliases and sends the original request through CLIProxyAPI's
normal credential selection, translation, thinking and payload-rule pipeline.
Explicit caller reasoning settings win. Concrete model requests bypass balance.

This feature does not install anything, enable itself, or alter existing auth files.

## Why this classifier

Research checked on 2026-10-27:

- [TypeSafe Jev](https://typesafe.ai/blog/introducing-system-one-models-and-jev)
  produces structured probabilistic decisions rather than generated strings.
  Its public offering is hosted; no usable open local Jev weights were verified.
- [decisionmodels/s1-llm-auto-router](https://huggingface.co/decisionmodels/s1-llm-auto-router)
  publishes MIT-licensed CPU ONNX weights: an 11-layer distilled mmBERT-small,
  int8 embeddings, 183 MB model plus approximately 34 MB tokenizer. It predicts
  nine task classes, five difficulty levels, stakes and four flags. Its author
  reports 26 ms median on a Xeon E5-2680 v4 with four threads. Those are author
  measurements, not R330 results. Its evaluation uses synthetic requests and
  broad labels; it does not establish which generation model is best for you.
- [RouteLLM](https://github.com/lm-sys/RouteLLM) learns model preferences from
  outcome data. That supports separating request classification from model
  quality rankings. [Model2Vec](https://github.com/MinishLab/model2vec) illustrates
  the faster static-embedding alternative, with less contextual expressiveness.

The default service uses s1, not the Jev API. Runtime needs NumPy, ONNX Runtime
and Tokenizers, without PyTorch, Transformers, a GPU or external inference calls.
The optional lexical-head mode below uses NumPy alone and requires your training
examples. It is less expressive than a contextual encoder.

## Setup

Requires Python 3.11+ with venv/pip and a CPU supported by the pinned ONNX Runtime
wheel. On Debian, install `python3-venv` if venv creation reports it missing.
Run from your CLIProxyAPI checkout:

```bash
bash tools/balance/setup.sh
.balance/venv/bin/python tools/balance/server.py --model-dir .balance/model --threads 2 --max-tokens 128
```

The setup script installs into `.balance/`, downloads an immutable model revision
from Hugging Face, verifies each SHA256 and retains the upstream MIT license. It
never modifies `config.yaml`, auth tokens, an existing service or system Python.
The serving process performs no model downloads. Start one process per host;
multiple proxy instances can share it.

Merge the `balance` subsection of [policy.example.yaml](../tools/balance/policy.example.yaml)
into your existing `routing:` map. Match every model string to the actual aliases
returned by your `/v1/models`; the example names are not automatically provisioned.
Choose an existing, broadly capable `fallback` and include it in `candidates`.
Minimal single-model configuration:

```yaml
routing:
  balance:
    enabled: true
    fallback: gpt-6.1-sol
    candidates:
      - model: gpt-6.1-sol
        tasks: ['*']
        quality: 95
        tools: true
        long-context: true
```

Change `gpt-6.1-sol` to your registered alias. Config reloads use a new policy
snapshot on the next request. The virtual `balance` entry appears in OpenAI,
Codex client, Claude and Gemini catalogs when enabled; no fake credentials are
registered. Example request, using your existing proxy API key:

```bash
curl http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer $CLIPROXY_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"balance","messages":[{"role":"user","content":"Write a Blender script for a spiral staircase"}]}'
```

For systemd, [balance-router.service](../tools/balance/balance-router.service)
assumes a checkout at `~/cliproxyapi`. Adjust its paths to your install, copy it to
`~/.config/systemd/user/`, then run:

```bash
systemctl --user daemon-reload
systemctl --user enable --now balance-router
```

It limits CPU to two cores and memory to 1 GiB. On macOS, run the Python command
in your existing supervisor. The proxy itself starts normally; if the classifier
is down, compatible requests immediately use the configured fallback.

## Selection policy and evidence

The sample is a versioned starter policy, not a universal leaderboard. Scores
`quality`, `cost` and `latency` are operator-relative values in [0,100]. Among
eligible candidates for a task and difficulty, the highest score wins:

```text
quality - cost-weight * cost - latency-weight * latency
```

Equal scores preserve config order. Defaults for both penalty weights are zero.
The example uses 0.10 and 0.05 to prefer cheaper/faster models with similar quality.
`tasks: ['*']` is a catch-all; optional `difficulties` restricts a candidate.
Rules are ordered literal phrase matches on the latest user turn. They explicitly
replace a broad task label, including a low-confidence label; they are policy
rules, not trained understanding, and can misread negation or quoted text.

Evidence for the two requested specialties:

- [OpenAI's Terra report](https://openai.com/index/introducing-gpt-6-1-terra/)
  supports a computer/browser-use preference and lower task costs. It also
  reports coding improvements, while recommending Sol/Astra for the most complex
  coding. It does not support a blanket claim that Terra is poor at everything else.
- [Blender AI's public arena](https://blenderai.org/leaderboard) currently places
  Opus 5.5 ahead, with Sol 6.1 among the strong contenders. Its votes and prompt
  set concern Blender work, not all CAD/3D tasks. The sample gives Opus a higher
  quality preference for moderate/hard 3D work, with Sol available as a catch-all.

Validate these choices against your prompts. GUI operation, generating Blender
Python, CAD geometry and rendering are different workloads. Update the policy as
models, aliases, evidence or prices change; no leaderboard scraping occurs in
the request path. Cost scores are not inferred from an OAuth subscription price.

Candidates must be registered and have an available count in the registry; pools
known to be entirely suspended or quota-exceeded are skipped. Actual credential
cooldowns, retries and same-model account failover remain with the auth manager.
There is no predictive quota-based cross-model retry in this version. New quota
failures can race routing and return the normal provider error.

Image inputs require registered IMAGE modality metadata. Tools require explicit
`tools: true`. Native tool types additionally require their exact wire name in
`native-tools`; verify executor/provider support before enabling these. For
example, a Playwright function tool is different from a provider-native computer
or search tool. Large user histories (>32,000 characters) require
`long-context: true`; this is a conservative policy gate, not exact token counting
or a guarantee that a provider's context window will fit.

Effort defaults are low for trivial/easy, medium for moderate and high for
hard/frontier tasks. Medium/high stakes raise lower difficulty to hard. A default
is applied only if registered thinking metadata advertises that exact level.
Token-budget-only models keep their defaults. Caller suffixes such as
`balance(high)` and explicit thinking/reasoning payload fields take precedence.
Provider application and final payload rules remain authoritative.

## Latency, memory and privacy

The service loads one model session, warms it before listening and binds only to
`127.0.0.1:8318`. ONNX defaults to two intra-op threads, one inter-op thread,
sequential execution and disabled thread spinning. BLAS is restricted to one
thread. One classification runs at a time; busy requests fail fast, with a bound
on connection handlers. The Go side also admits only one local preflight per
handler snapshot, with no waiting queue.

`timeout-ms` defaults to 100 and is capped at 250 ms. This is a local routing
preflight budget before credential acquisition, not a generation timeout.
Timeout, busy, malformed/oversized response, missing user text or confidence below
`min-confidence` (default 0.65) selects the configured fallback. Explicit specialty
rules may still select their matched task. If no compatible registered candidate
exists, return 503 rather than invent a model or ignore capability requirements.
Classification failure is not retried on the hot path. A timed-out ONNX operation
may finish locally, but cannot queue subsequent inference work.

Only user text goes to the loopback classifier. System instructions, assistant
content, tool results, image payloads and API/auth keys are excluded. The latest
user turn is bounded to 4,000 characters (beginning and end); context is the prior
user turn's final 600 characters. Default inference further truncates the pair
to 128 tokens using longest-first truncation. This changes the published 512-token
setup and trades context/accuracy for latency: evaluate it on held-out examples.
Use `--max-tokens 256` or `512` if measurements justify that tradeoff. Long decisions
may exceed the proxy budget and fall back. The classifier does not log prompts or
persist traffic. Existing proxy request logging settings still apply. Routing
metadata contains only selected model, task and decision source. Successful HTTP
responses also expose `X-Balance-Model` and `X-Balance-Source`, including SSE
headers, so clients can identify and pin the selected route.

Local measurements and exact commands are in [balance-benchmark.md](balance-benchmark.md).
They are engine timings, not end-to-end provider latency or R330 measurements.

## Retraining and evaluation

The released s1 ONNX artifact is an inference export. This integration does not
claim to fine-tune its transformer encoder without a training checkpoint. It does
provide a trainable small task head: hashed word/bigram features plus supervised
multiclass logistic regression, stored as portable JSON rather than executable
pickle. Fit it on your labeled examples and optional new task names such as
`browser` and `3d`, then map those labels in `candidates.tasks`.

Create private JSONL with `text` and `label`, at least 20 distinct examples per
label and at least two labels. No traffic is collected automatically. Keep it
outside Git (for example `.balance/private-tasks.jsonl`). Two rows illustrating
the format, not a sufficient training dataset:

```jsonl
{"text":"Use the browser to fill the checkout form", "label":"browser"}
{"text":"Build a bevelled mesh with Blender Python", "label":"3d"}
```

```bash
.balance/venv/bin/python tools/balance/train_head.py .balance/private-tasks.jsonl --output .balance/task-head.json
.balance/venv/bin/python tools/balance/server.py --model-dir .balance/model --head .balance/task-head.json
```

The custom task head replaces s1's category only above its stored acceptance
threshold (0.8 by default). s1 still supplies difficulty/stakes. Explicit phrase
rules run after the learned category, so remove a rule when you want to evaluate
or rely on the corresponding learned specialty. Training deduplicates normalized
text, rejects conflicting labels, holds out 20% per class and reports confusion,
macro F1, coverage and accepted accuracy. That same validation split calibrates
temperature; it is not an independent final test. Avoid near-duplicate templates
across train/test and evaluate separately on real workloads.

For the smallest runtime, train a second head with difficulty labels from
`trivial, easy, moderate, hard, frontier`, using the same trainer and requirements.
Then run without loading the ONNX encoder/tokenizer:

```bash
.balance/venv/bin/python tools/balance/train_head.py .balance/private-difficulty.jsonl --output .balance/difficulty-head.json
.balance/venv/bin/python tools/balance/server.py --head-only --head .balance/task-head.json --difficulty-head .balance/difficulty-head.json
```

This mode needs NumPy only (`pip install numpy==2.4.4` in a dedicated venv).
Difficulty uncertainty raises effort to hard; task uncertainty falls back. No
pretrained lexical heads are shipped, and there is no claim of production routing
accuracy from synthetic test fixtures. It needs your labels and independent evals.
The 8,192-feature float32 task weights cost about 32 KiB per label, plus Python,
NumPy and JSON-loading overhead. Validate resident memory on the actual host.

Evaluate either backend on an independent JSONL dataset using `text`, `label` and
optionally `difficulty` and `context`:

```bash
.balance/venv/bin/python tools/balance/evaluate.py .balance/test.jsonl --model-dir .balance/model
.balance/venv/bin/python tools/balance/evaluate.py .balance/test.jsonl --head-only --head .balance/task-head.json --difficulty-head .balance/difficulty-head.json
```

The evaluator prints aggregate accuracy, abstention coverage and confusion, never
prompts. Task accuracy alone cannot validate model rankings: compare generation
outcomes and cost for each candidate on your representative tasks before changing
policy scores. Revisit results after model/policy changes.

## Supported boundary

Supported: full-history stateless HTTP Chat Completions, Responses, Claude
Messages (including token count), Gemini generateContent, and HTTP SSE streams.
Opaque `previous_response_id`/interaction continuations, encrypted reasoning,
compaction/item references, WebSocket sessions, Home mode, provider-pinned native
Interactions, image generation and audio/video/file inputs require a concrete
model. This avoids switching provider-specific state between unrelated models.
Use the actual selected model for a stateful session.

## Verification

```bash
go test ./internal/balance ./internal/config ./sdk/api/handlers ./internal/client/claude/models
go test ./sdk/api/handlers/openai ./sdk/api/handlers/claude ./sdk/api/handlers/gemini -run Balance
.balance/venv/bin/python -m unittest discover -s tools/balance -p test_balance.py
BALANCE_CLASSIFIER_TEST_URL=http://127.0.0.1:8318/classify go test ./internal/balance -run LocalModelSmoke -v
go build -o /tmp/cliproxyapi-balance ./cmd/server
```
