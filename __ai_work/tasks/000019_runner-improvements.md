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


### END PLAN