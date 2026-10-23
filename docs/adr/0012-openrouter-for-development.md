# ADR-0012: OpenRouter for development

Status: accepted · 2026-10-23

## Context

The models that fit on a laptop (an 8B model through Ollama) are too weak to tell whether a
change to Kagegami made it better or worse: they misread output, ignore a flag that is in the
command, and give a different answer each time. Evals and manual runs on such a model measure the
model, not the program. A stronger model is needed while the program is built.

ADR-0010 allows cloud models, off by default, behind `model.cloud_fallback`. Its first cloud
connector was planned for v0.2. This ADR adds one early, for development only.

## Decision

**A provider `openrouter`** (`pkg/llm/openrouter`), named in the configuration like any model:
`openrouter/<vendor>/<model>`, for example `model.tier2: openrouter/anthropic/claude-sonnet-4.5`
(any model from openrouter.ai/models).

- **The address is fixed** in the code (`https://openrouter.ai/api/v1`). The configuration cannot
  change it, so the key cannot be sent to an address a file names. Redirects are not followed.
- **The key comes from `OPENROUTER_API_KEY` and from nowhere else.** It is not a field of the
  configuration, it is not in the code, and a missing key is an error that names the variable. The type
  that holds it prints as dots through fmt, slog and JSON, a message of the server that quotes it is
  cleaned, and tests check each of these.
- **The same gate as every cloud model.** It is a remote model, so `models.Build` refuses to make it
  unless `model.cloud_fallback: true`. Nothing about OpenRouter is on by default: the default
  models stay local (Ollama), and the choice of a cloud model is a line in the person's own config.
- **The key reaches the panel safely.** `kagegami watch` opens the panel in a new tmux pane, which does
  not inherit the environment of the shell. The key is passed in the environment of the new pane
  (`tmux split-window -e`), never in the text of its command, which any process can read.
- **The eval can use it by name** (`make eval EVAL_ARGS="-model openrouter/..."`). Its fixtures are made
  up, which is what makes sending them there fine; without that name the eval still refuses a
  model that is not on this machine.
- **It says what leaves.** Whenever a model is not on this machine, stderr and the first line of the
  panel say that the commands, their output and the hosts on the map are sent as they are.

The key is not written into the source, and the OpenRouter model is not made the default, even for
development: a key in a public repository is lost the day it is committed, and a default that sends
data to the cloud contradicts "local by default".

## Consequences

- **Masking is not built yet** (M9). Until it is, whatever the model reads goes out unmasked, to
  OpenRouter and to the company that runs the model. Use this on lab targets and made-up data only,
  never on a real engagement. The warning above says so every time.
- It costs money per token; `limits.session_tokens` is the ceiling, as for any model.
- The structured answer is requested through `response_format` and also written in the prompt, but
  not forced: a model may break it, and Kagegami's own strict parser decides, as for every model.
- This is the first of the planned cloud connectors, narrowed to one gateway. A general
  OpenAI-compatible connector with a configurable address stays a v0.2 item, with its own care for
  where a key may be sent.
