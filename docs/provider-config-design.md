# Provider Configuration — Design & Implementation Plan

> **Branch:** `feat/provider-config`  
> **Date:** 2026-10-25  
> **Status:** Draft → In Progress

---

## 1. Problem Statement

The `norman config` CLI command exposes general server settings (port, host, bell, rate limit) but does **not** let users configure LLM providers — neither adding new ones nor editing existing ones. The data model (`ProviderConfig`) and runtime consumer (`NewClientWithProvider`) already exist; the gap is the **CLI display/update path** and the **server-side PUT handler**.

---

## 2. Target Design

### 2.1 API Endpoints (new)

Add four CRUD endpoints under `/api/providers`:

| Method | Path | Description |
|--------|------|-------------|
| `GET`  | `/api/providers` | List all configured providers |
| `POST` | `/api/providers` | Add a new provider (validates type/model/api_key) |
| `PUT`  | `/api/providers/{name}` | Update an existing provider's fields |
| `DELETE` | `/api/providers/{name}` | Remove a provider (fails if it's the last one or selected by CLI flag) |

### 2.2 Schema Addition to `api-spec.yaml`

```yaml
components:
  schemas:
    ProviderConfig:
      type: object
      required: [name, type, endpoint]
      properties:
        name:
          type: string
          description: Unique provider identifier (e.g., "default", "local-llama")
        type:
          type: string
          enum: [openai, anthropic, ollama, gemini, llamacpp]
          description: Provider implementation type
        endpoint:
          type: string
          format: uri
          description: Base API URL (e.g., "https://api.openai.com/v1")
        model:
          type: string
          description: Default model name for this provider
        temperature:
          type: number
          format: float
          description: Provider default temperature (0–2)
        max_tokens:
          type: integer
          minimum: 1
          description: Provider default max tokens per call
        api_key:
          type: string
          description: API key (masked in GET responses, required for openai/anthropic types)
        extra:
          type: object
          additionalProperties: true
          description: Type-specific fields (e.g., cache_size for llamacpp)

    ProviderCreateRequest:
      type: object
      required: [name, type, endpoint]
      properties:
        name:
          type: string
        type:
          type: string
          enum: [openai, anthropic, ollama, gemini, llamacpp]
        endpoint:
          type: string
        model:
          type: string
        temperature:
          type: number
          format: float
        max_tokens:
          type: integer
        api_key:
          type: string
        extra:
          type: object
          additionalProperties: true

    ProviderUpdateRequest:
      type: object
      properties:
        type:
          type: string
          enum: [openai, anthropic, ollama, gemini, llamacpp]
        endpoint:
          type: string
        model:
          type: string
        temperature:
          type: number
          format: float
        max_tokens:
          type: integer
        api_key:
          type: string
        extra:
          type: object
          additionalProperties: true

    ProviderResponse:
      type: object
      properties:
        name:
          type: string
        type:
          type: string
        endpoint:
          type: string
        model:
          type: string
        temperature:
          type: number
          format: float
        max_tokens:
          type: integer
        api_key_masked:
          type: boolean
          description: true if api_key is non-empty (never returns raw key in GET)
        extra:
          type: object
          additionalProperties: true
```

### 2.3 CLI Enhancements (`cmd/norman/config.go`)

- **Display**: Append provider listing to human-readable and `--json` output
- **Set**: Accept dotted-path syntax for individual fields, e.g.:
  ```bash
  norman config --set providers.0.name=local
  norman config --set providers.0.type=ollama
  norman config --set providers.0.endpoint=http://localhost:11434
  norman config --set providers.0.model=llama3
  ```
- **Add**: New command `norman provider add <name> --type <type> --endpoint <url>` (delegated to PUT /api/providers)

### 2.4 Server-Side (`cmd/norman-server/config.go`)

Extend `mergeUpdatesIntoSystem` to handle:
- `"providers"` key → JSON array of ProviderCreateRequest objects → append to `cfg.System.LlmProviders`
- `"providers.0.name"` style keys → partial update on existing provider by index/name

### 2.5 Workspace-Level Provider Override (future)

Add `ProviderName string` field to `WorkspaceConfig` — allows per-workspace provider selection without duplicating endpoint config.

---

## 3. Implementation Order

| Step | File(s) | Change |
|------|---------|--------|
| 1 | `api-spec.yaml` | Add Provider schemas + CRUD paths |
| 2 | Run `make generate` | Regenerate `pkg/protocol/types.go`, `client.go`, `handlers.go` |
| 3 | `cmd/norman-server/config.go` | Extend `mergeUpdatesIntoSystem` for provider keys |
| 4 | `cmd/norman-server/routes.go` | Wire up new provider endpoints (GET/POST/PUT/DELETE) |
| 5 | `cmd/norman/config.go` | Add display + `--set` handling for providers |
| 6 | `pkg/config/config_test.go` | Add tests for provider validation & merge |

---

## 4. Validation Rules

- `name` must be unique across the list (case-insensitive)
- `type` must be one of: `openai`, `anthropic`, `ollama`, `gemini`, `llamacpp`
- `endpoint` must be a valid URI
- For `openai` and `anthropic` types, `api_key` is required (non-empty)
- `temperature` must be in `[0, 2]`
- `max_tokens` must be >= 1
- Cannot delete the last remaining provider
- Cannot delete a provider that is currently selected via `-p` flag (runtime conflict)

---

## 5. Backward Compatibility

- Existing configs without `llm_providers` continue to work (empty list = no providers, falls back to embedded defaults)
- The `"default"` provider from `config_defaults.yaml` is preserved as-is
- `SystemConfig.DefaultModel`, `Temperature`, `MaxTokens` fields remain for workspace-level overrides but are superseded by provider-level values
