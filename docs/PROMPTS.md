# Tier-2 prompt contract

Status: in effect, together with `internal/agent/tier2.go`.

This closes the open question from TECH.md ("prompt contract tier 2 — design separately
before code") and records the format decided on 2026-10-21/22. The prompts themselves are
`internal/agent/prompts/next.system.txt` and `pipeline.system.txt`; this document explains the
shape around them and why it is that shape, so a later change to the prompt text does not have
to rediscover the reasoning. `internal/agent/prompts/tier1.system.txt` is tier 1's own, much
narrower prompt (ADR covered in M5.5/M5.6); this document is about tier 2 only.

## Two prompts, not one with a switch

`n` (`kagegami next`) and `p` (`kagegami pipeline`) are separate system prompts and separate
answer shapes (`advice.Suggestion`, `advice.Pipeline`), not one prompt with a mode flag. `n` is
narrow: one command. `p` is wide: the whole map, ordered into steps. Mixing them into one prompt
would mean the model has to infer which shape to answer with; keeping them apart means the
schema itself says what is expected.

## Block order in the user message

Fixed, from the most stable to the most changeable, built by `wrapUser` in `tier2.go`:

```
<case>
target: ...
goal: ...
scope: ...
phase: ...
rules: ...
</case>
<map boundary="TOKEN">
...the map, as dense text (internal/agent/context.go, mapText)...
</map boundary="TOKEN">
<journal boundary="TOKEN">
...the folded journal (journalText)...
</journal boundary="TOKEN">
<request>
...the tester's own words, or a default request...
</request>
```

`<case>` and `<request>` are the tester's own words and are not fenced. `<map>` and `<journal>`
can hold text that came from the target (a banner, a page title, a tool's note) and are always
fenced by the same random boundary, on both of their opening and closing tags. The boundary is
new on every call (`Tier2.boundary`, the same mechanism tier 1 uses) and is checked not to occur
anywhere in the text it fences; if it did, that occurrence could not be confused with the real
closing tag, because by then the real boundary has already been chosen to not collide, but
picking it that way removes the question entirely rather than relying on an argument about what
a model would or would not be fooled by.

## Why XML-like tags, a boundary token, and a worked example

These three choices are a borrowed pattern, not a result measured on this project: the general
idea (structured tags over prose, a stated boundary between data and instructions, one worked
example) is common in current agent prompts, including other AI coding tools. No specific
wording was copied from any of them, and nothing here has been checked against their internals;
it is cited as "this is a known approach", not as a reference implementation. What *is*
project-specific, and backed by a local measurement, is what follows.

## The worked example is on by default, unmeasured for tier 2

Tier 1's few-shot question was settled by a measurement (`docs/PLAN.md`, 2026-10-22,
`llama3.1:8b`, N=15, one run: no difference, so it defaults off for tier 1). Tier 2 has not had
that measurement yet — reasoning about a whole map is a different task from extracting a delta,
and the plan (M6.7) says the eval for tier 2 needs a "is this advice any good" check, not just
"did it stay in scope", which is slower to build than tier 1's check was. Until that eval exists,
the worked example defaults **on** (`Tier2Options.NoFewShot` defaults to false) as the safer
starting guess, not as a measured fact, and `NoFewShot` exists specifically so the eval can
compare with and without it the same way the tier-1 harness did.

## Why the model's answer is not trusted on its own

The schema makes an answer well-formed; it does not make it correct. A model can return a
syntactically perfect `Suggestion` that names a host nobody is testing, or a `Pipeline` step
about a service that does not exist on the map. `advice.Context` (`internal/advice/check.go`)
is what actually decides whether a command or a step can be shown to the tester:

- **`next`**: a refused suggestion (out of scope, an unsafe command shape, a host name that is
  not on the map) is sent back to the model with the reason, up to `turnLimit` (2) real
  attempts. If none is usable, `Next` returns `ErrUnusable` rather than show something unchecked.
- **`pipeline`**: a bad step is dropped by `CheckPipeline` on its own, without asking the model
  again — a plan is a list, and removing one wrong entry does not invalidate the rest the way a
  single wrong command does for `next`. What was dropped, and why, comes back as `[]advice.Drop`
  and is shown to the tester (`kagegami pipeline` prints it under "step(s) ... left out").

Only a JSON that does not parse at all sends `pipeline` back for another try; a JSON that
parses but says something ungrounded is filtered, not retried.

A case with no scope set is refused before the model is ever called (`advice.ErrNoScope`): there
is nothing to check an answer against, so there is no point spending tokens on one.

## The `read_output` tool (M6.6)

The compact map is deliberately thin (see `docs/PLAN.md`, M6.2); sometimes that is not enough —
a long NSE script output, a `curl` body — and tier 2 can ask to see more of one specific
command's raw output instead of answering yet:

```
{"read_output":{"blob_id":N,"view":"head"|"tail"|"grep","pattern":"...","limit":N}}
```

Two things keep this from being a general file reader:

- **The allow-list.** `N` must be a blob id that a line of `<map>` or `<journal>` already cited
  as "(blob N)" (`AdviceContext.Citable`, built while that text is written). A command whose
  output was never summarized for this request cannot be read this way, whatever its real id is
  — asking for one is refused with the reason, and the attempt still counts against the budget.
- **Tight limits.** `Tier2Options.ReadOutputLimits` (60 lines, 3 KiB, 400 bytes a line by
  default) bound one read, reusing `internal/raw.Reader`'s own bounds (`Head`/`Tail`/`Grep`).
  There is no path from `read_output` to writing anything: it only ever reads a file already on
  disk from a command that already ran.

The result is fenced the same way as the map and the journal, with a fresh boundary of its own,
and is explicitly told to the model as DATA again, not as a new instruction.

**Cost of offering the tool.** While any read_output calls are still available, the request is
sent *without* the provider-enforced schema (`Request.Schema` left empty), because Ollama's
structured-output grammar would otherwise make the `read_output` shape impossible to produce at
all — the grammar only allows what the schema says, and the schema is `Suggestion` or
`Pipeline`, not a union of that and a tool call. Once the model gives its first real (non-tool)
answer attempt, the tool is turned off for any retry and the schema is enforced from then on, to
give a failing exchange its best chance of ending with something usable. This means a run that
never uses the tool still loses the provider's grammar-level enforcement on its very first
attempt; Kagegami's own strict parser (`advice.ParseSuggestion`/`ParsePipeline`, unknown fields
refused) is what actually guarantees the shape either way, so this is a loss of a quality assist,
not of a safety property. Whether it is worth it in practice is exactly the kind of question
`make eval` (M6.7) is for.

## Pipeline runs are versioned

Every answer `kagegami pipeline` manages to use is saved as a new row (`pipeline_run`,
migration `0003`), never overwritten. `kagegami pipeline --history` lists the saved runs and
`--show ID` prints one, neither of which calls the model. This is what lets a tester compare
today's plan with an earlier one.

## What is not measured yet

- Whether the worked example helps tier 2 at all (see above).
- Whether offering `read_output` costs more in lost schema enforcement than it gives back in
  accuracy, and whether the default limits (60 lines / 3 KiB / 2 calls) are the right size.
- Whether tier-2 advice is actually *good*, as opposed to merely in-scope and grounded. The
  checks in `advice.Context` catch what is unsafe or made up; they do not catch "technically
  fine but not a useful next step". That is the harder half of M6.7's eval, still to be built.
