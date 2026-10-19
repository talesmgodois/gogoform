import json
import os
import re
import shutil
import signal
import subprocess
import traceback
from datetime import datetime, timezone
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = BASE_DIR.parent

TASKS_DIR = BASE_DIR / "tasks"
FINISHED_DIR = BASE_DIR / "finished"
ERRORS_DIR = BASE_DIR / "errors"
REVIEWS_DIR = BASE_DIR / "reviews"
TRACK_DIR = ERRORS_DIR / "track"
WORKDIR = PROJECT_ROOT / "workdir"

# Git doesn't track empty directories, so once the last task file is moved out
# tasks/ would vanish on the next branch switch. A tracked .gitkeep keeps it alive,
# and mkdir recreates any directory that is missing anyway.
for _dir in (TASKS_DIR, FINISHED_DIR, ERRORS_DIR, REVIEWS_DIR, TRACK_DIR):
    _dir.mkdir(parents=True, exist_ok=True)
(TASKS_DIR / ".gitkeep").touch(exist_ok=True)


def run_command(command, cwd=WORKDIR, check=True):
    """Utility to run shell commands safely."""
    result = subprocess.run(
        command,
        cwd=cwd,
        text=True,
        capture_output=True,
        shell=isinstance(command, str)
    )
    if check and result.returncode != 0:
        raise RuntimeError(f"Command failed: {command}\nError: {result.stderr}")
    return result


def git_status(cwd=PROJECT_ROOT):
    """Returns 'git status --porcelain' output (empty string when the tree is clean)."""
    return run_command("git status --porcelain", cwd=cwd, check=False).stdout.strip()


def move_task(task_path, dest_dir):
    """Moves a task file into dest_dir and returns its new path."""
    dest = dest_dir / task_path.name
    shutil.move(str(task_path), str(dest))
    print(f"📁 Moved task file to {dest}")
    return dest


def extract_task_id(filename):
    """Extracts '000001' from '000001_scaffold_program.md'."""
    match = re.match(r"^(\d+)", filename)
    return match.group(1) if match else filename.split("_")[0]


def prompt_user_choice(question, options):
    """Prompts interactively for one of several (key, label) options. Returns the chosen key."""
    valid_keys = {key.lower() for key, _ in options}
    while True:
        print(f"\n{question}")
        for key, label in options:
            print(f"  [{key}] {label}")
        try:
            choice = input("Your choice: ").strip().lower()
        except EOFError:
            print("\n🛑 No interactive terminal to answer; aborting. Re-run from a terminal, or clean the working tree first.")
            raise SystemExit(1)
        if choice in valid_keys:
            return choice
        print(f"⚠️  Invalid choice '{choice}'. Please pick one of: {', '.join(sorted(valid_keys))}")


def handle_dirty_worktree(cwd=PROJECT_ROOT):
    """Detects uncommitted changes and asks the user how to proceed before a fresh run.

    Manual edits left in the working tree would otherwise be silently committed
    together with the first task's changes.
    """
    status = git_status(cwd)
    if not status:
        print("🧹 Working tree is clean.")
        return

    print("\n⚠️  Uncommitted changes detected in the working tree:")
    print(status)

    choice = prompt_user_choice(
        "How would you like to proceed before running the tasks?",
        [
            ("1", "Stash changes (git stash -u) and continue"),
            ("2", "Commit changes now via the /commits skill, then continue"),
            ("3", "Discard all changes (DESTRUCTIVE: git checkout -- . && git clean -fd)"),
            ("4", "Abort the run"),
        ],
    )

    if choice == "1":
        print("📦 Stashing changes...")
        run_command("git stash push -u -m 'runner.py auto-stash'", cwd=cwd)
        print("✅ Changes stashed. They can be restored later with 'git stash pop'.")
    elif choice == "2":
        print("📦 Committing changes now via /commits skill...")
        if run_grouped_commits()[0]:
            print("✅ Changes committed.")
        else:
            if git_status(cwd):
                print("❌ Changes still remain uncommitted after /commits. Aborting run so nothing gets lost.")
                raise SystemExit(1)
    elif choice == "3":
        confirm = input("Type 'discard' to permanently discard these changes: ").strip().lower()
        if confirm != "discard":
            print("❌ Discard not confirmed. Aborting run.")
            raise SystemExit(1)
        print("🗑️  Discarding all local changes...")
        run_command("git checkout -- .", cwd=cwd, check=False)
        run_command("git clean -fd", cwd=cwd, check=False)
        print("✅ Working tree cleaned.")
    else:
        print("🛑 Aborting run at user's request.")
        raise SystemExit(0)


def current_branch(cwd=PROJECT_ROOT):
    return run_command("git rev-parse --abbrev-ref HEAD", cwd=cwd, check=False).stdout.strip()


