# ADR-0010: Cloud models and handling of refusals

Status: accepted · 2026-10-21

## Context

Kagegami is meant for everyone who does reconnaissance, and the usual setup is one machine (a Mac or a Linux box). The "Ollama on the host, Kali in a VM" layout of the development plan is a convenience for development, not the product's shape.

Research done before the model layer (M5) was designed points to four facts that shape this decision. They describe the situation at the time of writing and may change; the decision is written so that it does not depend on any single provider's behaviour.

- Autonomous agents suffer mostly from model refusals and from token cost, not from a lack of ideas.
- Consumer subscriptions (Claude Pro/Max, ChatGPT Plus and similar) are not meant to be embedded in third-party tools.
- The OpenAI-compatible layer of at least one major provider does not offer everything the native API does, such as strict output schemas and prompt caching.
- Security work trips content filters. A tool that depends on one model for every step is left with nothing when that model refuses.

## Decision

### Connectors: three, no more

1. **Ollama**, native (M5.2).
2. **A generic OpenAI-compatible connector** on `net/http` with a configurable `base_url` (v0.2). Other providers are reachable through it and are not officially tested.
3. **Native Anthropic Messages** (v0.2; may move after v0.2 if time is short).

Gateways such as OpenRouter add a third party to the data of an engagement; the README says so.

### Subscriptions are not used

Kagegami does not use, store or intercept OAuth tokens of subscriptions. A user without an API key is pointed to a local model. An experimental `provider-subprocess` (running the vendor's own CLI under the user's login) is postponed beyond v0.2 and would pin a minimum CLI version.

### Rules for every cloud path

- The cloud is **off by default** and is switched on explicitly, with a warning that data leaves the machine and that it costs money.
- A **token and money ceiling per session** is on by default. When it is reached, Kagegami stops and keeps what has been collected.
- Data goes through the masking layer (`internal/mask`) before it leaves.
- Prices are never hard-coded; usage is taken from the API response.
- The stable part of a prompt (system prompt, case context, compact map) comes first, so that provider-side caching can work.

### `Provider` knows what a model can do

The interface reports capabilities: strict schema, tool calling, caching, context size. What is missing degrades softly (for example, a schema is checked by Kagegami instead of enforced by the provider).

### Refusals

- One error type, `Refused`, for every provider's way of saying no (a refusal stop reason, `content_filter`, a safety block).
- **An empty answer with HTTP 200 is an error**, not a success.
- **No automatic retry after a refusal.** The refusal is shown in the panel with the options (another model, a local one, do it by hand) and written to the journal.
- Switching to another model happens only on the user's command.
- Kagegami does not rewrite requests to get past a filter and does not cycle through models. The one retry of M5.5 is for invalid JSON without a refusal.

### A baseline without a model

The pipeline works without a model first (M6): rules "service, then the usual checks", the parsers, and the scope check. The model refines and explains it. A refusal or a weak model leaves the user with something.

### One model for both tiers

If a single model is configured, it serves both tiers and tier 2 degrades: a shorter map, and a pipeline assembled service by service.

### Verified models

The documentation keeps a table of models checked with `make eval` (M6.7), including the share of refusals. It does not promise that a small model equals a large one.

## Consequences

- Users get a working tool with a local model and an explicit, bounded, reversible step to the cloud.
- Refusals are visible and recorded instead of being hidden by retries, which keeps the journal honest and keeps Kagegami out of the business of defeating safety systems.
- Three connectors keep the maintenance and the test matrix small.
- The ceiling and the cloud switch need tests (the cloud path is unreachable in local-only mode, M6.8).
- The README points security professionals to the providers' own programs for verified use of their models.
