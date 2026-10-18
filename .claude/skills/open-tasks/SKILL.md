---
name: open-tasks
description: List the open (not finished) tasks in __ai_work — pending, failed and in-progress — with their plan progress and runner status, or mark one as ended. Use when asked to show, list or view open/pending tasks, to mark/close/end a task as done, or run /open-tasks.
argument-hint: "[task number or name filter] | end <task number or name>"
allowed-tools: Bash(ls:*), Bash(find:*), Bash(grep:*), Bash(head:*), Bash(cat:*), Bash(git log:*), Bash(git status:*), Bash(git rev-parse:*), Bash(git ls-files:*), Bash(git mv:*), Bash(mv:*), Read, Edit
---

# /open-tasks — view the open tasks, or mark one as ended

Arguments from the caller (may be empty): $ARGUMENTS

- Empty, or a task number/name → **list** mode (sections 1–3): show the open tasks, keeping
  only the ones matching the filter if one was given. Read-only: never create, move, edit or
  run tasks, and never change branches.
- `end <task number or name>` (also accept `done`, `close`, `finish`, or the same request in
  plain words, e.g. "marcar 000017 como ended") → **end** mode (section 4).

## Where tasks live

Task files are named `<NNNNNN>_<name>.md` (or `.txt`); a `.ncr.md` suffix means the runner
skips the code review for it. `__ai_work/runner.py` moves them between:

| Folder                     | Meaning                                                        |
|----------------------------|----------------------------------------------------------------|
| `__ai_work/tasks/`         | **Pending** — not started yet, or interrupted before failing  |
| `__ai_work/errors/`        | **Failed** — parked after a failed step; resumed on next run  |
| `__ai_work/finished/`      | Done — not open, ignore (unless the tracker says otherwise)   |

`__ai_work/errors/<stem>.review.md` next to a failed task is its failing code-review report.
`__ai_work/errors/track/<n>_error.json` files hold the runner's progress; files ending in
`.archive` are old runs and must be ignored. Only the highest-numbered non-archived file is
current. Its shape:

```json
{"run": {"current": {...}, "error": ...},
 "tasks": {"<file name>": {"file", "status", "todos": {"1": {"started", "finished"}},
           "stages": {"plan", "review", "commit", "push", "merge"}, "last_error"}}}
```

## 1. Collect

```bash
ls -1 __ai_work/tasks __ai_work/errors
ls -1 __ai_work/errors/track
git rev-parse --abbrev-ref HEAD
```

The open tasks are:
- every `.md`/`.txt` file in `tasks/` (skip `.gitkeep`);
- every task file in `errors/` (skip `*.review.md` and the `track/` folder);
- every entry in the current (non-archived) track file whose `status` is not `complete`,
  even if its file isn't in `tasks/` or `errors/`.

If a filter was given, keep only the matching tasks.

## 2. Inspect each open task

Read each task file and extract:
- **Title**: the first `# ` heading.
- **Summary**: one short line describing the goal, taken from the text after the title.
- **Plan progress**: if the file contains `<!-- RUNNER:PLAN -->`, count the `- [x]` / `N. [x]`
  items against all checkbox items below it (e.g. `2/5`). No marker → `no plan yet`.
- **Runner status** (from the current track file, if the task is in it): `status`, the last
  stage that is `true`, and `last_error` if set.
- **Failure reason** for tasks in `errors/`: `last_error` from the tracker, otherwise the
  verdict/first findings of the matching `*.review.md`.
- **Commits**: the runner commits straight onto the checked-out branch, tagging every commit
  with a `Task: <NNNNNN>` trailer. Count them with `git log --oneline --grep "^Task: <NNNNNN>$"`.
- **Partial work**: the unfinished task the runner was working on (`run.current` in the
  tracker) owns any uncommitted changes in `git status --porcelain`; mark it with "uncommitted".

## 3. Report

Reply in the user's language. Print a table sorted by task number:

```
| #      | Task                 | State   | Plan | Stage / error            | Commits             |
|--------|----------------------|---------|------|--------------------------|---------------------|
| 000021 | add_login_page       | pending | —    | —                        | —                   |
| 000017 | Implement_authorization | failed | 3/7 | review: missing tests  | 0 + uncommitted     |
```

Then, for each task, one line with its summary. Link files as markdown links with paths
relative to the repo root, e.g. `[000017_Implement_authorization.md](__ai_work/errors/000017_Implement_authorization.md)`.

End with the totals (`N open: X pending, Y failed, Z in progress`) and a hint that
`make run-tasks` runs/resumes them and `make new-task NAME=<name>` creates a new one.

If nothing is open, say so in one line and show the latest finished task number.

## 4. End mode — mark a task as ended

Closes an open task by hand (e.g. it was done outside the runner, or is no longer needed),
so `make run-tasks` stops picking it up. Never change branches, commit, push, merge or delete
branches here, and never discard working-tree changes.

1. **Resolve the task.** Run the collection from section 1 and match the argument against the
   open tasks' numbers and file names. Exactly one match is required: with none, say the task
   is not open (and whether it is already in `finished/`); with several, list them and stop.
2. **Move the task file into `finished/`.** Use `git mv` if the file is tracked
   (`git ls-files --error-unmatch <path>` succeeds), otherwise `mv`. Refuse if a file with the
   same name already exists in `finished/`. Do not edit the task file's content or plan.
3. **Move its failing review report**, if `__ai_work/errors/<stem>.review.md` exists, to
   `__ai_work/reviews/<stem>.review.error.md` with plain `mv` (`reviews/` is gitignored, so
   the report stays on disk but leaves git).
4. **Update the runner tracker.** If the task has an entry in the current (non-archived)
   track file, use `Edit` to set its `"status"` to `"complete"`, `"last_error"` to `null` and
   `"file"` to the new `__ai_work/finished/...` path, and set `run.current` to `null` if it
   points at this task. Leave the `stages` and `todos` as they are. If every entry is now
   `complete`, rename the track file by appending `.archive` (as the runner's
   `archive_if_complete` does).
5. **Report** what was moved/updated, then:
   - if the task had uncommitted partial work (see section 2), say it was kept in the working
     tree and must be committed or discarded by hand;
   - remind that the move is not committed yet (`/commits` commits it);
   - show the remaining open-task count.