def prepare_git(resuming):
    """Readies the checked-out branch for the run; tasks are committed straight onto it.

    With resuming=True the working tree is expected to hold the unfinished task's
    partial work, so it is kept as-is and nothing is pulled. Otherwise uncommitted
    changes would end up in the first task's commits, so the user is asked first.
    """
    # A fresh repo has no commits, so there is no branch to commit onto yet.
    if run_command("git rev-parse --verify HEAD", cwd=PROJECT_ROOT, check=False).returncode != 0:
        print("🌱 Repository has no commits; creating an empty initial commit on 'main'...")
        run_command("git checkout -B main", cwd=PROJECT_ROOT)
        run_command(["git", "commit", "--allow-empty", "-m", "chore: initial commit"], cwd=PROJECT_ROOT)

    branch = current_branch()
    if resuming:
        print(f"🧩 Resuming on '{branch}'; keeping the working tree (it may hold partial work).")
        return

    handle_dirty_worktree()
    print(f"⬇️  Pulling latest '{branch}' from remote (if any)...")
    pull_result = run_command(["git", "pull", "--ff-only", "origin", branch], cwd=PROJECT_ROOT, check=False)
    if pull_result.returncode != 0:
        print(f"ℹ️  Skipped pull (no remote configured yet, or nothing to pull): {pull_result.stderr.strip()}")


# Which CLI drives the agent: "claude" (Claude Code) or "opencode".
AGENT_CLI = os.environ.get("AGENT_CLI", "claude")

# AGENT_CLI_STDOUT=1 shows the agent CLI's stdout; any other value hides it.
AGENT_CLI_STDOUT = os.environ.get("AGENT_CLI_STDOUT", "0") == "1"

VERDICT_RE = re.compile(r"^VERDICT:\s*(PASS|FAIL)\b:?\s*(.*)$", re.MULTILINE)
COMMITS_RESULT_RE = re.compile(r"^RESULT:\s*(OK|PARTIAL|NOTHING_TO_COMMIT)\b:?\s*(.*)$", re.MULTILINE)
FIX_RESULT_RE = re.compile(r"^FIX_RESULT:\s*(FIXED|PARTIAL|NOTHING_TO_FIX|FAILED)\b:?\s*(.*)$", re.MULTILINE)

# How many /code-review-fixer rounds to run on a failing review before giving up.
MAX_FIX_ATTEMPTS = int(os.environ.get("MAX_FIX_ATTEMPTS", "1"))

# Optional guardrails for every claude call (opencode has no equivalent); unset means no limit.
MAX_TURNS = os.environ.get("MAX_TURNS") or None
MAX_BUDGET_USD = os.environ.get("MAX_BUDGET_USD") or None

# Per-stage model/effort overrides; unset means the CLI default. Models are claude
# aliases/ids (sonnet, opus, claude-haiku-5-5) or opencode provider/model ids
# (opencode/claude-sonnet-5-5). Effort is claude's --effort or opencode's --variant.
# Commits default to sonnet on claude.
MODEL_PLAN = os.environ.get("MODEL_PLAN") or None
MODEL_IMPLEMENT = os.environ.get("MODEL_IMPLEMENT") or None
MODEL_REVIEW = os.environ.get("MODEL_REVIEW") or None
MODEL_COMMITS = os.environ.get("MODEL_COMMITS", "sonnet" if AGENT_CLI == "claude" else "") or None
EFFORT_PLAN = os.environ.get("EFFORT_PLAN") or None
EFFORT_IMPLEMENT = os.environ.get("EFFORT_IMPLEMENT") or None
EFFORT_REVIEW = os.environ.get("EFFORT_REVIEW") or None
EFFORT_COMMITS = os.environ.get("EFFORT_COMMITS") or None

# Number of consecutive todos grouped into one implement call.
TODOS_PER_SESSION = max(1, int(os.environ.get("TODOS_PER_SESSION", "1")))


def agent_session_exists(session_id, cwd=WORKDIR):
    """Whether the agent CLI still has this session stored for cwd, so it can be resumed."""
    if AGENT_CLI == "opencode":
        result = run_command(["opencode", "session", "list", "--format", "json"], cwd=cwd, check=False)
        try:
            return any(session.get("id") == session_id for session in json.loads(result.stdout or "[]"))
        except ValueError:
            return False
    config_dir = Path(os.environ.get("CLAUDE_CONFIG_DIR") or Path.home() / ".claude")
    project = re.sub(r"[^A-Za-z0-9]", "-", str(cwd))
    return (config_dir / "projects" / project / f"{session_id}.jsonl").is_file()


