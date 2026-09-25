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
        choice = input("Your choice: ").strip().lower()
        if choice in valid_keys:
            return choice
        print(f"⚠️  Invalid choice '{choice}'. Please pick one of: {', '.join(sorted(valid_keys))}")


def handle_dirty_worktree(cwd=PROJECT_ROOT):
    """Detects uncommitted changes and asks the user how to proceed before switching branches.

    Runs at the start of every branch preparation step: an interrupted previous
    run (implementation failures currently leave changes uncommitted) or manual
    edits could otherwise be silently carried into the wrong branch, or block
    the upcoming 'git checkout main'.
    """
    status = git_status(cwd)
    if not status:
        print("🧹 Working tree is clean.")
        return

    print("\n⚠️  Uncommitted changes detected in the working tree:")
    print(status)

    choice = prompt_user_choice(
        "How would you like to proceed before switching branches?",
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
        if run_grouped_commits():
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


def task_branch(task_filename):
    return f"feat/{extract_task_id(task_filename)}"


def current_branch(cwd=PROJECT_ROOT):
    return run_command("git rev-parse --abbrev-ref HEAD", cwd=cwd, check=False).stdout.strip()


def branch_exists(branch_name, cwd=PROJECT_ROOT):
    return run_command(["git", "rev-parse", "--verify", "--quiet", f"refs/heads/{branch_name}"], cwd=cwd, check=False).returncode == 0


def prepare_git_branch(task_filename, resume=False):
    """Syncs main repo and creates a dedicated feat/<task_id> branch.

    With resume=True and an existing branch, the branch is checked out as-is
    instead of being recreated from main, so an interrupted run's commits and
    (when already on it) uncommitted changes are kept.
    """
    branch_name = task_branch(task_filename)

    if resume and branch_exists(branch_name):
        print(f"\n🔁 Resuming on existing branch '{branch_name}' for task '{task_filename}'...")
        if current_branch() == branch_name:
            print("🧩 Already on it; keeping the working tree (it may hold partial work).")
        else:
            handle_dirty_worktree()
            run_command(["git", "checkout", branch_name])
        return branch_name

    print(f"\n🔄 Preparing Git branch '{branch_name}' for task '{task_filename}'...")

    # Guard against losing in-progress work: ask the user how to proceed
    # if the working tree has uncommitted changes before switching branches.
    handle_dirty_worktree()

    # A fresh repo has no commits, so "main" doesn't exist yet and can't be checked out.
    # Give it an empty root commit to branch from.
    if run_command("git rev-parse --verify HEAD", check=False).returncode != 0:
        print("🌱 Repository has no commits; creating an empty initial commit on 'main'...")
        run_command("git checkout -B main")
        run_command(["git", "commit", "--allow-empty", "-m", "chore: initial commit"])

    print("📍 Checking out 'main'...")
    run_command("git checkout main")

    print("⬇️  Pulling latest 'main' from remote (if any)...")
    pull_result = run_command("git pull origin main", check=False)
    if pull_result.returncode != 0:
        print(f"ℹ️  Skipped pull (no remote configured yet, or nothing to pull): {pull_result.stderr.strip()}")

    print(f"🌿 Creating/checking out feature branch '{branch_name}'...")
    run_command(f"git checkout -B {branch_name}")
    print(f"✅ Now on branch '{branch_name}'.")

    return branch_name


# Which CLI drives the agent: "claude" (Claude Code) or "opencode".
AGENT_CLI = os.environ.get("AGENT_CLI", "claude")

# AGENT_CLI_STDOUT=1 shows the agent CLI's stdout; any other value hides it.
AGENT_CLI_STDOUT = os.environ.get("AGENT_CLI_STDOUT", "0") == "1"

VERDICT_RE = re.compile(r"^VERDICT:\s*(PASS|FAIL)\b:?\s*(.*)$", re.MULTILINE)
COMMITS_RESULT_RE = re.compile(r"^RESULT:\s*(OK|PARTIAL|NOTHING_TO_COMMIT)\b:?\s*(.*)$", re.MULTILINE)
FIX_RESULT_RE = re.compile(r"^FIX_RESULT:\s*(FIXED|PARTIAL|NOTHING_TO_FIX|FAILED)\b:?\s*(.*)$", re.MULTILINE)

# How many /code-review-fixer rounds to run on a failing review before giving up.
MAX_FIX_ATTEMPTS = int(os.environ.get("MAX_FIX_ATTEMPTS", "2"))


def run_agent(prompt=None, command=None, args="", cwd=WORKDIR, capture=False, plan=False):
    """Runs the agent CLI headlessly with a free-form prompt or a skill/command.

    plan=True runs it in the CLI's read-only plan mode. Returns (success, stdout).
    stdout is only captured when capture=True. The CLI's stdout is only shown
    when AGENT_CLI_STDOUT=1; stderr is always shown, except when capture=True,
    where it is shown only if AGENT_CLI_STDOUT=1 or the command failed.
    """
    if AGENT_CLI == "opencode":
        cmd = ["opencode", "run"]
        if plan:
            cmd += ["--agent", "plan"]
        if command:
            cmd += ["--command", command]
            if args:
                cmd.append(args)
        else:
            cmd.append(prompt)
    else:
        text = f"/{command} {args}".strip() if command else prompt
        mode = ["--permission-mode", "plan"] if plan else ["--dangerously-skip-permissions"]
        cmd = ["claude", "-p", text, *mode]

    if capture:
        result = subprocess.run(cmd, cwd=cwd, text=True, capture_output=True)
        if AGENT_CLI_STDOUT:
            print(result.stdout)
        if result.stderr and (AGENT_CLI_STDOUT or result.returncode != 0):
            print(result.stderr)
    else:
        stdout = None if AGENT_CLI_STDOUT else subprocess.DEVNULL
        result = subprocess.run(cmd, cwd=cwd, text=True, stdout=stdout)
    return result.returncode == 0, result.stdout or ""


def run_code_review(task_filename, attempt=0):
    """Runs the /code-review skill and gates on its VERDICT line.

    Returns (passed, reason, report_path). Every report is saved to reviews/ so
    /code-review-fixer can read it; reviews/ is gitignored so the next review
    doesn't pick up its own previous report as a change.
    """
    print(f"\n🔍 Running automated Code Review agent (round {attempt + 1})...")
    ok, output = run_agent(command="code-review", cwd=PROJECT_ROOT, capture=True)

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
    return verdict == "PASS", reason, report_path


def run_code_review_fixer(report_path):
    """Runs the /code-review-fixer skill on a review report.

    Returns True when the fixer changed something worth re-reviewing (FIXED or PARTIAL).
    """
    print("\n🛠️  Applying review findings via /code-review-fixer skill...")
    rel_report = report_path.relative_to(PROJECT_ROOT)
    ok, output = run_agent(command="code-review-fixer", args=str(rel_report), cwd=PROJECT_ROOT, capture=True)

    matches = FIX_RESULT_RE.findall(output)
    status, reason = matches[-1] if matches else ("UNKNOWN", "fixer produced no FIX_RESULT line")
    print(f"🧾 Fixer result: {status}" + (f" — {reason}" if reason else ""))
    return ok and status in ("FIXED", "PARTIAL")


def review_with_fixes(task_filename):
    """Reviews the change, running /code-review-fixer and re-reviewing on FAIL.

    Returns (passed, reason). The last failing report is copied next to the task in errors/.
    """
    attempt = 0
    while True:
        passed, reason, report_path = run_code_review(task_filename, attempt)
        if passed:
            return True, ""
        if attempt >= MAX_FIX_ATTEMPTS:
            print(f"🛑 Review still failing after {MAX_FIX_ATTEMPTS} fix round(s).")
            break
        if not run_code_review_fixer(report_path):
            print("🛑 Fixer made no progress; not re-reviewing.")
            break
        attempt += 1

    error_report = ERRORS_DIR / f"{Path(task_filename).stem}.review.md"
    shutil.copyfile(report_path, error_report)
    print(f"📝 Final review report copied to {error_report}")
    return False, reason


def push_branch(branch_name):
    """Pushes the branch to the remote, setting upstream with -u."""
    print(f"\n⬆️  Pushing branch '{branch_name}' to remote...")
    result = run_command(f"git push -u origin {branch_name}", cwd=PROJECT_ROOT, check=False)
    if result.returncode != 0:
        print(f"⚠️  Failed to push branch '{branch_name}':\n{result.stderr.strip()}")
        return False
    print(f"✅ Pushed '{branch_name}' to remote (upstream set).")
    return True


def merge_to_main(branch_name):
    """Merges the feature branch into main locally."""
    print(f"\n🔀 Merging '{branch_name}' into main...")
    run_command("git checkout main", cwd=PROJECT_ROOT)
    result = run_command(f"git merge --no-ff {branch_name}", cwd=PROJECT_ROOT, check=False)
    if result.returncode != 0:
        print(f"⚠️  Failed to merge '{branch_name}' into main:\n{result.stderr.strip()}")
        print("↩️  Aborting the in-progress merge to keep 'main' clean...")
        run_command("git merge --abort", cwd=PROJECT_ROOT, check=False)
        return False
    print(f"✅ Merged '{branch_name}' into main.")
    return True


def run_grouped_commits():
    """Triggers the /commits skill to group and commit remaining changes."""
    print("\n📦 Grouping and committing changes via /commits skill...")
    ok, output = run_agent(command="commits", cwd=PROJECT_ROOT, capture=True)

    matches = COMMITS_RESULT_RE.findall(output)
    status = matches[-1][0] if matches else "UNKNOWN"
    print(f"🧾 Commits result: {status}")
    leftover = git_status()
    if leftover:
        print(f"⚠️  Uncommitted changes remain after /commits:\n{leftover}")
    return ok and status in ("OK", "NOTHING_TO_COMMIT")




# ---------------------------------------------------------------------------
# Progress tracking: lets an interrupted or failed run be resumed where it stopped.
# ---------------------------------------------------------------------------

# Set RUNNER_RESUME=0 to archive any unfinished progress and start from scratch.
RESUME = os.environ.get("RUNNER_RESUME", "1") != "0"

TRACK_FILE_RE = re.compile(r"^(\d+)_error\.json(\.archive)?$")
ARCHIVE_SUFFIX = ".archive"
# Stages after the todos, in pipeline order. "plan" comes before the todos.
STAGES = ("plan", "review", "commit", "push", "merge")
WIP_COMMIT_PREFIX = "wip(runner):"


def now_iso():
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


class Tracker:
    """In-memory progress of every task, mirrored to errors/track/<n>_error.json.

    Shape: {"run": {...}, "tasks": {<task file name>: {"file", "status",
    "todos": {"<n>": {"started", "finished"}}, "stages": {<stage>: bool},
    "last_error", "wip_commit"}}}. Task file names are the keys because task ids
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
            "wip_commit": None,
        })
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

Break the task below into small todos, in execution order. Each todo must be implementable (and checkable) on its own in a single agent session, and together they must cover the whole task.

Finish by printing the plan between a line containing only PLAN_BEGIN and a line containing only PLAN_END, one todo per line formatted as "<n>. <todo>". Put nothing else between the markers.

--- TASK (file: {task_file}) ---
{task}
"""

IMPLEMENT_PROMPT = """You are implementing ONE step of a multi-step task that a runner drives step by step. Implement only the current todo below, then stop. Do not commit, and do not edit the task file; the runner tracks progress itself.

--- TASK (file: {task_file}) ---
{task}

--- PLAN ---
{plan}

--- CURRENT TODO ---
#{number}: {text}
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
    lines = []
    for number, _, text in items:
        todo = todos[str(number)]
        if todo["finished"]:
            status = "done"
        elif number == current:
            status = "CURRENT"
        elif todo["started"]:
            status = "interrupted"
        else:
            status = "pending"
        lines.append(f"{number}. [{status}] {text}")
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# Task pipeline: plan -> todos -> review -> commit -> push -> merge.
# ---------------------------------------------------------------------------

class TaskFailed(Exception):
    """A pipeline step failed; the task is parked in errors/ and resumed next run."""


def locate_task(name):
    """Finds a task file by name in tasks/, errors/ or finished/ (it moves between them)."""
    for directory in (TASKS_DIR, ERRORS_DIR, FINISHED_DIR):
        if (directory / name).is_file():
            return directory / name
    return None


def collect_tasks(tracker):
    """Returns pending tasks plus unfinished tracked ones, the checked-out branch's task first.

    The branch that is checked out is most likely the one that was interrupted,
    and resuming it first avoids the dirty-worktree prompt for its partial work.
    """
    names = {f.name for f in TASKS_DIR.iterdir() if f.is_file() and f.suffix in [".md", ".txt"]}
    for name, entry in tracker.tasks.items():
        if entry.get("status") == "complete" or name in names:
            continue
        # The file may only exist on the task's own branch, so it's located again after checkout.
        names.add(name)

    branch = current_branch()
    return sorted(names, key=lambda name: (task_branch(name) != branch, name))


def save_partial_work(tracker, task_name, branch_name):
    """Commits leftover changes as a WIP commit on the task branch, so they survive
    the switch to the next task's branch. It is undone again when the task resumes.
    """
    if current_branch() != branch_name or not git_status():
        return
    run_command("git add -A", cwd=PROJECT_ROOT)
    run_command(["git", "commit", "--no-verify", "-m", f"{WIP_COMMIT_PREFIX} {task_name} interrupted"], cwd=PROJECT_ROOT)
    sha = run_command("git rev-parse HEAD", cwd=PROJECT_ROOT).stdout.strip()
    tracker.tasks[task_name]["wip_commit"] = sha
    tracker.save()
    print(f"💾 Partial work saved on '{branch_name}' as WIP commit {sha[:10]}.")


def restore_partial_work(tracker, task_name):
    """Undoes the WIP commit made by save_partial_work, putting its changes back in the working tree."""
    entry = tracker.tasks[task_name]
    sha = entry.get("wip_commit")
    if not sha:
        return
    head = run_command("git rev-parse HEAD", cwd=PROJECT_ROOT).stdout.strip()
    if head == sha:
        run_command("git reset HEAD~1", cwd=PROJECT_ROOT)
        print(f"🧩 Restored partial work from WIP commit {sha[:10]} into the working tree.")
    else:
        print(f"⚠️  WIP commit {sha[:10]} is no longer HEAD; leaving history as it is.")
    entry["wip_commit"] = None
    tracker.save()


def plan_task(tracker, task_path, body):
    print(f"\n🧭 Planning '{task_path.name}' in plan mode...")
    tracker.set_current(task_path.name, "plan")
    ok, output = run_agent(
        prompt=PLAN_PROMPT.format(task_file=task_path.relative_to(PROJECT_ROOT), task=body),
        capture=True,
        plan=True,
    )
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
    for index, (number, _, text) in enumerate(items):
        todo = entry["todos"][str(number)]
        if todo["finished"]:
            continue

        print(f"\n🤖 Todo {number}/{len(items)} of '{name}': {text}")
        resume_note = ""
        if todo["started"]:
            error = f" (last error: {entry['last_error']})" if entry.get("last_error") else ""
            resume_note = RESUME_NOTE.format(error=error)
        prompt = IMPLEMENT_PROMPT.format(
            task_file=task_path.relative_to(PROJECT_ROOT),
            task=body,
            plan=render_plan(items, entry["todos"], number),
            number=number,
            text=text,
            resume_note=resume_note,
        )

        tracker.set_current(name, "implement", number)
        tracker.mark_todo(name, number, started=True)
        success, _ = run_agent(prompt=prompt)
        if not success:
            raise TaskFailed(f"implementation of todo #{number} failed")

        tracker.mark_todo(name, number, finished=True)
        items[index] = (number, True, text)
        write_plan(task_path, body, items)
        print(f"✅ Todo {number} done.")


def run_task(tracker, name):
    """Runs (or resumes) one task through the whole pipeline. Raises TaskFailed on failure."""
    # Any earlier attempt may have left commits (WIP included) on the branch, so never recreate it then.
    resuming = name in tracker.tasks
    task_path = locate_task(name)
    if task_path is None and not resuming:
        raise TaskFailed(f"task file '{name}' not found in tasks/, errors/ or finished/")

    # Check for empty tasks before touching Git, so they don't trigger a checkout/pull.
    if task_path is not None and not task_path.read_text(encoding="utf-8").strip():
        print(f"⚠️  Empty task {name}. Skipping...")
        return None
    if task_path is not None:
        tracker.entry(task_path)

    # 1. Sync Git & create (or, when resuming, reuse) the feature branch
    tracker.set_current(name, "branch")
    branch_name = prepare_git_branch(name, resume=resuming)
    if resuming:
        restore_partial_work(tracker, name)
    # Checking out the branch may have moved the file (e.g. into errors/ by the failed run).
    task_path = locate_task(name)
    if task_path is None:
        raise TaskFailed(f"task file '{name}' not found on branch '{branch_name}'")
    entry = tracker.entry(task_path)
    entry["status"] = "in_progress"

    # 2. Plan in plan mode, or reuse the plan already in the task file
    body, items = split_plan(task_path.read_text(encoding="utf-8"))
    if not items:
        items = plan_task(tracker, task_path, body)
    else:
        print(f"📋 Reusing the {len(items)}-todo plan already in {name}.")
        tracker.mark_stage(name, "plan")
    tracker.init_todos(name, items)
    # The tracker is the source of truth; keep the checkboxes in line with it.
    items = [(number, entry["todos"][str(number)]["finished"], text) for number, _, text in items]

    # 3. Implement the todos one agent call at a time
    implement_todos(tracker, task_path, body, items)

    # 4. Code review (with fix rounds)
    if not entry["stages"]["review"]:
        tracker.set_current(name, "review")
        if name.endswith(".ncr.md"):
            print(f"⏭️  Skipping code review for {name} (.ncr.md)")
        else:
            review_passed, reason = review_with_fixes(name)
            if not review_passed:
                raise TaskFailed(f"code review failed: {reason}")
        tracker.mark_stage(name, "review")

    # 5. Commit, with the task file (plan fully ticked) moved into finished/ as part of it
    if not entry["stages"]["commit"]:
        tracker.set_current(name, "commit")
        write_plan(task_path, body, [(number, True, text) for number, _, text in items])
        if task_path.parent != FINISHED_DIR:
            task_path = move_task(task_path, FINISHED_DIR)
            entry = tracker.entry(task_path)
        if not run_grouped_commits():
            raise TaskFailed("commit issues")
        tracker.mark_stage(name, "commit")

    # 6. Push & merge
    if not entry["stages"]["push"]:
        tracker.set_current(name, "push")
        if not push_branch(branch_name):
            raise TaskFailed("push failed")
        tracker.mark_stage(name, "push")
    if not entry["stages"]["merge"]:
        tracker.set_current(name, "merge")
        if not merge_to_main(branch_name):
            raise TaskFailed("merge failed")
        tracker.mark_stage(name, "merge")

    tracker.complete(name)
    return branch_name


def park_failed_task(tracker, name, reason):
    """Records the failure, moves the task file to errors/ and keeps its partial work on its branch."""
    tracker.fail(name, reason)
    branch_name = task_branch(name)
    task_path = locate_task(name)
    # Once committed into finished/ the move is part of the branch history; leave it there.
    if task_path and task_path.parent == TASKS_DIR:
        tracker.entry(move_task(task_path, ERRORS_DIR))
    save_partial_work(tracker, name, branch_name)


def process_tasks(tracker):
    names = collect_tasks(tracker)
    if not names:
        print("✨ No pending tasks found.")
        tracker.archive_if_complete()
        return

    print(f"📋 Found {len(names)} task(s) to process: {', '.join(names)}")

    completed, failed = [], []

    for name in names:
        print("\n" + "=" * 60)
        print(f"📋 Task: {name}")
        print("=" * 60)

        try:
            if run_task(tracker, name) is not None:
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

    print("\n" + "=" * 60)
    print(f"🏁 Run finished: {len(completed)} completed, {len(failed)} failed.")
    if completed:
        print(f"   ✅ {', '.join(completed)}")
    if failed:
        print(f"   ❌ {', '.join(failed)}")
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
            current = tracker.run.get("current")
            if current:
                try:
                    save_partial_work(tracker, current["task"], task_branch(current["task"]))
                except Exception as save_exc:
                    print(f"⚠️  Could not save partial work: {save_exc}")
            print(f"💾 Progress saved to {tracker.path.relative_to(PROJECT_ROOT)}; re-run to resume.")
        raise SystemExit(exit_code)


if __name__ == "__main__":
    main()
