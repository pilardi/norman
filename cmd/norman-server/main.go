// Package main implements the Norman Agent Server — a single long-running process
// exposing HTTP API + WebSocket events via Echo.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/norman/norman/pkg/agent"
	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/memory"
	"github.com/norman/norman/pkg/notify"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/scheduler"
	"github.com/norman/norman/pkg/skills"
)

// server holds all dependencies injected at startup.
type server struct {
	cfg        *config.Config
	store      *memory.Store
	rawSources *memory.RawSources
	activator  *skills.Activator
	loop       *agent.Loop
	wsHub      *WSHub
	notifier   *notify.Notifier
	scheduler  *scheduler.Scheduler
	registry   *skills.Registry

	// workspaceRegistry tracks created workspaces (in-memory + persisted).
	mu               sync.Mutex
	taskCancellations sync.Map // taskID -> struct{}{} for cancellation signaling
	workspaces       map[string]*protocol.Workspace
	startTime        time.Time
}

func main() {
	// --help / -h: print usage and exit.
	for _, arg := range os.Args[1:] {
		if arg == "--help" || arg == "-h" {
			fmt.Fprint(os.Stderr, config.ServerHelpText())
			os.Exit(0)
		}
	}

	fmt.Fprintf(os.Stderr, "Norman agent server starting...\n")

	// Bootstrap all dependencies (config, providers, stores, loops, schedulers).
	srv := bootstrap()

	host := srv.cfg.System.Host
	port := srv.cfg.System.ServerPort

	// Register routes on Echo router.
	e := echo.New()
	registerRoutes(e, srv)

	log.Printf("Norman Agent Server starting on %s:%d", host, port)

	// Start scheduler background goroutine.
	schedCtx, schedCancel := context.WithCancel(context.Background())
	srv.scheduler.Start(schedCtx)

	// Start idle timeout auto-cleanup goroutine (cleanup sessions inactive for >30min by default).
	go srv.idleCleanupLoop(30 * time.Minute)

	if err := e.Start(host + ":" + fmt.Sprintf("%d", port)); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
		schedCancel() // stop scheduler goroutine
		e.Logger.Info("shutting down...")
		e.Shutdown(context.Background())
	}()

	<-ctx.Done()
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string { return &s }

// intPtr returns a pointer to the given int.
func intPtr(i int) *int { return &i }
