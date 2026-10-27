---
name: "task-management"
description: >
  Manage, monitor, and control tasks — list active/scheduled/deferred tasks,
  cancel or delete them, create new scheduled or deferred tasks, and view task logs.
  Tools are available to the agent for programmatic task lifecycle management.
activation: "on-demand"
tools:
  - name: "list_tasks"
    description: "List all tasks (active, scheduled, deferred) with their status and metadata."
    parameters:
      type: object
      properties:
        status:
          type: string
          description: "Optional filter by status (running, completed, failed, scheduled, cancelled)"
        workspace_id:
          type: string
          description: "Optional filter by workspace ID"
  - name: "cancel_task"
    description: "Cancel a running or pending task by its ID. Returns confirmation."
    parameters:
      type: object
      properties:
        task_id:
          type: string
          description: "The UUID or short ID of the task to cancel"
  - name: "delete_task"
    description: "Permanently remove a completed/failed/cancelled task from memory and storage."
    parameters:
      type: object
      properties:
        task_id:
          type: string
          description: "The UUID or short ID of the task to delete"
  - name: "schedule_task"
    description: "Create a new recurring cron-based scheduled task. The command executes on each tick."
    parameters:
      type: object
      properties:
        name:
          type: string
          description: "Human-readable task name"
        schedule:
          type: string
          description: "Cron expression (5-field or 6-field), e.g. '*/5 * * * *'"
        command:
          type: string
          description: "Shell command to execute on each tick"
        workspace_id:
          type: string
          description: "Target workspace ID"
  - name: "defer_task"
    description: "Create a one-shot task that runs at an absolute time (RFC3339)."
    parameters:
      type: object
      properties:
        name:
          type: string
          description: "Human-readable task name"
        command:
          type: string
          description: "Shell command to execute when the task fires"
        defer_until:
          type: string
          format: date-time
          description: "Absolute time at which the task should run (RFC3339)"
        workspace_id:
          type: string
          description: "Target workspace ID"
  - name: "task_log"
    description: "Tail the last N log lines for a task's session output."
    parameters:
      type: object
      properties:
        task_id:
          type: string
          description: "The UUID or short ID of the task"
        lines:
          type: integer
          default: 50
          description: "Number of log lines to return (default 50)"
---

# Task Management Skill

Provides typed tools for the agent to manage the full task lifecycle: creation, scheduling, deferral, cancellation, deletion, and log inspection.

## Usage Notes

- `list_tasks` returns a structured array of task objects with status, ID, prompt, and timing.
- `cancel_task` works on tasks in `pending` or `running` status only.
- `delete_task` permanently removes task entries; use `cancel_task` first if the task is still active.
- `schedule_task` registers a recurring cron task; the command executes on each scheduler tick.
- `defer_task` creates a one-shot task that fires once at `defer_until`.
- `task_log` returns recent log lines for a specific task's session output.

## Example Workflow

```
1. list_tasks(status="running") → find running tasks
2. cancel_task(task_id="abc123") → cancel a stuck task
3. defer_task(name="cleanup", command="rm -rf /tmp/cache", defer_until="2026-10-23T14:00:00Z") → schedule for later
```
