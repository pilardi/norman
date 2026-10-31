# Clean Client Plan — `feat/clean-client` Branch

> **Goal:** Ensure the Norman CLI client communicates with the server exclusively through generated API-spec code (`pkg/protocol.Client`), eliminating all direct `net/http` calls in client files.
>
> **Status:** Draft — awaiting approval before implementation.

---

## 1. Current State Assessment

### ✅ Already Clean (using `newProtocolClient()` + generated code)

| File | Status |
|------|--------|
| `cmd/norman/config.go` | ✓ Uses `client.GetConfig()` / `client.PutConfig()` with typed requests |
| `cmd/norman/providers.go` | ✓ Uses `client.CreateProvider()`, `client.ListProviders()`, `client.GetProvider()`, `client.UpdateProvider()`, `client.DeleteProvider()`, `client.QueryProviderModels()`, `client.ListProviderModels()` |

### ❌ Still Using Direct HTTP Calls (need fixing)

| File | Issue | Lines / Details |
|------|-------|-----------------|
| `chat.go` | Raw `http.NewRequestWithContext` + `http.Client.Do()` for session creation & message sending | ~198–200, 251–253, 306–313 |
| `sessions.go` | Raw POST for archive; `httpGet()` for session list | ~95–100, 125–130 (archive), `handleSessionList`, `listSessions` |
| `workspaces.go` | Uses `httpPost()`, `httpPostJSON()`, `httpDelete()` helpers — all direct HTTP | All 5 functions (`Create`, `List`, `Stop`, `Restore`, `Purge`) |
| `skills.go` | Uses `httpGet()`, `httpPost()`, `httpDelete()` helpers — all direct HTTP | `listSkills`, `installSkill`, `installRemote`, `removeSkill` |

### 📋 Generated Methods Available (not yet used by client)

| Category | Method | API Endpoint |
|----------|--------|--------------|
| Sessions | `ListSessions(ctx)` | GET `/api/sessions` |
| Sessions | `CreateSession(ctx, body)` | POST `/api/sessions` |
| Sessions | `ArchiveSession(ctx, body)` | POST `/api/sessions/{id}` |
| Sessions | `DeleteSession(ctx, id)` | DELETE `/api/sessions/{id}` |
| Sessions | `GetSession(ctx, id)` | GET `/api/sessions/{id}` |
| Sessions | `SendMessage(ctx, id, body)` | POST `/api/sessions/{id}/messages` |
| Sessions | `SessionHistory(ctx, id, params)` | GET `/api/sessions/{id}/history` |
| Workspaces | `ListWorkspaces(ctx, params)` | GET `/api/workspaces` |
| Workspaces | `CreateWorkspace(ctx, body)` | POST `/api/workspaces` |
| Workspaces | `DeleteWorkspace(ctx, name)` | DELETE `/api/workspaces/{name}` |
| Workspaces | `GetWorkspace(ctx, name)` | GET `/api/workspaces/{name}` |
| Workspaces | `UpdateWorkspace(ctx, name, body)` | PUT `/api/workspaces/{name}` |
| Workspaces | `PurgeWorkspace(ctx, name, body)` | POST `/api/workspaces/{name}/purge` |
| Workspaces | `RestoreWorkspace(ctx, name, body)` | POST `/api/workspaces/{name}/restore` |
| Workspaces | `StopWorkspace(ctx, name, body)` | POST `/api/workspaces/{name}/stop` |
| Skills | `ListSkills(ctx)` | GET `/api/skills` |
| Skills | `InstallSkill(ctx, body)` | POST `/api/skills/install` |
| Skills | `RemoveSkill(ctx, name)` | DELETE `/api/skills/{name}` |
| Tasks | `ListTasks(ctx, params)` | GET `/api/tasks` |

---

## 2. Implementation Plan (5 Phases)

### Phase 1 — Sessions (`sessions.go`)

**Changes:**
- Replace `httpGet("/api/sessions")` → `client.ListSessions(ctx)`
- Replace raw POST to `/api/sessions/{id}` (archive) → `client.ArchiveSession(ctx, ArchiveSessionRequest{...})`
- Remove duplicate `archiveSession()` function (keep only `handleSessionArchive`)

**Migration map:**

| Current | Replacement |
|---------|-------------|
| `httpGet("/api/sessions")` | `client.ListSessions(ctx)` → parse with `ParseListSessionsResponse` |
| Raw POST `/api/sessions/{id}` | `client.ArchiveSession(ctx, ArchiveSessionRequest{SessionId: actualID})` |

### Phase 2 — Chat (`chat.go`)

**Changes:**
- Replace raw POST to `/api/sessions` (session creation) → `client.CreateSession(ctx, SessionCreateRequest{...})`
- Replace raw POST to `/api/sessions/{id}/messages` → `client.SendMessage(ctx, id, SendMessageRequest{...})`
- Replace `httpGet("/api/tasks")` in `listEmergentTasks` → `client.ListTasks(ctx, params)`

**Migration map:**

| Current | Replacement |
|---------|-------------|
| Raw POST `/api/sessions` | `client.CreateSession(ctx, SessionCreateRequest{WorkspaceId: foundID, ClientType: "cli"})` |
| Raw POST `/api/sessions/{id}/messages` | `client.SendMessage(ctx, sessionID, SendMessageRequest{Message: line})` |
| `httpGet("/api/tasks")` | `client.ListTasks(ctx, nil)` → parse with `ParseListTasksResponse` |

