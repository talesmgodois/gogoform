---
name: make
description: Run a make target of this repo (root Makefile, which forwards workdir/ targets) and reply with only its result; full output only with --verbose. Use when asked to run a make command/target, or run /make.
argument-hint: "<target> [VAR=value ...] [--verbose]"
allowed-tools: Bash, Read
---

# /make — run a make target, show only the result

Arguments from the caller: $ARGUMENTS

## Output rules (most important)

- Do **not** narrate. No text before or between tool calls, no plan, no "running…", no
  explanation of what you are about to do or of how you read the output.
- Your only message is the final report from section 3, in the user's language.
- Exception: with `--verbose`, include the full command output too (section 3).

## 1. Parse

- Remove `--verbose` (or `-v`) from the arguments; remember whether it was there.
- What is left is passed to make as-is: the target(s) plus `VAR=value` assignments,
  e.g. `new-task NAME=add_login` or `run-tasks AGENT_CLI=opencode`.
- No target → use `help`.
- Run from the repo root. Never add flags such as `-i`, `-k` or `-B` that the caller didn't give.

## 2. Run

Save the output to a log file so it isn't printed into the conversation:

```bash
LOG=$(mktemp /tmp/make-XXXXXX.log); start=$(date +%s)
make <args> >"$LOG" 2>&1 </dev/null; code=$?
echo "exit=$code seconds=$(( $(date +%s) - start )) log=$LOG"
```

Use the scratchpad directory for the log instead of `/tmp` when the session has one.
`</dev/null` matters: `run-tasks` may ask questions on stdin (e.g. dirty working tree);
it must not hang. If the log shows it stopped at such a prompt, report that the target
needs an interactive terminal and quote the question.

Timing:
- Targets that never end on their own (`dev`, `run`, `db-logs`, `adminer`): run the same
  command with `run_in_background: true`, then read the log once it shows the service is up
  (or failed). Report that it is running in the background and how to stop it.
- `run-tasks` and other long targets (`build-db`, `test`, `setup-tools`): use
  `run_in_background: true` and wait for the completion notification instead of a
  foreground timeout; don't poll the log.
- Everything else: foreground, `timeout: 600000`.

Read the log with `tail`/`grep` (or `Read` for short logs) — only as much as the report needs.

## 3. Report

Without `--verbose`, reply with only:

```
✅ make <args> — OK (<duration>)        or        ❌ make <args> — failed (exit <code>, <duration>)
```

followed by the result in at most ~10 lines:
- **Success:** the useful outcome — what was created/changed/printed (e.g. the new task file
  path, test totals, migration status, the `help` target list). For `run-tasks`, the
  completed/failed tasks from the runner's final summary.
- **Failure:** the error itself — the first relevant error lines and the make line that
  failed (`make: *** [...] Error N`), in a code block. No speculation about fixes unless the
  cause is obvious from the error; then one line.

Format the result as chat markdown, not as raw terminal text: strip ANSI color codes, and
render lists/tables from the output as markdown tables or bullet lists with targets and
commands in `code`. The `help` target is the exception to the ~10-line limit — render it in
full like this, one table per section of the output, keeping every target and description:

```
### Raiz
| Alvo | Descrição |
|------|-----------|
| `new-task` | Cria o próximo arquivo de task em `__ai_work/tasks` — `make new-task NAME=my_task` |

### App (`workdir/`)
| Alvo | Descrição |
|------|-----------|
| `dev` | Sobe o banco, espera ficar saudável e roda a app localmente |
```

Translate the descriptions to the user's language; keep target names, variables and example
commands unchanged. Skip `help` itself.

Then one line: `Log: <log path>` (the path must be absolute, written in one piece).

With `--verbose`, give the same status line, then the full log in a code block. If it is over
~300 lines, show the last 300 and say how many were cut, pointing to the log file.
