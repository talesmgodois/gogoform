# 30 ways to call `make run-tasks`

Every option is an environment variable read by `__ai_work/runner.py`; pass it as a `make` flag
(`make run-tasks VAR=value`). Run from the repo root in an interactive terminal (the runner asks what
to do when the worktree is dirty). See `README.md` for the full option table and defaults.

## Basics

1. `make run-tasks`
   Default run: Claude Code, resumes saved progress, 1 fix round, review on, no push, agent output hidden.

2. `make run-tasks AGENT_CLI_STDOUT=1`
   Same, but streams the agent's output live — use it to watch what the agent is doing.

3. `make run-tasks RUNNER_RESUME=0`
   Discards the saved progress in `errors/track/` and restarts the pending tasks from the plan stage.

4. `make run-tasks RUNNER_PUSH=1`
   Pushes the branch at the end if at least one task was committed.

5. `make run-tasks RUNNER_SKIP_REVIEW=1`
   Skips code review (and fixes) for all tasks — fast path for trivial or docs-only tasks.

## Review / fix loop

6. `make run-tasks MAX_FIX_ATTEMPTS=0`
   Review runs, but a FAIL verdict is not auto-fixed — useful when you want to fix review findings by hand.

7. `make run-tasks MAX_FIX_ATTEMPTS=3`
   Up to three `/code-review-fixer` rounds before giving up on a failing review — for large or risky tasks.

8. `make run-tasks MODEL_REVIEW=opus EFFORT_REVIEW=high`
   Strongest reviewer — catch subtle bugs on security- or billing-sensitive changes.

9. `make run-tasks MODEL_REVIEW=sonnet EFFORT_REVIEW=medium`
   Cheaper review for routine changes.

## Models per stage

10. `make run-tasks MODEL_PLAN=opus MODEL_IMPLEMENT=sonnet`
    Smart planner, cheaper implementer — the plan carries the hard thinking, implementation follows it.

11. `make run-tasks MODEL_PLAN=opus MODEL_IMPLEMENT=opus MODEL_REVIEW=opus`
    Max quality on every stage except commits (which defaults to sonnet).

12. `make run-tasks MODEL_IMPLEMENT=haiku MODEL_COMMITS=haiku`
    Cheapest run for mechanical tasks (renames, copy changes, small template edits).

13. `make run-tasks MODEL_COMMITS=opus`
    Better commit grouping/messages when the diff is large and mixed.

14. `make run-tasks MODEL_IMPLEMENT=claude-sonnet-5-5`
    Pins an exact model id instead of an alias, for reproducible runs.

15. `make list-models` then `make run-tasks MODEL_IMPLEMENT=<one of them>`
    Check which values are valid before setting any `MODEL_*`.

## Effort per stage

16. `make run-tasks EFFORT_PLAN=high`
    Thinks harder only when planning — good for tasks with vague requirements.

17. `make run-tasks EFFORT_IMPLEMENT=low EFFORT_COMMITS=low`
    Faster, cheaper execution when the plan is already precise.

18. `make run-tasks EFFORT_PLAN=max EFFORT_IMPLEMENT=high EFFORT_REVIEW=high`
    Hard task (migrations, multi-package refactors): maximum reasoning where it matters.

## Session granularity

19. `make run-tasks TODOS_PER_SESSION=3`
    Groups 3 plan todos per implement call — fewer sessions, less context re-loading, faster.

20. `make run-tasks TODOS_PER_SESSION=99`
    Implements the whole plan in a single session — for small tasks where splitting adds overhead.

21. `make run-tasks TODOS_PER_SESSION=1 AGENT_CLI_STDOUT=1`
    One todo per session while watching — easiest to debug a task that keeps failing at one step.

## Guardrails (Claude only)

22. `make run-tasks MAX_BUDGET_USD=5`
    Caps the spend of each agent call at 5 USD so a runaway session can't burn money.

23. `make run-tasks MAX_TURNS=50`
    Caps turns per agent call — stops sessions that loop without converging.