### Phase 3 — Workspaces (`workspaces.go`)

**Changes:**
- Replace `httpPost("/api/workspaces", ...)` → `client.CreateWorkspace(ctx, body)`
- Replace `httpGet("/api/workspaces")` → `client.ListWorkspaces(ctx, params)`
- Replace `httpPostJSON("/api/workspaces/{name}/stop", ...)` → `client.StopWorkspace(ctx, name, body)`
- Replace `httpPostJSON("/api/workspaces/{name}/restore", ...)` → `client.RestoreWorkspace(ctx, name, body)`
- Replace `httpPostJSON("/api/workspaces/{name}/purge", ...)` → `client.PurgeWorkspace(ctx, name, body)`

**Migration map:**

| Current | Replacement |
|---------|-------------|
| `httpPost("/api/workspaces", req)` | `client.CreateWorkspace(ctx, WorkspaceCreateRequest{Name: name, ClientType: "cli"})` |
| `httpGet("/api/workspaces")` | `client.ListWorkspaces(ctx, nil)` → parse with `ParseListWorkspacesResponse` |
| `httpPostJSON("/api/workspaces/{name}/stop", reqBody)` | `client.StopWorkspace(ctx, name, StopRequest{Reason: reason})` |
| `httpPostJSON("/api/workspaces/{name}/restore", reqBody)` | `client.RestoreWorkspace(ctx, name, RestoreRequest{RecreateSandbox: recreateSandbox})` |
| `httpPostJSON("/api/workspaces/{name}/purge", reqBody)` | `client.PurgeWorkspace(ctx, name, PurgeRequest{Confirm: true})` |

### Phase 4 — Skills (`skills.go`)

**Changes:**
- Replace `httpGet("/api/skills")` → `client.ListSkills(ctx)`
- Replace `httpPost("/api/skills/install", ...)` → `client.InstallSkill(ctx, body)`
- Replace `httpDelete("/api/skills/{name}")` → `client.RemoveSkill(ctx, name)`

**Migration map:**

| Current | Replacement |
|---------|-------------|
| `httpGet("/api/skills")` | `client.ListSkills(ctx)` → parse with `ParseListSkillsResponse` |
| `httpPost("/api/skills/install", payload)` | `client.InstallSkill(ctx, SkillInstallRequest{Repo: sourcePath})` |
| `httpDelete("/api/skills/{name}")` | `client.RemoveSkill(ctx, name)` |

### Phase 5 — Cleanup (`http.go`)

**Changes:**
- After all callers are migrated, evaluate whether helper functions (`httpGet`, `httpPost`, `httpPostJSON`, `httpDelete`, `httpPutJSON`) are still needed.
- Likely candidates for removal: `httpPost`, `httpPostJSON`, `httpDelete` (all replaced by generated methods).
- Keep `httpGet` only if WebSocket-related code needs it (unlikely — chat.go uses raw HTTP for session creation/messages, which will be replaced).
- Consider removing the entire `http.go` file if no remaining callers exist.

---

## 3. Files to Modify

| # | File | Actions |
|---|------|---------|
| 1 | `cmd/norman/sessions.go` | Replace raw HTTP with `protocol.Client` calls; remove duplicate `archiveSession()` |
| 2 | `cmd/norman/chat.go` | Replace raw POST for session creation, message sending; replace `httpGet("/api/tasks")` |
| 3 | `cmd/norman/workspaces.go` | Replace all `httpPost`/`httpPostJSON`/`httpDelete` with generated methods |
| 4 | `cmd/norman/skills.go` | Replace `httpGet`/`httpPost`/`httpDelete` with generated methods |
| 5 | `cmd/norman/http.go` | Remove unused helpers (or entire file) after migration |

---

## 4. Verification Steps

After each phase:
1. `make build` — confirms compilation succeeds
2. `golangci-lint run ./...` — confirms no lint issues
3. Manual smoke test of affected CLI commands

---

## 5. Risks & Considerations

| Risk | Mitigation |
|------|------------|
| Generated response types differ from current `map[string]interface{}` parsing | Use `ParseXxxResponse()` helpers; add explicit type assertions |
| `ArchiveSession` expects a request body — check if the API spec allows empty body or requires `{}` | Verify against `api-spec.yaml`; adjust accordingly |
| WebSocket in `chat.go` remains unchanged (intentional) | Document as "real-time transport, not API" |
| `urlPathEscape()` helper may still be needed for typed calls | Keep if used by generated methods; otherwise remove |

---

## 6. Order of Execution

1. ✅ **Phase 1** (Sessions) — DONE. Replaced `handleSessionList`, `handleSessionArchive`, `listSessions`; removed duplicate `archiveSession()`. Updated `chat.go` slash-command `/archive` to use typed call. All tests pass, lint clean.
2. **Phase 3** (Workspaces) — lowest risk, most straightforward replacements
3. **Phase 4** (Skills) — similar pattern to workspaces
4. **Phase 2** (Chat) — most complex; involves message sending + session history
5. **Phase 5** (Cleanup) — remove unused helpers from `http.go`

This order allows incremental verification after each phase.
