// Package main implements the Norman Agent Server — a single long-running process
// exposing HTTP API + WebSocket events via Echo.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/google/uuid"
	"github.com/norman/norman/pkg/agent"
	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/env"
	"github.com/norman/norman/pkg/llm"
	"github.com/norman/norman/pkg/llm/llamacpp"
	"github.com/norman/norman/pkg/memory"
	"github.com/norman/norman/pkg/notify"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/scheduler"
	"github.com/norman/norman/pkg/skills"
)

// bootstrap initializes all server dependencies and returns the fully-configured
// server struct ready for route registration.
func bootstrap() *server {
	cfg, err := config.LoadDefault()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// --verbose CLI flag overrides config.yaml Logging.Verbose (server-side debug output).
	// -p/--provider CLI flag selects which configured provider to use (overrides default_provider).
	var selectedProvider string
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--verbose", "-v":
			cfg.System.Logging.Verbose = true
		case "--provider", "-p":
			selectedProvider = "unknown" // placeholder; resolved after config load
		}
	}

	// Resolve selected provider: CLI flag > configured default_provider > first in list.
	var providerName string
	if selectedProvider != "" && selectedProvider != "unknown" {
		providerName = selectedProvider
	} else if cfg.System.DefaultProvider != "" {
		providerName = cfg.System.DefaultProvider
	} else if len(cfg.System.LlmProviders) > 0 {
		providerName = cfg.System.LlmProviders[0].Name
	}

	var llmClient llm.LLM
	if providerName != "" {
		provCfg, err := cfg.System.ResolveProvider(providerName)
		if err != nil {
			log.Fatalf("failed to resolve provider %q: %v", providerName, err)
		}

		switch provCfg.Type {
		case "openai":
			llmClient = llm.NewClientAdapter(llm.NewClientWithProvider(provCfg))
		case "llamacpp":
			// Build type-specific config from Extra map.
			llmCfg := llamacpp.DefaultConfig()
			if v, ok := provCfg.Extra["cache_size"].(float64); ok {
				llmCfg.CacheSize = int(v)
			}
			if v, ok := provCfg.Extra["cache_type"].(string); ok {
				llmCfg.CacheType = v
			}
			if v, ok := provCfg.Extra["cache_enabled"].(bool); ok {
				llmCfg.CacheEnabled = v
			}

			// Use provider-level defaults for model/temp/tokens.
			wsCfg := config.DefaultWorkspaceConfig()
			wsCfg.Model = provCfg.Model
			if provCfg.Temperature > 0 {
				wsCfg.Temperature = provCfg.Temperature
			}
			if provCfg.MaxTokens > 0 {
				wsCfg.MaxTokens = provCfg.MaxTokens
			}

			// Apply embedding endpoint configuration.
			var opts []llamacpp.Option
			switch provCfg.EmbeddingEndpoint {
			case "/embeddings":
				opts = append(opts, llamacpp.WithNativeEmbeddings())
			case "/api/v1/embeddings":
				opts = append(opts, llamacpp.WithOpenAICompatibleEmbeddings())
			}

			llmClient = llamacpp.NewProvider(wsCfg, llmCfg, opts...)
		default:
			log.Fatalf("unsupported provider type %q", provCfg.Type)
		}
	} else {
		// Fallback: no providers configured, use OpenAI-compatible client with defaults.
		llmClient = llm.NewClientAdapter(llm.NewClient(cfg.System))
	}

	_ = providerName // used for logging/debugging; actual client is in llmClient

	// Initialize memory store — path derived from environment, not hardcoded.
	// The server communicates with the environment via OpenShell; the store
	// location is now configurable and can be passed in or auto-detected.
	storeDir := os.Getenv("NORMAN_STORE_DIR")
	if storeDir == "" {
		storeDir = os.ExpandEnv("$HOME/.norman/store") // fallback for local dev
	}
	store, err := memory.NewStore(storeDir)
	if err != nil {
		log.Fatalf("failed to init memory store: %v", err)
	}

	// Discover skills
	skillsResult, err := skills.Discover(cfg.System.Skills.DiscoveryPaths)
	if err != nil {
		log.Printf("warning: skill discovery failed: %v (continuing without skills)", err)
	}

	// Build registry map
	registry := make(map[string]skills.SkillEntry)
	for _, entry := range skillsResult.Skills {
		registry[entry.Name] = entry
	}

	// Initialize activator
	activator := skills.NewActivator(registry, cfg.System.Skills.MaxActive)

	// Initialize WebSocket hub
	wsHub := NewWSHub()
	go wsHub.Run()

	// Extract server address from config (used for listen only).
	_ = cfg.System.ServerPort // port is used in main.go after bootstrap returns
	_ = cfg.System.Host       // host is used in main.go after bootstrap returns

	// Resolve OpenShell gateway address: env var > metadata.json.
	openShellAddr := cfg.System.OpenShell.Address
	if openShellAddr == "" {
		openShellAddr = env.ResolveGatewayAddress()
	}

	// Load mTLS certificates from the same gateway directory as metadata.json.
	metadataPath := filepath.Join(os.ExpandEnv("$HOME"), ".config", "openshell", "gateways", "openshell")
	tlsConfig := env.ResolveMTLSConfig(metadataPath)

	// Create real OpenShell SDK client with TLS config if available.
	// If the client fails to connect, the server refuses to start (no fake fallback).
	openShellClient, err := env.NewClientWithTLS(openShellAddr, tlsConfig)
	if err != nil {
		log.Fatalf("[server] FATAL: OpenShell gateway connection failed at %s: %v\nServer refusing to start.", openShellAddr, err)
	}

	loopClient := env.NewSDKAdapter(openShellClient)

	// Create workspace manager — handles sandbox creation with context file injection.
	serverURL := cfg.System.ServerURL
	if serverURL == "" {
		serverURL = fmt.Sprintf("http://%s:%d", cfg.System.Host, cfg.System.ServerPort)
	}
	wsManager := env.NewWorkspaceManager(openShellClient, serverURL)

	// Initialize raw sources store for environment snapshots (context injection).
	rawSourcesDir := os.Getenv("NORMAN_RAW_SOURCES_DIR")
	if rawSourcesDir == "" {
		rawSourcesDir = os.ExpandEnv("$HOME/.norman/raw_sources") // fallback for local dev
	}
	rawSources, err := memory.NewRawSources(rawSourcesDir)
	if err != nil {
		log.Fatalf("failed to init raw sources store: %v", err)
	}

	// Create agent loop (with concrete client for P3 sandbox dep install)
	loop := agent.NewWithClient(cfg, store, rawSources, activator, wsHub, loopClient, openShellClient)

	// Wire LLM client into the agent loop.
	if llmClient != nil {
		loop = loop.WithLLMClient(llmClient)
	}

	// Initialize notification notifier from config
	notifier := notify.New(wsHub, cfg.System.Notify.BellEnabled, cfg.System.Notify.SlackWebhook, cfg.System.Notify.RateLimitPerMin)

	// Initialize scheduler and wire results into loop's notification path
	resultsCh := make(chan scheduler.TaskResult, 100) // buffered to avoid blocking
	sched := scheduler.NewScheduler(resultsCh)

	// Wire OpenShell Exec() into the scheduler's runCommand adapter
	sched.WithRunCommand(func(ctx context.Context, provider, sandbox string, cmd []string) (*v1.ExecResult, error) {
		return openShellClient.RunCommand(ctx, provider, sandbox, cmd)
	})

	// Wire OutputHandler to push WS events for real-time progress updates.
	sched.WithOutputHandler(func(result string, err error) {
		msg := result
		if err != nil {
			msg = fmt.Sprintf("Task completed with error: %v", err)
		}
		wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr(msg),
		})
	})

	loop = loop.WithNotifier(notifier).WithResultsChannel(resultsCh)

	// Build server with all dependencies
	registryPath := filepath.Join(os.Getenv("HOME"), ".norman", "skills_registry.json")
	reg := skills.NewRegistry(registryPath)

	srv := &server{
		cfg:              cfg,
		store:            store,
		rawSources:       rawSources,
		activator:        activator,
		loop:             loop,
		wsHub:            wsHub,
		notifier:         notifier,
		scheduler:        sched,
		registry:         reg,
		workspaceManager: wsManager,
		workspaces:       make(map[string]*protocol.Workspace),
		startTime:        time.Now(),
	}

	// Seed registry from activator's current entries (host-installed skills)
	for name, entry := range activator.Registry() {
		_ = reg.Register(name, skills.RegistryEntry{
			SourceURL:             entry.SourceURL,
			Version:               entry.Version,
			Description:           entry.Description,
			InstalledInWorkspaces: []string{},
		})
	}

	// Load persisted workspaces from disk on startup
	if loaded, err := store.ListWorkspaces(); err != nil {
		log.Printf("server: failed to load workspaces from disk: %v", err)
	} else {
		for _, wsMap := range loaded {
			if name, ok := wsMap["name"].(string); ok {
				ws := &protocol.Workspace{
					Id:        nil,
					Name:      strPtr(name),
					Path:      strPtr(wsMap["path"].(string)),
					Status:    (*protocol.WorkspaceStatus)(strPtr(wsMap["status"].(string))),
					CreatedAt: nil,
				}
				if idStr, ok := wsMap["id"].(string); ok {
					id, err := uuid.Parse(idStr)
					if err == nil {
						ws.Id = &id
					}
				}
				if purpose, ok := wsMap["purpose"].(string); ok {
					ws.Purpose = strPtr(purpose)
				}
				if scope, ok := wsMap["scope"].([]interface{}); ok {
					strSlice := make([]string, len(scope))
					for i, v := range scope {
						if s, ok := v.(string); ok {
							strSlice[i] = s
						}
					}
					ws.Scope = &strSlice
				}
				if tags, ok := wsMap["tags"].([]interface{}); ok {
					strSlice := make([]string, len(tags))
					for i, v := range tags {
						if s, ok := v.(string); ok {
							strSlice[i] = s
						}
					}
					ws.Tags = &strSlice
				}
				srv.workspaces[name] = ws
			}
		}
		log.Printf("server: loaded %d persisted workspaces from disk", len(srv.workspaces))
	}

	// Load persisted scheduled tasks into the scheduler on startup.
	if scheduled, err := store.LoadScheduledTasks(); err != nil {
		log.Printf("scheduler: failed to load scheduled tasks from disk: %v", err)
	} else if scheduled != nil {
		for _, entryMap := range scheduled {
			entry, ok := entryMap.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := entry["name"].(string)
			schedule, _ := entry["schedule"].(string)
			command, _ := entry["command"].(string)
			active, _ := entry["active"].(bool)
			entryID, _ := entry["id"].(string)

			if entryID == "" || name == "" {
				continue
			}

			schedEntry := scheduler.TaskEntry{
				ID:       entryID,
				Name:     name,
				Schedule: schedule,
				Command:  command,
				Active:   active,
			}
			if err := sched.Add(schedEntry); err != nil {
				log.Printf("scheduler: failed to add task %s: %v", entryID[:8], err)
			}
		}
		log.Printf("scheduler: loaded %d persisted scheduled tasks from disk", len(scheduled))
	}

	return srv
}