def stream_json_events(cmd, cwd):
    """Runs cmd, yielding each JSON line of its stdout as it arrives. Returns (returncode, stderr)."""
    proc = subprocess.Popen(cmd, cwd=cwd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    for line in proc.stdout:
        try:
            yield json.loads(line)
        except ValueError:
            continue
    stderr = proc.stderr.read()
    proc.wait()
    return proc.returncode, stderr


def run_agent(prompt=None, command=None, args="", cwd=WORKDIR, plan=False,
              resume=None, fork=False, model=None, effort=None):
    """Runs the agent CLI headlessly with a free-form prompt or a skill/command.

    plan=True runs it in the CLI's read-only plan mode; resume continues a stored
    session (fork=True copies it first). Both CLIs stream JSON events, so
    AGENT_CLI_STDOUT=1 shows the agent's messages live. Returns (success, output,
    usage): output is the agent's final text, usage holds the session id, cost,
    tokens, turns and duration. stderr is shown when AGENT_CLI_STDOUT=1 or the call failed.
    """
    if AGENT_CLI == "opencode":
        return _run_opencode(prompt, command, args, cwd, plan, resume, fork, model, effort)
    return _run_claude(prompt, command, args, cwd, plan, resume, fork, model, effort)


def _run_claude(prompt, command, args, cwd, plan, resume, fork, model, effort):
    text = f"/{command} {args}".strip() if command else prompt
    mode = ["--permission-mode", "plan"] if plan else ["--dangerously-skip-permissions"]
    cmd = ["claude", "-p", text, *mode, "--output-format", "stream-json", "--verbose"]
    if resume:
        cmd += ["--resume", resume]
        if fork:
            cmd += ["--fork-session"]
    if model:
        cmd += ["--model", model]
    if effort:
        cmd += ["--effort", effort]
    if MAX_TURNS:
        cmd += ["--max-turns", MAX_TURNS]
    if MAX_BUDGET_USD:
        cmd += ["--max-budget-usd", MAX_BUDGET_USD]

    final = {}
    events = stream_json_events(cmd, cwd)
    while True:
        try:
            event = next(events)
        except StopIteration as stop:
            returncode, stderr = stop.value
            break
        if event.get("type") == "assistant" and AGENT_CLI_STDOUT:
            for block in event.get("message", {}).get("content", []):
                if block.get("type") == "text" and block.get("text"):
                    print(block["text"], flush=True)
        elif event.get("type") == "result":
            final = event

    ok = returncode == 0 and bool(final) and not final.get("is_error")
    if not ok and final.get("subtype") not in (None, "success"):
        print(f"⚠️  Agent stopped: {final['subtype']}")
    if stderr and (AGENT_CLI_STDOUT or not ok):
        print(stderr)
    u = final.get("usage") or {}
    usage = {
        "model": model,
        "cost_usd": final.get("total_cost_usd"),
        "duration_s": round((final.get("duration_ms") or 0) / 1000, 1),
        "num_turns": final.get("num_turns"),
        "session_id": final.get("session_id"),
        "input_tokens": u.get("input_tokens", 0),
        "output_tokens": u.get("output_tokens", 0),
        "cache_creation_input_tokens": u.get("cache_creation_input_tokens", 0),
        "cache_read_input_tokens": u.get("cache_read_input_tokens", 0),
    } if final else None
    return ok, final.get("result") or "", usage


def _run_opencode(prompt, command, args, cwd, plan, resume, fork, model, effort):
    # MAX_TURNS / MAX_BUDGET_USD have no opencode equivalent; permissions follow opencode's config.
    cmd = ["opencode", "run", "--format", "json"]
    if plan:
        cmd += ["--agent", "plan"]
    if resume:
        cmd += ["--session", resume]
        if fork:
            cmd += ["--fork"]
    if model:
        cmd += ["--model", model]
    if effort:
        cmd += ["--variant", effort]
    if command:
        cmd += ["--command", command]
        if args:
            cmd.append(args)
    else:
        cmd.append(prompt)

    started = datetime.now(timezone.utc)
    texts, error, session_id, steps = [], None, None, 0
    tokens = {"input": 0, "output": 0, "cache_read": 0, "cache_write": 0}
    cost = 0.0
    events = stream_json_events(cmd, cwd)
    while True:
        try:
            event = next(events)
        except StopIteration as stop:
            returncode, stderr = stop.value
            break
        session_id = event.get("sessionID") or session_id
        part = event.get("part") or {}
        kind = event.get("type")
        if kind == "text" and part.get("text"):
            texts.append(part["text"])
            if AGENT_CLI_STDOUT:
                print(part["text"], flush=True)
        elif kind == "tool_use" and AGENT_CLI_STDOUT:
            print(f"🔧 {part.get('tool')}", flush=True)
        elif kind == "step_finish":
            steps += 1
            cost += part.get("cost") or 0
            t = part.get("tokens") or {}
            tokens["input"] += t.get("input", 0)
            tokens["output"] += t.get("output", 0) + t.get("reasoning", 0)
            tokens["cache_read"] += (t.get("cache") or {}).get("read", 0)
            tokens["cache_write"] += (t.get("cache") or {}).get("write", 0)
        elif kind == "error":
            error = (event.get("error") or {}).get("data", {}).get("message") or str(event.get("error"))

    ok = returncode == 0 and error is None
    if error:
        print(f"⚠️  Agent error: {error}")
    if stderr and (AGENT_CLI_STDOUT or not ok):
        print(stderr)
    usage = {
        "model": model,
        "cost_usd": round(cost, 6),
        "duration_s": round((datetime.now(timezone.utc) - started).total_seconds(), 1),
        "num_turns": steps,
        "session_id": session_id,
        "input_tokens": tokens["input"],
        "output_tokens": tokens["output"],
        "cache_creation_input_tokens": tokens["cache_write"],
        "cache_read_input_tokens": tokens["cache_read"],
    } if session_id else None
    return ok, "\n".join(texts), usage


def run_code_review(task_filename, attempt=0, recheck_report=None):
    """Runs the /code-review skill and gates on its VERDICT line.

    Returns (passed, reason, report_path, usage). Every report is saved to reviews/ so
    /code-review-fixer can read it; reviews/ is gitignored so the next review
    doesn't pick up its own previous report as a change. With recheck_report, the
    review first checks whether that previous report's findings were fixed.
    """
    print(f"\n🔍 Running automated Code Review agent (round {attempt + 1})...")
    args = ""
    if recheck_report is not None:
        rel = recheck_report
        try:
            rel = recheck_report.relative_to(PROJECT_ROOT)
        except (ValueError, AttributeError):
            pass
        args = f"--recheck {rel}"
    ok, output, usage = run_agent(command="code-review", args=args, cwd=PROJECT_ROOT,
                                  model=MODEL_REVIEW, effort=EFFORT_REVIEW)

    # Use the last VERDICT line, in case the report quotes the format earlier.
    matches = VERDICT_RE.findall(output)
    if not ok or not matches:
        verdict, reason = "FAIL", "review agent crashed or produced no VERDICT line"
    else:
        verdict, reason = matches[-1]

    print(f"🧾 Code review verdict: {verdict}" + (f" — {reason}" if reason else ""))

    report_path = REVIEWS_DIR / f"{Path(task_filename).stem}.review.{attempt + 1}.md"
    report_path.write_text(output, encoding="utf-8")
    print(f"📝 Review report saved to {report_path}")
    return verdict == "PASS", reason, report_path, usage


def run_code_review_fixer(report_path):
    """Runs the /code-review-fixer skill on a review report.

    Returns (changed, usage); changed is True when the fixer changed something worth
    re-reviewing (FIXED or PARTIAL).
    """
    print("\n🛠️  Applying review findings via /code-review-fixer skill...")
    rel_report = report_path.relative_to(PROJECT_ROOT)
    ok, output, usage = run_agent(command="code-review-fixer", args=str(rel_report), cwd=PROJECT_ROOT,
                                  model=MODEL_IMPLEMENT, effort=EFFORT_IMPLEMENT)

    matches = FIX_RESULT_RE.findall(output)
    status, reason = matches[-1] if matches else ("UNKNOWN", "fixer produced no FIX_RESULT line")
    print(f"🧾 Fixer result: {status}" + (f" — {reason}" if reason else ""))
    return ok and status in ("FIXED", "PARTIAL"), usage


def review_with_fixes(tracker, task_name, task_filename):
    """Reviews the change, running /code-review-fixer and re-reviewing on FAIL.

    Returns (passed, reason). The last failing report is copied next to the task in errors/.
    """
    attempt = 0
    previous_report = None
    while True:
        passed, reason, report_path, usage = run_code_review(task_filename, attempt, recheck_report=previous_report)
        if tracker is not None and task_name is not None:
            tracker.add_usage(task_name, {"stage": "review", "todo": None, "attempt": attempt + 1, **{k: v for k, v in (usage or {}).items()}})
        if passed:
            return True, ""
        if attempt >= MAX_FIX_ATTEMPTS:
            print(f"🛑 Review still failing after {MAX_FIX_ATTEMPTS} fix round(s).")
            break
        changed, fix_usage = run_code_review_fixer(report_path)
        if tracker is not None and task_name is not None:
            tracker.add_usage(task_name, {"stage": "fixer", "todo": None, "attempt": attempt + 1, **{k: v for k, v in (fix_usage or {}).items()}})
        if not changed:
            print("🛑 Fixer made no progress; not re-reviewing.")
            break
        previous_report = report_path
        attempt += 1

    error_report = ERRORS_DIR / f"{Path(task_filename).stem}.review.md"
    shutil.copyfile(report_path, error_report)
    print(f"📝 Final review report copied to {error_report}")
    return False, reason


def push_current_branch():
    """Pushes the checked-out branch to origin."""
    branch = current_branch()
    print(f"\n⬆️  Pushing '{branch}' to remote...")
    result = run_command(["git", "push", "origin", branch], cwd=PROJECT_ROOT, check=False)
    if result.returncode != 0:
        print(f"⚠️  Failed to push '{branch}':\n{result.stderr.strip()}")
        return False
    print(f"✅ Pushed '{branch}' to remote.")
    return True


# Trailer added to every commit of a task, so `git log --grep "Task: <id>"` finds them all.
TASK_TRAILER = "Task: {task_id}"


def run_grouped_commits(task_name=None, plan=None):
    """Triggers the /commits skill to group and commit remaining changes.

    With task_name, every commit gets the task's trailer in its footer.
    plan (list of (number, done, text)) is passed so commits group by todo.
    Returns (success, usage).
    """
    print("\n📦 Grouping and committing changes via /commits skill...")
    args_parts = []
    if task_name:
        trailer = TASK_TRAILER.format(task_id=extract_task_id(task_name))
        args_parts.append(f'Add the trailer line "{trailer}" as the last line of every commit message footer.')
    if plan:
        plan_text = "; ".join(f"{number}. {text}" for number, _, text in plan)
        args_parts.append(f"Group commits by these todos (one commit per todo where possible): {plan_text}")
    args = " ".join(args_parts)
    ok, output, usage = run_agent(command="commits", args=args, cwd=PROJECT_ROOT,
                                  model=MODEL_COMMITS, effort=EFFORT_COMMITS)

    matches = COMMITS_RESULT_RE.findall(output)
    status = matches[-1][0] if matches else "UNKNOWN"
    print(f"🧾 Commits result: {status}")
    leftover = git_status()
    if leftover:
        print(f"⚠️  Uncommitted changes remain after /commits:\n{leftover}")
    return ok and status in ("OK", "NOTHING_TO_COMMIT"), usage

# ---------------------------------------------------------------------------
# Progress tracking: lets an interrupted or failed run be resumed where it stopped.
# ---------------------------------------------------------------------------

# Set RUNNER_RESUME=0 to archive any unfinished progress and start from scratch.
RESUME = os.environ.get("RUNNER_RESUME", "1") != "0"

# Set RUNNER_PUSH=1 to push the branch once the run has committed at least one task.
PUSH = os.environ.get("RUNNER_PUSH", "0") == "1"

TRACK_FILE_RE = re.compile(r"^(\d+)_error\.json(\.archive)?$")
ARCHIVE_SUFFIX = ".archive"
# Stages after the todos, in pipeline order. "plan" comes before the todos.
STAGES = ("plan", "review", "commit")


def now_iso():
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


class Tracker:
    """In-memory progress of every task, mirrored to errors/track/<n>_error.json.

    Shape: {"run": {...}, "tasks": {<task file name>: {"file", "status",
    "todos": {"<n>": {"started", "finished"}}, "stages": {<stage>: bool},
    "last_error"}}}. Task file names are the keys because task ids
    alone aren't unique (e.g. 000001_scaffold_program and 000001_cleanup).

    The file is rewritten atomically after every change, so even a hard kill
    loses at most the step in flight. Each run writes its own file, numbered one
    past every existing one; a run starts from the newest non-archived file.
    """

    def __init__(self):
        track_files = self._track_files()
        active = sorted((n, path) for n, path, archived in track_files if not archived)
        self.tasks = {}
        resumed_from = None

        if active and not RESUME:
            print("🧹 RUNNER_RESUME=0: archiving unfinished progress and starting fresh.")
            self._archive(path for _, path in active)
        else:
            # Newest first; skip any file that can't be read rather than losing the run.
            for _, path in reversed(active):
                try:
                    self.tasks = json.loads(path.read_text(encoding="utf-8")).get("tasks", {})
                    resumed_from = path.name
                    break
                except (OSError, ValueError) as exc:
                    print(f"⚠️  Ignoring unreadable track file {path.name}: {exc}")

        self.number = max((n for n, _, _ in track_files), default=0) + 1
        self.path = TRACK_DIR / f"{self.number}_error.json"
        self.run = {
            "number": self.number,
            "started_at": now_iso(),
            "resumed_from": resumed_from,
            "current": None,
            "error": None,
        }
        if resumed_from:
            unfinished = [name for name, entry in self.tasks.items() if entry.get("status") != "complete"]
            print(f"🔁 Resuming from {resumed_from}; unfinished: {', '.join(unfinished) or 'none'}")

    @staticmethod
    def _track_files():
        """Returns (n, path, archived) for every track file."""
        files = []
        for path in TRACK_DIR.iterdir():
            match = TRACK_FILE_RE.match(path.name)
            if match:
                files.append((int(match.group(1)), path, bool(match.group(2))))
        return files

    @staticmethod
    def _archive(paths):
        for path in paths:
            path.rename(path.with_name(path.name + ARCHIVE_SUFFIX))
            print(f"🗄️  Archived {path.name}{ARCHIVE_SUFFIX}")

    def save(self):
        tmp = self.path.with_name(self.path.name + ".tmp")
        tmp.write_text(json.dumps({"run": self.run, "tasks": self.tasks}, indent=2) + "\n", encoding="utf-8")
        os.replace(tmp, self.path)

    def entry(self, task_path):
        """Returns the task's entry, creating it on first sight, and records where its file is."""
        entry = self.tasks.setdefault(task_path.name, {
            "file": None,
            "status": "in_progress",
            "todos": {},
            "stages": {stage: False for stage in STAGES},
            "last_error": None,
            "session_id": None,
            "usage": [],
        })
        entry.setdefault("session_id", None)
        entry.setdefault("usage", [])
        entry["file"] = str(task_path.relative_to(PROJECT_ROOT))
        self.save()
        return entry

    def set_current(self, task_name, stage, todo=None):
        self.run["current"] = {"task": task_name, "stage": stage, "todo": todo}
        self.save()

    def init_todos(self, task_name, plan_items):
        """Makes sure every plan item has a todo entry; ticked items count as finished."""
        todos = self.tasks[task_name]["todos"]
        for number, done, _ in plan_items:
            todo = todos.setdefault(str(number), {"started": done, "finished": done})
            if done:
                todo.update(started=True, finished=True)
        self.save()

    def mark_todo(self, task_name, number, **flags):
        self.tasks[task_name]["todos"][str(number)].update(flags)
        print(f"🗂️  {task_name} todo #{number}: {self.tasks[task_name]['todos'][str(number)]}")
        self.save()

    def mark_stage(self, task_name, stage):
        self.tasks[task_name]["stages"][stage] = True
        self.save()

    def add_usage(self, task_name, record):
        entry = self.tasks.get(task_name)
        if entry is None:
            return
        entry.setdefault("usage", []).append(record)
        self.save()

    def fail(self, task_name, reason):
        entry = self.tasks[task_name]
        entry.update(status="failed", last_error=reason)
        self.save()

    def complete(self, task_name):
        entry = self.tasks[task_name]
        entry.update(status="complete", last_error=None)
        self.run["current"] = None
        self.save()

    def record_error(self, exc):
        self.run["error"] = {
            "at": now_iso(),
            "type": type(exc).__name__,
            "message": str(exc),
            "traceback": traceback.format_exc(),
        }
        current = self.run.get("current")
        if current and current["task"] in self.tasks:
            where = f"stage '{current['stage']}'" + (f", todo #{current['todo']}" if current["todo"] else "")
            self.tasks[current["task"]]["last_error"] = f"{type(exc).__name__} during {where}: {exc}"
        self.save()

    def archive_if_complete(self):
        """Archives every active track file once all tracked tasks are complete."""
        if any(entry.get("status") != "complete" for entry in self.tasks.values()):
            return False
        if not self.tasks:
            return True
        active = [path for _, path, archived in self._track_files() if not archived]
        if active:
            print("\n🎉 Every tracked task is complete.")
            self._archive(active)
        return True


# ---------------------------------------------------------------------------
# Plan section of the task file.
# ---------------------------------------------------------------------------

# Separates the task as written by the user from the plan the runner appends.
PLAN_MARKER = "<!-- RUNNER:PLAN -->"
PLAN_BLOCK_RE = re.compile(r"^PLAN_BEGIN\s*$(.*?)^PLAN_END\s*$", re.MULTILINE | re.DOTALL)
PLAN_OUTPUT_ITEM_RE = re.compile(r"^\s*\d+[.)]\s+(.+?)\s*$")
PLAN_FILE_ITEM_RE = re.compile(r"^(\d+)\.\s+\[( |x|X)\]\s+(.+?)\s*$")

PLAN_PROMPT = """You are PLANNING a task, not implementing it. Explore the repository as needed, but do not modify any files.

Break the task below into 3-6 todos, in execution order. Each todo should cover a coherent slice of the work (for example data layer, handlers, templates, tests, docs) and be implementable on its own, and together they must cover the whole task. Avoid many tiny todos.

Finish by printing the plan between a line containing only PLAN_BEGIN and a line containing only PLAN_END, one todo per line formatted as "<n>. <todo>". Put nothing else between the markers.

--- TASK (file: {task_file}) ---
{task}
"""

IMPLEMENT_PROMPT = """You are implementing one or more steps of a multi-step task that a runner drives step by step. Implement only the current todo(s) below, then stop. Do not commit, and do not edit the task file; the runner tracks progress itself.

--- TASK (file: {task_file}) ---
{task}

--- PLAN ---
{plan}

--- CURRENT TODO(S) ---
{current_todos}
{resume_note}"""

RESUME_NOTE = """
--- RESUMING ---
A previous attempt at this todo did not finish{error}. The working tree may already hold part of its work: check `git status` and `git diff` first and continue from there rather than starting over.
"""


def split_plan(content):
    """Splits task file content into (task body, plan items or None).

    Plan items are (number, done, text) tuples.
    """
    if PLAN_MARKER not in content:
        return content.strip(), None
    body, plan = content.split(PLAN_MARKER, 1)
    items = [
        (int(match.group(1)), match.group(2) != " ", match.group(3))
        for match in map(PLAN_FILE_ITEM_RE.match, plan.splitlines()) if match
    ]
    return body.strip(), items


def parse_plan_output(output):
    """Extracts the todos from the last PLAN_BEGIN/PLAN_END block, renumbered from 1."""
    blocks = PLAN_BLOCK_RE.findall(output)
    if not blocks:
        return []
    texts = [match.group(1) for match in map(PLAN_OUTPUT_ITEM_RE.match, blocks[-1].splitlines()) if match]
    return [(number, False, text) for number, text in enumerate(texts, start=1)]


def write_plan(task_path, body, items):
    """Rewrites the task file as the task body, the plan marker and a numbered checklist."""
    lines = [f"{number}. [{'x' if done else ' '}] {text}" for number, done, text in items]
    task_path.write_text(f"{body}\n\n{PLAN_MARKER}\n## Plan\n\n" + "\n".join(lines) + "\n", encoding="utf-8")


def render_plan(items, todos, current):
    """Renders the plan with each todo's status, for the implementation prompt."""
    if isinstance(current, int):
        current_set = {current}
    else:
        current_set = set(current or [])
    lines = []
    for number, _, text in items:
        todo = todos[str(number)]
        if todo["finished"]:
            status = "done"
        elif number in current_set:
            status = "CURRENT"
        elif todo["started"]:
            status = "interrupted"
        else:
            status = "pending"
        lines.append(f"{number}. [{status}] {text}")
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# Task pipeline: plan -> todos -> review -> commit, straight onto the checked-out branch.
# ---------------------------------------------------------------------------

class TaskFailed(Exception):
    """A pipeline step failed; the task is parked in errors/, the run stops, and the next run resumes it."""


def locate_task(name):
    """Finds a task file by name in tasks/, errors/ or finished/ (it moves between them)."""
    for directory in (TASKS_DIR, ERRORS_DIR, FINISHED_DIR):
        if (directory / name).is_file():
            return directory / name
    return None


def collect_tasks(tracker):
    """Returns pending tasks plus unfinished tracked ones, the unfinished ones first.

    An unfinished task's partial work is still in the working tree, so it must be
    resumed (and committed) before any other task adds changes on top of it.
    """
    names = {f.name for f in TASKS_DIR.iterdir() if f.is_file() and f.suffix in [".md", ".txt"]}
    unfinished = {name for name, entry in tracker.tasks.items() if entry.get("status") != "complete"}
    return sorted(names | unfinished, key=lambda name: (name not in unfinished, name))


def plan_task(tracker, task_path, body):
    print(f"\n🧭 Planning '{task_path.name}' in plan mode...")
    tracker.set_current(task_path.name, "plan")
    entry = tracker.tasks[task_path.name]
    ok, output, usage = run_agent(
        prompt=PLAN_PROMPT.format(task_file=task_path.relative_to(PROJECT_ROOT), task=body),
        plan=True,
        model=MODEL_PLAN,
        effort=EFFORT_PLAN,
    )
    tracker.add_usage(task_path.name, {"stage": "plan", "todo": None, **(usage or {})})
    # Implement calls fork this session, so they start with the planner's exploration.
    entry["session_id"] = (usage or {}).get("session_id")
    tracker.save()
    items = parse_plan_output(output)
    if not ok or not items:
        raise TaskFailed("planning failed: agent crashed or printed no PLAN_BEGIN/PLAN_END todo list")
    write_plan(task_path, body, items)
    tracker.mark_stage(task_path.name, "plan")
    print(f"📝 Plan with {len(items)} todo(s) written to {task_path.name}.")
    return items


def implement_todos(tracker, task_path, body, items):
    name = task_path.name
    entry = tracker.tasks[name]
    # Each implement call forks the planning session, so it starts with the plan's exploration.
    session_id = entry.get("session_id")
    if session_id and not agent_session_exists(session_id):
        print(f"ℹ️  Planning session {session_id} not found; implementing without it.")
        session_id = None
    # Build list of pending todos in order.
    pending = [(number, text) for (number, done, text) in items
               if not entry["todos"][str(number)]["finished"]]
    # Group into chunks of TODOS_PER_SESSION.
    chunks = [pending[i:i + TODOS_PER_SESSION] for i in range(0, len(pending), TODOS_PER_SESSION)]
    for chunk in chunks:
        numbers = [n for n, _ in chunk]
        texts = [t for _, t in chunk]
        resume_note = ""
        if any(entry["todos"][str(n)]["started"] for n in numbers):
            error = f" (last error: {entry['last_error']})" if entry.get("last_error") else ""
            resume_note = RESUME_NOTE.format(error=error)
        current_todos_block = "\n".join(f"#{n}: {t}" for n, t in chunk)
        prompt = IMPLEMENT_PROMPT.format(
            task_file=task_path.relative_to(PROJECT_ROOT),
            task=body,
            plan=render_plan(items, entry["todos"], numbers),
            current_todos=current_todos_block,
            resume_note=resume_note,
        )
        for n in numbers:
            tracker.set_current(name, "implement", n)
            tracker.mark_todo(name, n, started=True)
        success, _, usage = run_agent(
            prompt=prompt,
            resume=session_id,
            fork=True if session_id else False,
            model=MODEL_IMPLEMENT,
            effort=EFFORT_IMPLEMENT,
        )
        tracker.add_usage(name, {"stage": "implement", "todo": numbers if len(numbers) > 1 else numbers[0],
                                **{k: v for k, v in (usage or {}).items()}})
        if not success:
            raise TaskFailed(f"implementation of todo(s) {numbers} failed")

        for n in numbers:
            tracker.mark_todo(name, n, finished=True)
        for index, (number, _, text) in enumerate(items):
            if number in numbers:
                items[index] = (number, True, text)
        write_plan(task_path, body, items)
        print(f"✅ Todo(s) {numbers} done.")


def run_task(tracker, name):
    """Runs (or resumes) one task through the whole pipeline.

    Returns True once committed, False for a skipped empty task. Raises TaskFailed on failure.
    """
    task_path = locate_task(name)
    if task_path is None:
        raise TaskFailed(f"task file '{name}' not found in tasks/, errors/ or finished/")
    if not task_path.read_text(encoding="utf-8").strip():
        print(f"⚠️  Empty task {name}. Skipping...")
        return False
    entry = tracker.entry(task_path)
    entry["status"] = "in_progress"

    # 1. Plan in plan mode, or reuse the plan already in the task file
    body, items = split_plan(task_path.read_text(encoding="utf-8"))
    if not items:
        items = plan_task(tracker, task_path, body)
    else:
        print(f"📋 Reusing the {len(items)}-todo plan already in {name}.")
        tracker.mark_stage(name, "plan")
    tracker.init_todos(name, items)
    # The tracker is the source of truth; keep the checkboxes in line with it.
    items = [(number, entry["todos"][str(number)]["finished"], text) for number, _, text in items]

    # 2. Implement the todos one agent call at a time
    implement_todos(tracker, task_path, body, items)

    # 3. Code review (with fix rounds)
    if not entry["stages"]["review"]:
        tracker.set_current(name, "review")
        if name.endswith(".ncr.md"):
            print(f"⏭️  Skipping code review for {name} (.ncr.md)")
        else:
            review_passed, reason = review_with_fixes(tracker, name, name)
            if not review_passed:
                raise TaskFailed(f"code review failed: {reason}")
        tracker.mark_stage(name, "review")

    # 4. Commit, with the task file (plan fully ticked) moved into finished/ as part of it
    if not entry["stages"]["commit"]:
        tracker.set_current(name, "commit")
        write_plan(task_path, body, [(number, True, text) for number, _, text in items])
        if task_path.parent != FINISHED_DIR:
            task_path = move_task(task_path, FINISHED_DIR)
            entry = tracker.entry(task_path)
        committed, commit_usage = run_grouped_commits(name, items)
        tracker.add_usage(name, {"stage": "commits", "todo": None, **{k: v for k, v in (commit_usage or {}).items()}})
        if not committed:
            raise TaskFailed("commit issues")
        tracker.mark_stage(name, "commit")

    tracker.complete(name)
    return True


def park_failed_task(tracker, name, reason):
    """Records the failure and moves the task file to errors/; its partial work stays uncommitted."""
    tracker.fail(name, reason)
    task_path = locate_task(name)
    # The commit stage may already have moved it into finished/; leave it there.
    if task_path and task_path.parent == TASKS_DIR:
        tracker.entry(move_task(task_path, ERRORS_DIR))


def print_task_cost(tracker, name):
    entry = tracker.tasks.get(name) or {}
    usage = entry.get("usage") or []
    if not usage:
        return
    cost = sum((u.get("cost_usd") or 0) for u in usage)
    inp = sum((u.get("input_tokens") or 0) for u in usage)
    out = sum((u.get("output_tokens") or 0) for u in usage)
    cache = sum((u.get("cache_read_input_tokens") or 0) for u in usage)
    turns = sum((u.get("num_turns") or 0) for u in usage)
    print(f"💰 {name}: ${cost:.4f}, {len(usage)} session(s), {turns} turn(s), "
          f"in={inp} out={out} cache_read={cache}")


def process_tasks(tracker):
    names = collect_tasks(tracker)
    if not names:
        print("✨ No pending tasks found.")
        tracker.archive_if_complete()
        return

    print(f"📋 Found {len(names)} task(s) to process: {', '.join(names)}")
    resuming = any(entry.get("status") != "complete" for entry in tracker.tasks.values())
    prepare_git(resuming)

    completed, failed = [], []

    for name in names:
        print("\n" + "=" * 60)
        print(f"📋 Task: {name}")
        print("=" * 60)

        try:
            if run_task(tracker, name):
                print(f"✅ Completed: {name}")
                completed.append(name)
        except TaskFailed as exc:
            print(f"❌ {name}: {exc}")
            if name in tracker.tasks:
                park_failed_task(tracker, name, str(exc))
            failed.append(name)
        except Exception as exc:
            print(f"💥 Unexpected error while processing {name}: {exc}")
            if name in tracker.tasks:
                tracker.record_error(exc)
                park_failed_task(tracker, name, f"{type(exc).__name__}: {exc}")
            failed.append(name)
        if name in tracker.tasks:
            print_task_cost(tracker, name)
        if failed:
            # Later tasks may build on this one, and its partial work is still uncommitted.
            print("🛑 Stopping the run; fix or end the failed task, then re-run to resume it.")
            break

    skipped = names[names.index(failed[0]) + 1:] if failed else []
    print("\n" + "=" * 60)
    print(f"🏁 Run finished: {len(completed)} completed, {len(failed)} failed, {len(skipped)} not started.")
    if completed:
        print(f"   ✅ {', '.join(completed)}")
    if failed:
        print(f"   ❌ {', '.join(failed)}")
    if skipped:
        print(f"   ⏸️  {', '.join(skipped)}")
    if completed and PUSH:
        push_current_branch()
    # Overall cost summary.
    total_cost = 0.0
    total_sessions = 0
    for entry in tracker.tasks.values():
        for u in (entry.get("usage") or []):
            total_cost += (u.get("cost_usd") or 0)
            total_sessions += 1
    if total_sessions:
        print(f"💰 Run total: ${total_cost:.4f} across {total_sessions} session(s).")
    if not tracker.archive_if_complete():
        print(f"   💾 Progress saved to {tracker.path.relative_to(PROJECT_ROOT)}; re-run to resume.")
    print("=" * 60)


class RunnerInterrupted(BaseException):
    """Raised from SIGTERM/SIGHUP so they go through the same rescue as Ctrl-C."""


def _raise_interrupted(signum, _frame):
    raise RunnerInterrupted(f"received {signal.Signals(signum).name}")


def main():
    tracker = Tracker()
    for sig in (signal.SIGTERM, signal.SIGHUP):
        signal.signal(sig, _raise_interrupted)

    try:
        process_tasks(tracker)
    except BaseException as exc:
        # Global rescue: record where and why the run stopped so the next run resumes there.
        for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            signal.signal(sig, signal.SIG_IGN)
        exit_code = exc.code if isinstance(exc, SystemExit) else 1
        if not (isinstance(exc, SystemExit) and exc.code in (0, None)):
            print(f"\n💥 Run stopped: {type(exc).__name__}: {exc}")
        if tracker.tasks:
            tracker.record_error(exc)
            print(f"💾 Progress saved to {tracker.path.relative_to(PROJECT_ROOT)}; re-run to resume.")
        raise SystemExit(exit_code)


if __name__ == "__main__":
    main()
