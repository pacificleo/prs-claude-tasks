package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kylemclaren/claude-tasks/internal/api"
	"github.com/kylemclaren/claude-tasks/internal/db"
	"github.com/kylemclaren/claude-tasks/internal/scheduler"
	"github.com/kylemclaren/claude-tasks/internal/tui"
	"github.com/kylemclaren/claude-tasks/internal/upgrade"
	"github.com/kylemclaren/claude-tasks/internal/version"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(version.Info())
			return
		case "upgrade":
			if err := upgrade.Upgrade(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "help", "--help", "-h":
			printHelp()
			return
		case "daemon":
			if err := runDaemon(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "serve":
			if err := runServer(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		default:
			// Anything that's not a known subcommand and doesn't look like a
			// flag is rejected; flags fall through to TUI mode so users can
			// run `claude-tasks --db /tmp/foo.db`.
			if !strings.HasPrefix(os.Args[1], "-") {
				fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
				printHelp()
				os.Exit(1)
			}
		}
	}

	if err := runTUI(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// defaultDBPath returns the default --db value: ~/.ai-tasks/tasks.db.
// Falls back to "tasks.db" in the current directory if the home directory
// can't be resolved (extremely unusual).
func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "tasks.db"
	}
	return filepath.Join(home, ".ai-tasks", "tasks.db")
}

// pidPathFor returns the daemon PID file path co-located with the DB.
func pidPathFor(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "daemon.pid")
}

// addDBFlag registers the shared --db flag on fs and returns the bound value.
func addDBFlag(fs *flag.FlagSet) *string {
	return fs.String("db", defaultDBPath(), "Absolute path to the SQLite database file")
}

func runTUI(args []string) error {
	fs := flag.NewFlagSet("claude-tasks", flag.ExitOnError)
	dbPath := addDBFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	database, err := db.New(*dbPath)
	if err != nil {
		return fmt.Errorf("initializing database: %w", err)
	}
	defer database.Close()

	pidPath := pidPathFor(*dbPath)
	daemonPID, daemonRunning := isDaemonRunning(pidPath)

	var sched *scheduler.Scheduler
	if daemonRunning {
		fmt.Printf("Daemon running (PID %d), TUI in client mode\n", daemonPID)
	} else {
		sched = scheduler.New(database)
		if err := sched.Start(); err != nil {
			return fmt.Errorf("starting scheduler: %w", err)
		}
		defer sched.Stop()
	}

	return tui.Run(database, sched, daemonRunning)
}

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	dbPath := addDBFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	pidPath := pidPathFor(*dbPath)

	if pid, running := isDaemonRunning(pidPath); running {
		return fmt.Errorf("daemon already running (PID %d)", pid)
	}

	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	defer os.Remove(pidPath)

	database, err := db.New(*dbPath)
	if err != nil {
		return fmt.Errorf("initializing database: %w", err)
	}
	defer database.Close()

	sched := scheduler.New(database)
	if err := sched.Start(); err != nil {
		return fmt.Errorf("starting scheduler: %w", err)
	}
	defer sched.Stop()

	fmt.Println("claude-tasks daemon started")
	fmt.Printf("PID: %d\n", os.Getpid())
	fmt.Printf("Database: %s\n", *dbPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
	return nil
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := addDBFlag(fs)
	port := fs.Int("port", 8080, "HTTP server port")
	if err := fs.Parse(args); err != nil {
		return err
	}

	database, err := db.New(*dbPath)
	if err != nil {
		return fmt.Errorf("initializing database: %w", err)
	}
	defer database.Close()

	sched := scheduler.New(database)
	if err := sched.Start(); err != nil {
		return fmt.Errorf("starting scheduler: %w", err)
	}
	defer sched.Stop()

	server := api.NewServer(database, sched)

	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("claude-tasks API server starting on %s\n", addr)
	fmt.Printf("Database: %s\n", *dbPath)

	srv := &http.Server{
		Addr:    addr,
		Handler: server.Router(),
	}

	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return srv.Shutdown(ctx)
}

// isDaemonRunning checks if a daemon is running by reading PID file and checking process
func isDaemonRunning(pidPath string) (int, bool) {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, false
	}

	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0, false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}

	// On Unix, FindProcess always succeeds, so send signal 0 to check if alive
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return 0, false
	}

	return pid, true
}

func printHelp() {
	fmt.Println(`ai-tasks - Schedule and run Claude CLI tasks via cron

Usage:
  ai-tasks [--db PATH]                    Launch the interactive TUI
  ai-tasks daemon [--db PATH]             Run scheduler in foreground (for services)
  ai-tasks serve  [--db PATH] [--port N]  Run HTTP API server (for mobile/remote access)
  ai-tasks version                        Show version information
  ai-tasks upgrade                        Upgrade to the latest version
  ai-tasks help                           Show this help message

Flags:
  --db PATH    Absolute path to the SQLite database file
               (default: ~/.ai-tasks/tasks.db)
  --port N     HTTP server port for 'serve' (default: 8080)

For more information, visit: https://github.com/kylemclaren/claude-tasks`)
}