24. `make run-tasks MAX_TURNS=150 MAX_BUDGET_USD=10 RUNNER_PUSH=1`
    "Unattended" run: generous but bounded limits, pushes when done.

## OpenCode

25. `make run-tasks AGENT_CLI=opencode`
    Drives the pipeline with OpenCode instead of Claude Code (MAX_TURNS/MAX_BUDGET_USD are ignored).

26. `make run-tasks AGENT_CLI=opencode MODEL_IMPLEMENT=opencode/claude-sonnet-5-5`
    OpenCode with an explicit `provider/model` id (list them with `make list-models AGENT_CLI=opencode`).

27. `make run-tasks AGENT_CLI=opencode EFFORT_REVIEW=high AGENT_CLI_STDOUT=1`
    OpenCode with a high-effort review variant, output visible.

## Combined recipes

28. `make run-tasks RUNNER_RESUME=0 AGENT_CLI_STDOUT=1 MAX_FIX_ATTEMPTS=2`
    Retry a task that failed badly: fresh start, watch it live, one extra fix round.

29. `make run-tasks RUNNER_SKIP_REVIEW=1 MODEL_IMPLEMENT=haiku TODOS_PER_SESSION=99 RUNNER_PUSH=1`
    Quick-and-cheap batch of trivial tasks, committed and pushed.

30. `make run-tasks MODEL_PLAN=opus EFFORT_PLAN=high MODEL_REVIEW=opus MAX_FIX_ATTEMPTS=2 MAX_BUDGET_USD=8 RUNNER_PUSH=1`
    Production-grade run for important features: careful plan, strict review, extra fix round, bounded cost, pushed.

## Save tokens

Tokens are driven by how many agent sessions run (each one re-reads `AGENTS.md`, the task and the
files it touches), how long each session thinks (effort) and how many turns it takes. The model
choice changes the price per token, not the token count — so these calls cut the stages and the
thinking, not the model.

- `make run-tasks TODOS_PER_SESSION=99`
  One implement session per task instead of one per todo — the biggest saving, since context is loaded once.

- `make run-tasks EFFORT_PLAN=low EFFORT_IMPLEMENT=low EFFORT_REVIEW=low EFFORT_COMMITS=low`
  Less thinking (fewer reasoning tokens) in every stage — fine when tasks are precisely written.

- `make run-tasks MAX_FIX_ATTEMPTS=0`
  No fixer sessions after a failing review; read the review report and fix the findings yourself.

- `make run-tasks RUNNER_SKIP_REVIEW=1`
  Drops the review and fixer sessions entirely — check the diff yourself.

- `make run-tasks MAX_TURNS=40`
  Caps turns per agent call, so a session that keeps exploring or retrying stops early (Claude only).

- `make run-tasks TODOS_PER_SESSION=99 EFFORT_PLAN=low EFFORT_IMPLEMENT=low EFFORT_REVIEW=low EFFORT_COMMITS=low MAX_FIX_ATTEMPTS=0 MAX_TURNS=60`
  Fewest tokens while still planning and reviewing: combine all of the above.

- `make run-tasks RUNNER_SKIP_REVIEW=1 TODOS_PER_SESSION=99 EFFORT_PLAN=low EFFORT_IMPLEMENT=low EFFORT_COMMITS=low MAX_TURNS=40`
  Absolute minimum for trivial tasks (copy changes, renames): plan + one implement session + commits.

Also saves tokens, without flags:
- Keep `RUNNER_RESUME` at its default (`1`) — restarting with `0` re-runs stages that already finished.
- Write precise tasks that name the files to touch, so the agent doesn't explore to find them.
- Name trivial tasks `.ncr.md` so only those skip review.
- `AGENT_CLI_STDOUT` only changes what you see; it doesn't affect token use.

> Tip: to skip review for a single task only, name it `NNNNNN_name.ncr.md` instead of using
> `RUNNER_SKIP_REVIEW=1`.
