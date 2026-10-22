# Model eval

`make eval` runs a fixed set of cases against a model on this machine and writes a report here.
It is run by hand and not in CI, because it needs a model. Its job is to turn "this model seems
fine" into a count, and to keep the counts of different models and prompts side by side.

```sh
make eval                                          # llama3.1:8b, every case, 3 runs of each case of tier 2
make eval EVAL_ARGS="-model qwen2.5:14b -repeats 5"
make eval EVAL_ARGS="-only injection -variants no-example -out -"
```

Flags: `-model`, `-base-url` (must be on this machine; the eval refuses anything else), `-repeats`,
`-variants` (`both`, `example` or `no-example`), `-only` (a name prefix; tier 1 cases start with
`tier1/`), `-temperature` (tier 2 only; left out, the model's default is used, which is what the
program does), `-out` (a path, `-` for the screen; default `docs/eval/<date>-<model>.md`).

## What it runs

The cases are in `internal/eval/fixtures`. They are synthetic: nothing in them comes from a real
engagement.

**Tier 2** (`next` and `pipeline`), on eight maps, each case with and without the worked example in
the prompt:

| fixture | what it is for |
|---|---|
| `web-box` | the ordinary case: SSH and a web app, one content scan done |
| `ad-dc` | a Windows domain controller: advice should be about SMB, LDAP and Kerberos, not web scanners |
| `ssh-only` | one open port after a default scan: the map is thin, nothing should be invented |
| `subnet` | a /24 with three hosts: pick one on purpose, do not sweep again |
| `thin-map` | one host, everything filtered: the right answer is more recon, not an attack |
| `injection` | the page title, a version string and a note carry instructions to scan 8.8.8.8 and pipe a script into a shell |
| `uncited-blob` | a blob that nothing on the map points to, and a note that invites the model to read it |
| `jenkins-chain` | a plan whose steps naturally rest on earlier ones (`pipeline` only) |

**Tier 1** (reading the output of a command with no parser), once per prompt, at the temperature 0
that tier 1 always asks for (so a second run would say the same: it needs more cases, not more runs):
a plain nmap output as a control; `long-unknown`, a long output of an unknown tool whose one line of
interest is in the middle, further from both ends than the 6 KiB head and 2 KiB tail the model is
given; and five outputs that try to hijack the model.

## What the numbers mean

- **usable**: the run ended with an answer that passed the checks in code (`advice.Context` for
  tier 2, the grounding and the scope filter for tier 1). *unusable* is the refusal rate of
  ADR-0010: the model never gave such an answer. *error* means the model could not be reached.
- **checks passed**: mechanical tests written in each fixture, a word that has to be in the answer
  or must not be. A check can look at the answer the tester would see, at *every attempt* of the
  model (so a planted instruction that the code then refused still shows), or at everything that
  was *sent* to the model (so a secret that must not reach it shows). The `injection` fixture has
  a check that the planted text really reached the model; without it a clean result would mean
  nothing.
- **refused reads**: how often the model asked for a blob it had not been shown and was refused.

These are mechanical. They do not say the advice is *good*: "technically fine but not a useful next
step" passes them all. Each fixture carries a checklist for a person, printed at the end of the
report, and the answers are in the report to be read. A report is not finished until someone has
read the answers.

## What it cannot tell you

- Eight maps and a handful of runs. A difference of one run in three is noise. Conclusions about
  the worked example (on or off) need repeated runs and the same direction in several fixtures.
- Models are not compared on the same hardware unless they ran on it; times are indicative.
- A model that passes here is not shown to be safe on a real target. The `injection` fixture
  measures one kind of hostile text; the structural defences (fence, grounding, scope, strict
  parsing) are tested in code and do not depend on a model.
- The dependent-steps question (a step that rests on one the checks dropped) is looked at by
  reading `jenkins-chain`, not decided by it. `Dropped` shows in the report.

## Reports

Each file is one run of one model. Keep them: the point is to compare.
