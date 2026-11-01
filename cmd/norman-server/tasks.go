package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/scheduler"
)

// ListTasks returns tasks filtered by optional status.
func (s *server) ListTasks(w http.ResponseWriter, r *http.Request, params protocol.ListTasksParams) {
	tasks, err := s.store.ListTasks()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if params.Status != nil && *params.Status != "" {
		filtered := make([]map[string]interface{}, 0)
		statusStr := string(*params.Status)
		for _, t := range tasks {
			if s, ok := t["status"].(string); ok && s == statusStr {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

// CreateTask submits a new task to the agent loop.
func (s *server) CreateTask(w http.ResponseWriter, r *http.Request) {
	var req protocol.TaskCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	if req.SessionId == nil || *req.SessionId == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "session_id is required"})
		return
	}
	sessionID := *req.SessionId

	if _, err := s.store.GetSession(sessionID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("session %s not found", sessionID)})
		return
	}

	prompt := ""
	if req.Prompt != nil {
		prompt = *req.Prompt
	}
	workspaceID := ""
	if req.WorkspaceId != nil {
		workspaceID = *req.WorkspaceId
	}

	task, err := s.loop.Submit(context.Background(), prompt, workspaceID, sessionID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           task.ID,
		"status":       string(task.Status),
		"prompt":       task.Prompt,
		"workspace_id": task.Workspace,
		"session_id":   sessionID,
	})
}

// ScheduleTask registers a recurring scheduled task.
func (s *server) ScheduleTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name              string `json:"name"`
		Schedule          string `json:"schedule"` // cron expression
		Command           string `json:"command"`  // shell command to execute
		WorkspaceId       string `json:"workspace_id"`
		Skill             string `json:"skill"`
		ActivationCommand string `json:"activation_command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	id := uuid.New().String()[:8] + "-scheduled"
	entry := scheduler.TaskEntry{
		ID:                id,
		Name:              req.Name,
		Schedule:          req.Schedule,
		Command:           req.Command,
		Skill:             req.Skill,
		ActivationCommand: req.ActivationCommand,
		OutputHandler: func(result string, err error) {
			s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
				Event:   protocol.TaskComplete,
				TaskId:  &id,
				Message: strPtr(fmt.Sprintf("Scheduled task '%s' completed: %s", req.Name, result)),
			})
		},
	}

	// Wire activation callback if a skill is associated.
	if req.Skill != "" && s.loop != nil && s.loop.Activator() != nil {
		skillName := req.Skill
		entry.ActivationCallback = func() error {
			return s.loop.ActivateSkill(skillName, nil) // nil = local venv fallback
		}
	}

	if err := s.scheduler.Add(entry); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":                 id,
		"name":               req.Name,
		"schedule":           req.Schedule,
		"workspace_id":       req.WorkspaceId,
		"skill":              req.Skill,
		"activation_command": req.ActivationCommand,
	})
}

// UnscheduleTask removes a scheduled task by ID.
func (s *server) UnscheduleTask(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.scheduler.Remove(id); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"unscheduled": true,
		"id":          id,
	})
}

// ListScheduledTasks returns all scheduled tasks.
func (s *server) ListScheduledTasks(w http.ResponseWriter, r *http.Request) {
	entries := s.scheduler.List()
	result := make([]protocol.ScheduledTask, 0, len(entries))
	for _, entry := range entries {
		status := protocol.ScheduledTaskStatusActive
		if !entry.Active {
			status = protocol.ScheduledTaskStatusPaused
		}
		id, err := uuid.Parse(entry.ID)
		if err != nil {
			log.Printf("list scheduled tasks: bad UUID %q: %v", entry.ID, err)
			continue
		}
		result = append(result, protocol.ScheduledTask{
			Id:                &id,
			Name:              &entry.Name,
			Schedule:          &entry.Schedule,
			Status:            &status,
			WorkspaceId:       nil,
			Skill:             strPtr(entry.Skill),
			ActivationCommand: strPtr(entry.ActivationCommand),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// GetTask returns a task by ID.
func (s *server) GetTask(w http.ResponseWriter, r *http.Request, id string) {
	task, err := s.loop.GetTask(id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	uuidVal, parseErr := uuid.Parse(id)
	if parseErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("invalid task ID: %v", parseErr)})
		return
	}

	status := task.Status
	resp := protocol.Task{
		Id:          &uuidVal,
		Status:      &status,
		Prompt:      strPtr(task.Prompt),
		WorkspaceId: strPtr(task.Workspace),
		CreatedAt:   &task.CreatedAt,
		Error:       strPtr(task.Error),
	}

	if len(task.Output) > 0 {
		resp.OutputPath = strPtr(fmt.Sprintf("/tmp/task-%s.output", task.ID[:8]))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// CancelTask cancels a running task by signaling cancellation.
func (s *server) CancelTask(w http.ResponseWriter, r *http.Request, id string) {
	uuidVal, parseErr := uuid.Parse(id)
	if parseErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("invalid task ID: %v", parseErr)})
		return
	}

	taskID := uuidVal.String()
	s.taskCancellations.Store(taskID, struct{}{})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"cancelled": true,
		"task_id":   taskID,
	})
}

// ReportTaskComplete marks a task as completed.
func (s *server) ReportTaskComplete(w http.ResponseWriter, r *http.Request, id string) {
	var req protocol.TaskCompletion
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskComplete,
		TaskId:  &id,
		Message: strPtr("Task completed"),
	})

	w.WriteHeader(http.StatusOK)
}

// TailTaskLog returns the last N log lines for a task's session.
func (s *server) TailTaskLog(w http.ResponseWriter, r *http.Request, id string, params protocol.TailTaskLogParams) {
	lines := 10
	if params.Lines != nil && *params.Lines > 0 {
		lines = *params.Lines
	}

	task, err := s.loop.GetTask(id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	logs, err := s.loop.GetLogs(r.Context(), task.SessionId)
	if err != nil {
		log.Printf("tail task log: failed to get logs for session %s: %v", task.SessionId, err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"lines": lines,
			"data":  []string{"no logs available"},
		})
		return
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &id,
		Message: strPtr("Tail " + id[:8] + " log (last " + string(rune(lines)) + " lines)"),
	})

	start := 0
	if len(logs) > lines {
		start = len(logs) - lines
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"lines": lines,
		"data":  logs[start:],
	})
}

// DeferTask registers a one-shot task to run at an absolute time.
func (s *server) DeferTask(w http.ResponseWriter, r *http.Request) {
	var req protocol.DeferTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	id := uuid.New().String()[:8] + "-deferred"

	// Parse defer_until from RFC3339 string (pointer type from generated code)
	deferTime := *req.DeferUntil

	entry := scheduler.TaskEntry{
		ID:         id,
		Name:       *req.Name,
		Command:    *req.Command,
		DeferUntil: deferTime,
		OutputHandler: func(result string, err error) {
			s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
				Event:   protocol.TaskComplete,
				TaskId:  &id,
				Message: strPtr(fmt.Sprintf("Deferred task '%s' completed: %s", *req.Name, result)),
			})
		},
	}

	if err := s.scheduler.Defer(entry, deferTime); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          id,
		"name":        *req.Name,
		"defer_until": req.DeferUntil.Format(time.RFC3339),
	})
}
