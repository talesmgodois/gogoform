# runner.py — token efficiency improvement plan

## Context
`make run-tasks` seems to burn a lot of tokens for the work it gets done. Reading
`__ai_work/runner.py` along with the `code-review`, `code-review-fixer` and `commits` skills shows
the main cost: **every step starts a new, cold `claude -p` session** that has to rediscover the repo.
And the repo has **no CLAUDE.md**, so nothing gives those sessions a head start.

For example, task 000023 cost 1 plan session + 14 implement sessions (one per todo) + 1–3
reviews + 0–2 fixer runs + 1 commits session, so about 17–21 cold sessions. Each one rereads the
task, explores `internal/`, the templates and so on, and the review and commits sessions both
reread the whole diff and every untracked file. On top of that, the runner records no usage, so
none of this can be measured.

## Findings, ranked by impact

1. **One cold session per todo** (`implement_todos`, runner.py:597). The `PLAN_PROMPT` asks for
   "small todos … in a single agent session", which pushes toward 10–15 todos. Each one pays the
   full exploration cost again, and the planner's exploration is discarded (only the text between
   PLAN_BEGIN and PLAN_END is kept).
2. **No CLAUDE.md**. Every session (implement, review, fixer, commits) rediscovers the layout, the
   make targets and the conventions. The code-review skill even says to read CLAUDE.md first.
3. **No measurement**. `run_agent` either throws stdout away or keeps only the text. With
   `--output-format json`, the CLI reports `total_cost_usd`, `usage`, `num_turns` and `session_id`
   for each call.
4. **The review/fix loop rereads everything** (runner.py:247). There can be up to 3 full reviews
   of the whole task diff and 2 fixers, each starting cold. The re-review after a fix redoes the
   whole diff when it only needs to check the fixer's changes against the previous findings.
5. **Every stage uses the same model and effort**. Commits and planning don't need the strongest
   model, and the commits skill rereads the entire diff to group it, even though the plan's todos
   already describe the logical groups.
6. **Sessions have no limit**. There is no `--max-turns` or `--max-budget-usd`, so a looping
   session (for example, todo 14's "run the app end to end") can run without bound.

## Proposed changes (all in `__ai_work/runner.py` unless noted)

### A. Measure first
- `run_agent`: for `AGENT_CLI == "claude"`, always add `--output-format json` and `capture=True`.
  Parse the JSON, then return `result` as stdout so the existing regexes (VERDICT, FIX_RESULT,
  RESULT, PLAN_BEGIN) keep working.
- Record `{stage, todo, cost_usd, input/output/cache tokens, num_turns, session_id}` in a new
  `usage` list on the tracker entry (`Tracker.mark_todo`/`mark_stage` already save after each
  step). Print a per-task cost summary in `process_tasks`.
- Keep `AGENT_CLI_STDOUT=1` printing the result text.

### B. Reuse the planning session's context
- Run the plan session with a fixed `--session-id <uuid>` saved on the tracker entry
  (`entry["session_id"]`).
- Implement todos with `--resume <session_id> --fork-session` (or just `--resume`), so each todo
  starts with the planner's exploration already in context and the prompt prefix is cached.
  The resumed call must pass `--dangerously-skip-permissions` in place of the planner's
  `--permission-mode plan`.
- If no session ID is saved (an old task, or the session expired), fall back to today's cold start.

### C. Fewer, bigger todos
- Change `PLAN_PROMPT` to ask for **3–6 todos**, each a coherent slice (for example, data layer,
  handlers, templates, tests and docs), and drop "small … single agent session".
- Optionally add an env var `TODOS_PER_SESSION` (default 1) that groups consecutive todos into one
  `IMPLEMENT_PROMPT` call. `render_plan` already supports marking statuses, so it would only need
  to accept a list of current numbers.

### D. Add a concise CLAUDE.md (repo root, about 60–100 lines)
- Stack, directory map (`workdir/internal/pkg/...`, handlers, templates, sqlc), make targets
  (`make test`, `make sqlc`, `make swagger`), and conventions (errors via apperrors, test fakes in
  `fakes_test.go`, CSP/nonce rules). The content would come from an exploration pass during
  implementation. Every session benefits, including reviews.

### E. Cheaper models per stage
- Add env vars `MODEL_PLAN`, `MODEL_IMPLEMENT`, `MODEL_REVIEW`, `MODEL_COMMITS` (passed as
  `--model` when set). Suggested defaults: commits → `sonnet`, all others → unset (CLI default).
  Optionally, `EFFORT_<STAGE>` maps to `--effort`.
- `run_grouped_commits`: pass the ticked plan in `args` ("one commit per todo where possible:
  1. …, 2. …") so the commits skill groups by the plan instead of working the grouping out from
  the raw diff.

### F. Narrow the re-review
- In `review_with_fixes`, on attempt > 0, pass args to `/code-review` that name the previous report
  path and say "verify those findings are fixed and review only what changed since the fix", and
  update `.claude/skills/code-review/SKILL.md` §1 to accept a `--recheck <report>` argument.
  Lower `MAX_FIX_ATTEMPTS` to 1 by default.

### G. Guardrails
- Add `--max-turns` (env `MAX_TURNS`, default e.g. 80) and an optional `--max-budget-usd` (env
  `MAX_BUDGET_USD`) to every `claude` call. If a call hits a limit, it fails the step, which the
  existing `TaskFailed` and resume path already handle.

## Suggested order
A first (so the gains can be measured), then D + C (cheap, big win), then B, E, F, G.

## Verification
- `python -m py_compile __ai_work/runner.py`.
- Create a small throwaway task (`make new-task NAME=runner_smoke`, for example "add a comment to
  README"), run `make run-tasks`, and check that `__ai_work/errors/track/<n>_error.json` gets
  `usage` entries and `session_id`, that implement calls resume the plan session (the same
  session_id prefix shows in the JSON), and that the per-task cost summary prints.
- Compare `cost_usd` per task before and after on a similar task.
- `/open-tasks` still parses the tracker (extra keys are ignored).
