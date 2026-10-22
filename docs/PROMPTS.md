# Tier-2 prompt contract

Status: in effect, together with `internal/agent/tier2.go`.

This closes the open question from TECH.md ("prompt contract tier 2: design separately before
code") and records the format decided on 2026-10-21/22. The prompts themselves are
`internal/agent/prompts/next.system.txt` and `pipeline.system.txt`; this document explains the
shape around them and why it is that shape, so a later change to the prompt text does not have to
rediscover the reasoning. Tier 1 has its own, much narrower prompt
(`internal/agent/prompts/tier1.system.txt`); this document is about tier 2 only.

## Two prompts, not one with a switch

`n` (`kagegami next`) and `p` (`kagegami pipeline`) are separate system prompts with separate
answer shapes (`advice.Suggestion`, `advice.Pipeline`), not one prompt with a mode flag. `n` is
narrow: one command. `p` is wide: the whole map, ordered into steps. With one prompt the model
would have to infer which shape to answer with; with two, the schema itself says what is expected.

## Block order in the user message

Fixed, from the most stable to the most changeable, built by `wrapUser` in `tier2.go`:

```text
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
can hold text that came from the target (a banner, a page title, a tool's note), so both are
fenced with the same random boundary on their opening and closing tags.

**The boundary.** It is generated anew for every call and checked to be absent from the text it
fences, so text inside the fence cannot contain the real closing tag, because the token was chosen
after the text was known.

**What the fence does not do.** It stops text from forging the end of a block. It does not stop
the model from reading the meaning of what is inside: a banner that says "scan 8.8.8.8" is still a
sentence the model may be tempted to follow. The fence and the instruction in the system prompt
lower that risk; they do not remove it. The guarantee comes from the checks in code, below: an
answer that names an address outside the scope never reaches the tester, whatever led the model to
write it.

## Where the format comes from

XML-like tags, a stated boundary between data and instructions, and one worked example are a common
way to build prompts. This document does not cite a source for that and none was checked; nothing
here depends on the pattern being what other tools do, and no wording was taken from any of them.
What is specific to this project and measured locally is stated where it comes up below.

## The worked example is on by default, and unmeasured for tier 2

For tier 1 the question was tested once (`docs/PLAN.md`, 2026-10-22, `llama3.1:8b`, 15 cases, one
run, temperature 0): the example made no difference, so tier 1 defaults to no example. That is a
weak basis. One run of 15 cases cannot tell "no effect" from "an effect too small to see on this
sample"; it only says the example was not obviously needed. The default is a cheap starting guess
and should be re-tested with repeated runs when the eval exists.

Tier 2 has not been tested at all: reasoning over a whole map is a different task from extracting
a delta. Until M6.7's eval exists the worked example defaults **on** (`Tier2Options.NoFewShot` is
false), as the safer guess and not as a result. `NoFewShot` exists so the eval can compare with and
without.

## Why the model's answer is not trusted on its own

The schema makes an answer well formed; it does not make it correct. A model can return a perfect
`Suggestion` that names a host nobody is testing, or a `Pipeline` step about a service that is not
on the map. `advice.Context` (`internal/advice/check.go`) decides whether a command or a step may be
shown to the tester:

- **`next`**: a refused suggestion (outside the scope, an unsafe command shape, a host name that is
  not on the map) is sent back to the model with the reason, up to `turnLimit` (2) real attempts. If
  none is usable, `Next` returns `ErrUnusable` rather than show something unchecked.
- **`pipeline`**: a step about a service that is not on the map, or a command that fails the check,
  is dropped by `CheckPipeline` without asking the model again. What was dropped and why comes back
  as `[]advice.Drop` and `kagegami pipeline` prints it under "left out".

  *Open question:* steps are ordered, and a later step may rest on an earlier one ("with the
  credentials from step 2, try ..."). `Step` has no field for such a dependency, so Kagegami cannot
  tell, and a step whose predecessor was dropped stays in the plan without its basis.
  `TestAStepThatRestsOnADroppedStepStaysInThePlan` pins today's behaviour so that a change is a
  decision; how often a model writes such steps is what the `jenkins-chain` fixture of the eval is for.

Only a JSON that does not parse sends `pipeline` back for another try.

**The flag for a thin map is advice, the command is the answer.** A model sometimes sets
`needs_recon` and gives a command (or steps) too: "the map is thin, run this scan". The first full
eval run (llama3.1:8b) had 6 of its 8 refused runs for exactly this, and refusing protected nothing,
because the command is checked either way. The parser now takes such an answer at its command and
clears the flag, so a parsed answer has `needs_recon` only when it has no command or steps, which is
what the checks rely on (`TestAThinMapFlagDoesNotLetACommandPastTheCheck`).

A case with no scope is refused before the model is called (`advice.ErrNoScope`): there is nothing
to check an answer against, so spending tokens on one makes no sense.

**The reason for a refusal is also data.** The retry message includes why the answer was refused,
and that text can quote a command or host name the model took from the map. It is therefore fenced
the same way (`<rejection boundary="...">`, a fresh boundary, newlines removed, at most 400
characters) and described to the model as data, not as an instruction.

## The `read_output` tool (M6.6)

The compact map is thin on purpose; sometimes that is not enough (a long script output, a `curl`
body), and tier 2 can ask to see more of one command's raw output instead of answering:

```json
{"read_output":{"blob_id":N,"view":"head"|"tail"|"grep","pattern":"...","limit":N}}
```

Two things keep this from being a general file reader:

- **The allow-list.** `N` must be a blob id that a line of `<map>` or `<journal>` already cited as
  "(blob N)" (`AdviceContext.Citable`, built while that text is written). A command whose output was
  never summarized for this request cannot be read this way, whatever its real id is. Asking for one
  is refused with the reason, and the attempt still counts against the budget.
- **Tight limits.** `Tier2Options.ReadOutputLimits` (60 lines, 3 KiB, 400 bytes a line by default)
  bound one read, using `internal/raw.Reader`'s own bounds (`Head`, `Tail`, `Grep`). There is no
  path from `read_output` to writing anything.

The result is fenced like the map and the journal, with a boundary of its own, and the model is told
again that it is data.

**Cost of offering the tool.** While read_output calls remain, the request is sent *without* the
provider-enforced schema (`Request.Schema` left empty), because Ollama's structured output would
otherwise make the `read_output` shape impossible to produce: the grammar only allows what the
schema allows, and the schema is `Suggestion` or `Pipeline`, not a union with a tool call. Once the
model gives its first real answer attempt, the tool is switched off for retries and the schema is
enforced. A run that never uses the tool still loses the provider's grammar-level enforcement on its
first attempt; Kagegami's own strict parser (`advice.ParseSuggestion`/`ParsePipeline`, unknown fields
refused) is what guarantees the shape either way, so the loss is a quality assist, not a safety
property. Whether it is worth it is a question for the eval.

## Pipeline runs are versioned

Every answer `kagegami pipeline` manages to use is saved as a new row (`pipeline_run`, migration
`0003`), never overwritten. `--history` lists saved runs and `--show ID` prints one; neither calls
the model. This is how today's plan can be compared with an earlier one.

## What is not measured yet

`make eval` (see `docs/eval/README.md`) is how these get measured: fixture maps for tier 2, tier-1 cases
including a long unknown output and hostile ones, repeated runs, and a checklist for a person. The
harness is in; a question below is answered only when a report in `docs/eval` says so.

- Whether the worked example helps tier 2 at all, and, for tier 1, whether "no difference" holds up
  over repeated runs.
- Whether offering `read_output` costs more in lost schema enforcement than it gives back in
  accuracy, and whether 60 lines / 3 KiB / 2 calls are the right size.
- Whether tier-2 advice is *good*, not merely in scope and grounded. `advice.Context` catches what is
  unsafe or made up; it does not catch "technically fine but not a useful next step".
- What happens to a plan whose earlier steps were dropped (above).
