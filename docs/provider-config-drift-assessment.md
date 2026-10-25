# Provider Configuration Drift Assessment
> **Date:** 2026-10-22  
> **Author:** Norman agent (session assessment)  
> **Status:** Draft — actionable items tracked for branch `fix/provider-config-drift`  
> **Related:** `pkg/config/config.go`, `pkg/llm/client.go`, `pkg/llm/llamacpp/provider.go`, `config_defaults.yaml`

---

## 1. Problem Statement

Norman's LLM provider configuration is split across **two layers** that have drifted apart:

| Layer | What it owns | Example fields |
|-------|-------------|----------------|
| **System-level defaults** (`SystemConfig`) | Global server settings + LLM runtime knobs | `default_model`, `temperature`, `max_tokens` |
| **Provider config** (`ProviderConfig`) | Provider identity + endpoint + type-specific extras | `name`, `type`, `endpoint`, `model`, `Extra` |

The problem: `temperature` and `max_tokens` are defined at the **system level**, but they are fundamentally **provider-level concerns**. A system should not dictate a global temperature — it should let each provider declare its own default, with workspace overrides as a secondary layer.

Similarly, `default_model` is a system-level field that duplicates what `ProviderConfig.Model` already captures. The "default provider" concept (a string selector) is redundant when providers are ordered in a list and the first one serves as the implicit default.

---

## 2. Current Configuration Topology

```
┌─────────────────────────────────────────────────────────────┐
│                    config_defaults.yaml                       │
│  system:                                                      │
│    default_model: "gpt-4o"        ← system-level             │
│    temperature: 0.7               ← system-level            │
│    max_tokens: 32000              ← system-level            │
│    llm_providers: []              ← provider list           │
│    default_provider: ""           ← selector string         │
└─────────────────────────────────────────────────────────────┘
          │                    │
          ▼                    ▼
  SystemConfig (Go struct)   ProviderConfig[] (Go slice)
  ├─ DefaultModel            ├─ Name, Type, Endpoint, Model
  ├─ Temperature             └─ Extra (type-specific fields)
  └─ MaxTokens
```

**Resolution path in `main.go` (lines ~85–132):**

```go
// Priority: CLI flag > config default_provider > first provider in list
providerName = selectedProvider || cfg.System.DefaultProvider || providers[0].Name

provCfg, err := cfg.System.ResolveProvider(providerName)

switch provCfg.Type {
case "openai":
    llmClient = llm.NewClientAdapter(llm.NewClient(cfg.System))  // ← reads SystemConfig directly!
case "llamacpp":
    wsCfg := config.DefaultWorkspaceConfig()
    if cfg.System.DefaultModel != "" { wsCfg.Model = cfg.System.DefaultModel }
    if cfg.System.Temperature > 0  { wsCfg.Temperature = cfg.System.Temperature }
    if cfg.System.MaxTokens > 0   { wsCfg.MaxTokens = cfg.System.MaxTokens }
    llmClient = llamacpp.NewProvider(wsCfg, llmCfg)
}
```

---

## 3. Identified Drift Points

### Drift #1: `temperature` and `max_tokens` belong on providers, not system

**Current:** `SystemConfig.Temperature` and `SystemConfig.MaxTokens` are global defaults that apply to **all** providers uniformly.

**Problem:** Different providers have different capabilities. A local llama.cpp server might support 32k tokens but a smaller model should default to 8k. A global "max_tokens: 32000" is misleading for small models.

**Evidence in code:**
- `pkg/llm/client.go` line ~45: `MaxTokens: cfg.MaxTokens` — reads directly from system config
- `pkg/llm/llamacpp/provider.go`: its own `Config` struct has `Temperature` and `MaxTokens`, but these are **not** populated from the provider's `Extra` map during construction (the `NewProvider` constructor takes `wsCfg.Temperature` / `wsCfg.MaxTokens` instead)
- The `CompletionRequest` struct carries `Temperature` and `MaxTokens` fields — these should be per-provider, not system-wide

