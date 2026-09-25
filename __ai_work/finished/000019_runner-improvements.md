# 000019 runner-improvements

## Goal

## Evaluation

First you should read the following proposal, and evaluate it

### BEGIN Proposal

Improve the runner.py, The runner.py loop should now use the plan mode of the AGENT_CLI, 

For each task, the runner should use the plan mode of the AGENT_CLI, and update the task file with a plan section, add some sort of placeholder separating the task content from the planning content

The plan content should contain a list of todos as a numbered list. After each implementation, the runner log should add to a dictionary in memory that follows the following format:  {[taskId] { [todoNumber]: { started: boolean, finished: boolean}}}

You should add a "global" rescue to recover and write down a file in the ./__ai_work/errors/track <int_n>_error.json, with the dict value as json. 

int_n is a integer starting by 1, that increases every recover of the 

The runner.py should check provide that context to the AGENT_CLI, so it know where to begin.

If all the tickets are complete that file should be renamed by adding a .archive on the end of the file

The runner should check the track files to see if everything is complete at the end, and also update the task file by marking everything as complete

### END Proposal

Now you should decide if you are going to imlement that proposal, or if you have one better, The goal is that the runner.py and the any AGENT_CLI together be able to start take the work from the place it stopped or that generated a error.

Add the plan to this file under the following block, The plan you build, you can implement without asking questions.


### BEGIN PLAN

#### Evaluation of the proposal

The core idea (plan first, track todo progress, persist it, feed it back to the
agent on the next run) is right, and it is kept. These parts change:

1. **Run one todo per agent call.** If one agent call implements the whole
   task, the runner can't tell which todo it is on. So `started`/`finished`
   would only be guesses. The runner will call the agent once per todo, so it
   knows for sure which todo started and which finished.
2. **Save the tracker after every change, not only in the rescue.** A
   `try/except` can't catch `kill -9`, a closed terminal, or a container
   restart. The tracker is written to disk (atomically) after every
   transition. The global rescue only adds the error details and does a last
   flush.
3. **Track the stages after the todos too.** A crash during review, commit,
   push, or merge must not re-run the implementation. Each task entry also
   tracks `plan`, `review`, `commit`, `push`, and `merge`.
4. **Don't reset the branch on resume.** `git checkout -B feat/<id>` from
   `main` would throw away the work of the interrupted run. It would also stop
   on the "dirty worktree" prompt because of the task's own partial changes. On
   resume, the runner checks out the existing branch and keeps the working tree
   when it is already on that branch.
5. **Keep the track files out of git.** They are runtime state. If git saw
   them, they would end up in the task's commits and trigger the dirty-worktree
   prompt.

#### Design

- **Task file layout.** After planning, the runner appends
  `<!-- RUNNER:PLAN -->` and a `## Plan` section to the task file. The section
  is a numbered checklist (`1. [ ] ...`). Each item is ticked (`[x]`) when its
  todo finishes, so the task file is a readable record of progress. On resume,
  if the marker is already there, the plan is parsed from the file and the
  planning step is skipped.
- **Planning.** The runner runs the agent in plan mode:
  `claude -p --permission-mode plan` or `opencode run --agent plan`. It asks
  the agent to print the plan between `PLAN_BEGIN` and `PLAN_END` as a numbered
  list. The runner writes the plan into the file; the agent does not.
- **Tracker (in memory, mirrored to disk):**
  `{"run": {number, started_at, resumed_from, current, error}, "tasks": {<task file name>: {"file", "status", "todos": {"1": {"started": bool, "finished": bool}}, "stages": {...}, "last_error", "wip_commit"}}}`.
  This extends the proposed `{taskId: {todoNumber: {started, finished}}}`
  shape. The key is the task *file name*, because task ids alone are not
  unique (`000001_scaffold_program` and `000001_cleanup` both exist).
- **Partial work survives branch switches.** When a task fails, the task file
  moves to `errors/` and any leftover changes are saved as a
  `wip(runner): ...` commit on the task's branch. The next task can then start
  from a clean tree. On resume, that WIP commit is undone
  (`git reset HEAD~1`), so the changes come back as uncommitted work and never
  reach the final history.
- **Track files.** The file is
  `__ai_work/errors/track/<n>_error.json`. `n` starts at 1 and goes up by one
  on each run that resumes a previous one (archived files count too, so a
  number is never reused). When a run starts, the runner loads the newest
  non-archived track file as its starting state. Tracked tasks that are not
  complete are resumed first, starting with the one whose branch is checked
  out.
- **Global rescue.** A wrapper around `process_tasks` catches every
  `BaseException`. That includes Ctrl-C, and SIGTERM/SIGHUP, which are turned
  into exceptions. It writes the exception, traceback, task, and todo into the
  track file, then exits with a non-zero code.
- **Resuming.** Tracked tasks that are not complete are resumed wherever
  their file now is (`tasks/`, `errors/`, or `finished/`). The file is looked
  up again after the branch checkout. Each todo prompt includes the task text, the plan
  with each item's status, the last error, and a note: if the todo was started
  but not finished, check `git status`/`git diff` and continue the partial work
  instead of starting over.
- **Completion.** When a task's merge is done, all its checkboxes are ticked
  and it is marked `complete`. At the end of the run, if every tracked task is
  complete, every active track file is renamed with a `.archive` suffix.
- **Opt-out.** `RUNNER_RESUME=0` archives any active tracker and starts
  fresh.

#### Todos

1. Add a gitignore entry for `__ai_work/errors/track/*.json*`.
2. Add the tracker helpers to `runner.py`: load the newest active track file,
   allocate `n`, save atomically, mark todos/stages, and archive.
3. Add the plan helpers: plan-mode `run_agent` support, plan output parsing,
   and writing, reading, and ticking the plan section in the task file.
4. Make `prepare_git_branch` safe to resume: reuse the existing branch, and
   keep the working tree when already on it.
5. Rewrite `process_tasks` as a staged, resumable pipeline: plan → todos →
   review → commit → push → merge. Each step is skipped if already done.
6. Add the global rescue with signal handling and error snapshots.
7. Update the `run-tasks` Makefile help to mention `RUNNER_RESUME`.
8. Check it: compile the file and run a dry run with a stubbed agent that
   covers planning, crash/resume, and archiving.

### END PLAN