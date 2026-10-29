// Package main implements the Norman Agent Server — a single long-running process
// exposing HTTP API + WebSocket events via Echo.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/norman/norman/pkg/agent"
	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/docs"
	"github.com/norman/norman/pkg/env"
	"github.com/norman/norman/pkg/memory"
	"github.com/norman/norman/pkg/notify"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/scheduler"
	"github.com/norman/norman/pkg/skills"
	"github.com/urfave/cli/v3"
)

// server holds all dependencies injected at startup.
type server struct {
	cfg              *config.Config
	store            *memory.Store
	rawSources       *memory.RawSources
	activator        *skills.Activator
	loop             *agent.Loop
	wsHub            *WSHub
	notifier         *notify.Notifier
	scheduler        *scheduler.Scheduler
	registry         *skills.Registry
	workspaceManager *env.WorkspaceManager

	// workspaceRegistry tracks created workspaces (in-memory + persisted).
	mu                sync.Mutex
	taskCancellations sync.Map // taskID -> struct{}{} for cancellation signaling
	workspaces        map[string]*protocol.Workspace
	startTime         time.Time
}

func main() {
	app := &cli.Command{
		Name:                  "norman-server",
		Description:           "Norman Agent Server — single long-running process exposing HTTP API + WebSocket events via Echo",
		EnableShellCompletion: true,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "Enable verbose debug output (overrides config.yaml Logging.Verbose)",
			},
			&cli.StringFlag{
				Name:    "provider",
				Aliases: []string{"p"},
				Usage:   "Select which configured provider to use (overrides default_provider)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := runServer(cmd.Bool("verbose"), cmd.String("provider")); err != nil {
				return err
			}
			return nil
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		os.Exit(1)
	}
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string { return &s }

// intPtr returns a pointer to the given int.
func intPtr(i int) *int { return &i }

func runServer(verbose bool, selectedProvider string) error {
	fmt.Fprintf(os.Stderr, "Norman agent server starting...\n")

	srv := bootstrap(verbose, selectedProvider)

	host := srv.cfg.System.Host
	port := srv.cfg.System.ServerPort

	// Register routes on Echo router.
	e := echo.New()
	registerRoutes(e, srv)

	// API reference documentation (Scalar-powered).
	e.GET("/docs", docs.Handler("api-spec.yaml", "Norman API Reference", false, ":root{--bg-color:#000000;--color-gold:#D4AF37;--color-red:#A52A2A;--color-silver:#E5E5E5;--color-charcoal:#1C1C1C}"))

	log.Printf("Norman Agent Server starting on %s:%d", host, port)

	// Start scheduler background goroutine.
	schedCtx, schedCancel := context.WithCancel(context.Background())
	srv.scheduler.Start(schedCtx)

	// Start idle timeout auto-cleanup goroutine (cleanup sessions inactive for >30min by default).
	go srv.idleCleanupLoop(30 * time.Minute)

	// Graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down...")
	schedCancel()
	_ = e.Close()
	return nil
}