**Impact:** Adding a new provider type requires modifying `SystemConfig` (or duplicating logic in each provider's constructor). There is no single source of truth for "what are this provider's defaults?"

### Drift #2: `default_model` duplicates `ProviderConfig.Model`

**Current:** `SystemConfig.DefaultModel` holds the global default model name. Each `ProviderConfig` also has a `Model` field.

**Problem:** If a provider is configured with `model: "llama3.1:8b"`, having a separate `default_model: "gpt-4o"` at system level creates ambiguity — which one wins? The current code in `main.go` resolves this by checking `cfg.System.DefaultModel` as a fallback, but this is a workaround for the missing per-provider default.

**Evidence:**
- `pkg/config/config.go`: `DefaultModel string` on `SystemConfig`
- `pkg/config/config.go`: `Model string` on `ProviderConfig`
- `main.go` line ~120: `if cfg.System.DefaultModel != "" { wsCfg.Model = cfg.System.DefaultModel }` — system-level override applied to workspace config

**Impact:** The "default model" is a system-level concept that should be the provider's own `Model` field. A global default_model is only needed when no providers are configured (the fallback path).

### Drift #3: `default_provider` string selector vs. first-provider fallback

**Current:**
```yaml
llm_providers:
  - name: "default"
    type: "openai"
    model: "gpt-4o"
  - name: "local"
    type: "ollama"
    model: "llama3.1:8b"
default_provider: "default"   ← explicit selector
```

**Problem:** The code in `main.go` already falls back to `providers[0].Name` when `default_provider` is empty. Having both an explicit selector AND a positional fallback creates two ways to express "use the first provider," which is confusing.

**Evidence:**
- `main.go` lines 86–92: resolution priority is `CLI flag > default_provider > providers[0].Name`
- If `default_provider` is empty, it uses the first provider — making the explicit string selector redundant for the common case

**Impact:** Users can express "use the first provider" in two ways (`default_provider: ""` or omit it), and three ways to override (`--provider`, `default_provider`, per-workspace `model`). The mental model is unclear.

### Drift #4: Three-tier hierarchy creates precedence confusion

```
1. CLI flag (-p/--provider)          ← highest
2. SystemConfig.DefaultProvider       ← explicit selector
3. providers[0].Name                 ← positional fallback
4. ProviderConfig.Model               ← provider-specific default
5. WorkspaceConfig.Model              ← workspace override
6. SystemConfig.DefaultModel          ← global fallback
```

**Problem:** Steps 4 and 6 are the same concept (a model name) at different levels. Steps 2–3 are two selectors for "which provider to use." The hierarchy is deeper than necessary.

---

## 4. Proposed Canonical Topology

### After refactoring:

```yaml
# config_defaults.yaml
system:
  server_port: 8080
  host: "127.0.0.1"
  base_dir: "~/.norman/workspaces"
  llm_providers:
    - name: "default"
      type: "openai"
      endpoint: "https://api.openai.com/v1"
      model: "gpt-4o"        ← provider-level default (replaces SystemConfig.DefaultModel)
      temperature: 0.7         ← provider-level (replaces SystemConfig.Temperature)
      max_tokens: 32000        ← provider-level (replaces SystemConfig.MaxTokens)
    - name: "local"
      type: "llamacpp"
      endpoint: "http://localhost:8081"
      model: "llama3.1:8b"
      temperature: 0.5
      max_tokens: 8192

# No SystemConfig.DefaultModel, .Temperature, or .MaxTokens fields.
# No default_provider string — providers are selected by name (CLI flag) or position.
```

### Resolution path (simplified):

```go
// Priority: CLI flag > first provider in list (positional default)
providerName = selectedProvider || providers[0].Name

provCfg := cfg.System.ResolveProvider(providerName)

// Provider config already has Model, Temperature, MaxTokens.
// Workspace overrides can still override these per-workspace.
llmClient = createProvider(provCfg, workspaceConfig)
```

### Key changes:

| Field | Before | After | Rationale |
|-------|--------|-------|-----------|
| `SystemConfig.DefaultModel` | Global default model string | **Removed** — each provider has its own `model` | Model is a provider concern |
| `SystemConfig.Temperature` | Global temperature float | **Removed** — each provider has its own `temperature` | Temperature is provider-specific |
| `SystemConfig.MaxTokens` | Global max tokens int | **Removed** — each provider has its own `max_tokens` | Token budget is provider-specific |
| `SystemConfig.DefaultProvider` | Selector string | **Optional** (for explicit naming, not positional) | Keep for CLI `-p` resolution, remove as primary selector |
| `ProviderConfig.Model` | Optional field | **Required** (default model for this provider) | Primary source of truth |
| `ProviderConfig.Temperature` | Missing | **Added** — default temperature for this provider | Provider-specific runtime knob |
| `ProviderConfig.MaxTokens` | Missing | **Added** — default max tokens for this provider | Provider-specific budget |

---

## 5. Migration Plan

### Phase 1: Extend `ProviderConfig` (non-breaking)

- Add `Temperature float64` and `MaxTokens int` fields to `ProviderConfig` struct in `pkg/config/config.go`
- Update `config_defaults.yaml` to include these on existing providers
- Update embedded defaults (`config_defaults.yaml`) with provider-level settings

### Phase 2: Deprecate system-level fields (non-breaking)

- Keep `SystemConfig.DefaultModel`, `.Temperature`, `.MaxTokens` but mark as deprecated
- Update `LoadDefault()` to merge provider-level values into workspace config when system-level values are zero
- Add migration warning if old-style config is loaded without provider-level settings

### Phase 3: Switch primary resolution path (breaking)

- In `main.go`, switch from `cfg.System.DefaultModel` → `provCfg.Model` as the primary model source
- Switch from `cfg.System.Temperature` → `provCfg.Temperature` as the primary temperature source
- Switch from `cfg.System.MaxTokens` → `provCfg.MaxTokens` as the primary token budget source
- Remove the "first provider positional fallback" in favor of explicit `default_provider` or CLI flag

### Phase 4: Cleanup (breaking)

- Remove `SystemConfig.DefaultModel`, `.Temperature`, `.MaxTokens` fields entirely
- Update all consumers (`client.go`, `llamacpp/provider.go`) to read from provider config instead of system config
- Add integration tests verifying the new resolution path
- Update `config_defaults.yaml` to remove old-style entries

---

## 6. Files Affected

| File | Change | Phase |
|------|--------|-------|
| `pkg/config/config.go` | Add `Temperature`, `MaxTokens` to `ProviderConfig`; deprecate system-level fields | 1, 3, 4 |
| `config_defaults.yaml` | Add provider-level settings; remove old-style top-level fields | 1, 4 |
| `pkg/llm/client.go` | Read model/temp/tokens from `ProviderConfig` instead of `SystemConfig` | 3, 4 |
| `pkg/llm/llamacpp/provider.go` | Constructor reads from provider config's Temperature/MaxTokens | 3, 4 |
| `cmd/norman-server/main.go` | Simplify resolution path; remove system-level fallback logic | 2, 3 |
| `pkg/config/embedded.go` | No change (YAML is the source of truth) | — |
| `docs/provider-config-drift-assessment.md` | This document | — |

---

## 7. Risk Assessment

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| Existing `~/.norman/config.yaml` files break on migration | Medium (Phase 3+) | Phase 2 deprecation warnings; backward-compatible merge logic |
| Provider-specific defaults conflict with workspace overrides | Low | Workspace override logic already handles precedence correctly |
| CLI `-p` flag behavior changes subtly | Low | Explicit `default_provider` selector remains available for named resolution |
| `CompletionRequest` struct needs `Temperature`/`MaxTokens` at provider level, not system level | Medium | These fields already exist on `CompletionRequest`; the issue is where defaults originate |

---

## 8. Open Questions

1. **Should `default_provider` be removed entirely?** If providers are always selected by name (CLI flag or explicit config), the positional fallback (`providers[0]`) becomes unnecessary. However, removing it requires a migration path for configs that rely on implicit first-provider selection.
2. **Should workspace-level `model`, `temperature`, `max_tokens` be scoped to a provider?** Currently they are global workspace overrides. A more granular approach would allow per-provider workspace overrides (e.g., "use gpt-4o's budget but llama3.1's temperature"). This is likely over-engineering for MVP.
3. **How do we handle the fallback path ("no providers configured")?** Currently `llm.NewClient(cfg.System)` is used as a catch-all. After this refactor, the fallback should create a default provider from system-level defaults (re-purposing `SystemConfig` fields only for the no-provider case).

---

*Assessment complete. Branch: `fix/provider-config-drift`. No code changes yet — this document serves as the design spec.*
