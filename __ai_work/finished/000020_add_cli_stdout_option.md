# 000020 add_cli_stdout_option

Improve the runner.py 

The runner.py should show the stdout  of the AGENT_CLI based on env var AGENT_CLI_STDOUT=1, to show or not

<!-- RUNNER:PLAN -->
## Plan

1. [x] In `__ai_work/runner.py`, next to `AGENT_CLI` (~line 186), add a module-level flag `AGENT_CLI_STDOUT = os.environ.get("AGENT_CLI_STDOUT", "0") == "1"`, with a short comment saying that `AGENT_CLI_STDOUT=1` shows the agent CLI's stdout and any other value hides it.
2. [x] In `run_agent()` (~lines 196-222) of `__ai_work/runner.py`, apply the flag: when `capture=True`, keep capturing stdout (callers parse it) but only print `result.stdout` if `AGENT_CLI_STDOUT` is set, and print `result.stderr` if the flag is set or the command failed; when `capture=False`, pass `stdout=subprocess.DEVNULL` to `subprocess.run` if the flag is off, so stderr still reaches the terminal; update the docstring to describe this.
3. [x] In the root `Makefile` `run-tasks` target, pass `AGENT_CLI_STDOUT` through the same way as the other options (`$(if $(AGENT_CLI_STDOUT),AGENT_CLI_STDOUT=$(AGENT_CLI_STDOUT))`) and add `AGENT_CLI_STDOUT=1` to the target's help comment.
4. [x] Check the change: run `python3 -m py_compile __ai_work/runner.py`, then put a fake `claude` script that echoes a line on PATH and call `run_agent` with `capture=True` and `capture=False`, with and without `AGENT_CLI_STDOUT=1`. Confirm output shows only when the flag is set, and that the captured stdout returned to callers is still populated when the flag is off.
