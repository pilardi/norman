package main

import (
	"log"
	"os"
	"time"

	"github.com/norman/norman/pkg/config"
)

// printStartupInfo prints diagnostic information about the server startup when verbose mode is enabled.
// It encapsulates all startup logging in a single place rather than scattering it across bootstrap/main.
func printStartupInfo(srv *server, startTime time.Time) {
	if !srv.cfg.System.Logging.Verbose {
		return
	}

	log.Println("──────────────────────────────────────────────")
	log.Println("  Norman Agent Server — Startup Diagnostics")
	log.Println("──────────────────────────────────────────────")

	// Config source
	cfgPath := config.ConfigPath()
	configExists := fileExists(cfgPath)
	if configExists {
		info, err := os.Stat(cfgPath)
		if err == nil {
			log.Printf("  [config] Loaded from: %s (modified: %s)", cfgPath, info.ModTime().Format(time.RFC3339))
		} else {
			log.Printf("  [config] Loaded from: %s (stat error: %v)", cfgPath, err)
		}
	} else {
		log.Printf("  [config] No config file at %s — using embedded defaults", cfgPath)
	}

	// Provider info
	if srv.providerName != "" {
		provCfg, err := srv.cfg.System.ResolveProvider(srv.providerName)
		if err != nil {
			log.Printf("  [provider] WARNING: resolved provider %q not found: %v", srv.providerName, err)
		} else {
			log.Printf("  [provider] Active provider: %s (type=%s, model=%s)",
				srv.providerName, provCfg.Type, provCfg.Model)
			if provCfg.Endpoint != "" {
				log.Printf("                endpoint: %s", maskSecret(provCfg.Endpoint))
			}
			if provCfg.MaxTokens > 0 {
				log.Printf("                max_tokens: %d", provCfg.MaxTokens)
			}
			if provCfg.Temperature > 0 {
				log.Printf("                temperature: %.2f", provCfg.Temperature)
			}
		}
	} else {
		log.Println("  [provider] No provider configured — using OpenAI-compatible defaults")
	}

	// Server bind info
	log.Printf("  [server] Listening on %s:%d", srv.cfg.System.Host, srv.cfg.System.ServerPort)
	if srv.cfg.System.ServerURL != "" {
		log.Printf("  [server] External URL: %s", maskSecret(srv.cfg.System.ServerURL))
	}

	// OpenShell gateway
	gwAddr := srv.cfg.System.OpenShell.Address
	if gwAddr == "" {
		log.Println("  [openshell] Gateway address auto-resolved from metadata.json")
	} else {
		log.Printf("  [openshell] Gateway: %s", gwAddr)
	}

	// Memory store
	storeDir := os.Getenv("NORMAN_STORE_DIR")
	if storeDir == "" {
		storeDir = os.ExpandEnv("$HOME/.norman/store")
	}
	log.Printf("  [memory] Store directory: %s", storeDir)

	// Raw sources
	rawSourcesDir := os.Getenv("NORMAN_RAW_SOURCES_DIR")
	if rawSourcesDir == "" {
		rawSourcesDir = os.ExpandEnv("$HOME/.norman/raw_sources")
	}
	log.Printf("  [raw-sources] Directory: %s", rawSourcesDir)

	// Skills
	log.Printf("  [skills] Discovery paths: %v", srv.cfg.System.Skills.DiscoveryPaths)
	log.Printf("  [skills] Max active skills: %d", srv.cfg.System.Skills.MaxActive)

	// Scheduler
	taskCount, lastTick := srv.scheduler.Status()
	log.Printf("  [scheduler] Loaded tasks: %d (last tick: %s)", taskCount, lastTick.Format(time.RFC3339))

	// Notification
	log.Printf("  [notify] Bell enabled: %v", srv.cfg.System.Notify.BellEnabled)
	if srv.cfg.System.Notify.SlackWebhook != "" {
		log.Printf("  [notify] Slack webhook: configured")
	}
	log.Printf("  [notify] Rate limit: %d/min", srv.cfg.System.Notify.RateLimitPerMin)

	// Uptime
	uptime := time.Since(startTime)
	log.Printf("  [timing] Startup completed in %s", uptime.Round(time.Millisecond))
	log.Println("──────────────────────────────────────────────")
}

// fileExists returns true if the file exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// maskSecret redacts the last 4 characters of a value for display purposes.
func maskSecret(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:len(s)-4] + "****"
}
