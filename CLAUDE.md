# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build Commands

```bash
# Build
go build -o claude-tasks ./cmd/claude-tasks

# Run tests
go test -v ./...

# Run a single test
go test -v -run TestName ./internal/package/...

# Lint (requires golangci-lint)
golangci-lint run --timeout=5m

# Release build with version info and optimizations
VERSION=$(git describe --tags --always)
COMMIT=$(git rev-parse --short HEAD)
DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
go build -ldflags="-s -w -X github.com/kylemclaren/claude-tasks/internal/version.Version=$VERSION -X github.com/kylemclaren/claude-tasks/internal/version.Commit=$COMMIT -X github.com/kylemclaren/claude-tasks/internal/version.BuildDate=$DATE" -o claude-tasks ./cmd/claude-tasks
```

## CLI Commands

```bash
claude-tasks              # Launch the interactive TUI
claude-tasks daemon       # Run scheduler in foreground (for services)
claude-tasks serve        # Run HTTP API server (default port 8080)
claude-tasks serve --port 3000  # Run API on custom port
claude-tasks version      # Show version information
claude-tasks upgrade      # Upgrade to the latest version
claude-tasks help         # Show help message
```

## Architecture

Claude Tasks is a Go TUI application for scheduling Claude CLI tasks via cron expressions. The data is stored in SQLite at `~/.claude-tasks/tasks.db`.

### Package Structure

```
cmd/claude-tasks/main.go   Entry point - CLI commands, initializes DB, starts scheduler, launches TUI
internal/
  api/                     HTTP REST API server (chi router) for mobile/remote access
  tui/                     Bubble Tea TUI (views: list, add, edit, output, settings)
  scheduler/               Cron job scheduling (robfig/cron, 6-field with seconds)
  executor/                Claude CLI subprocess execution, captures output
  db/                      SQLite models (Task, TaskRun) and CRUD operations
  usage/                   Anthropic API usage tracking, threshold enforcement
  webhook/                 Discord and Slack webhook notifications
  version/                 Version info (set at build time via ldflags)
  upgrade/                 Self-update from GitHub releases
mobile/                    Expo/React Native app (connects to API server)
```

### Execution Flow

1. Scheduler triggers task based on cron expression
2. Executor checks API usage against threshold (default 80%)
3. If under threshold, spawns Claude CLI with task prompt in configured working directory
4. Captures output, creates TaskRun record
5. Posts to Discord/Slack webhooks if configured
6. Updates next run time

### Key Dependencies

- **charmbracelet/bubbletea** - TUI framework
- **charmbracelet/bubbles** - Table, spinner, viewport, progress components
- **charmbracelet/lipgloss** - Terminal styling
- **charmbracelet/glamour** - Markdown rendering
- **go-chi/chi/v5** - HTTP router for REST API
- **robfig/cron/v3** - Cron scheduling (6-field: `second minute hour day month weekday`)
- **mattn/go-sqlite3** - SQLite driver (CGO required)

### Data Storage

- Default database path: `~/.claude-tasks/tasks.db`
- Override with the `--db PATH` flag (absolute path to the SQLite file) on every command
- Database auto-migrates on startup
- `daemon.pid` file (co-located with the DB) tracks the running daemon process

### Operating Modes

1. **TUI Mode** (default): Interactive terminal UI with embedded scheduler
2. **Daemon Mode** (`daemon`): Headless scheduler, TUI connects as client
3. **Server Mode** (`serve`): REST API + scheduler for mobile/remote access

When a daemon is running, the TUI detects it via PID file and operates in client mode (no duplicate scheduler).

### REST API

The `serve` command starts an HTTP server with these endpoints:

```
GET    /api/v1/health              Health check
GET    /api/v1/tasks               List all tasks
POST   /api/v1/tasks               Create task
GET    /api/v1/tasks/{id}          Get task by ID
PUT    /api/v1/tasks/{id}          Update task
DELETE /api/v1/tasks/{id}          Delete task
POST   /api/v1/tasks/{id}/toggle   Toggle enabled
POST   /api/v1/tasks/{id}/run      Run immediately
GET    /api/v1/tasks/{id}/runs     Get task run history
GET    /api/v1/tasks/{id}/runs/latest  Get latest run
GET    /api/v1/settings            Get settings
PUT    /api/v1/settings            Update settings
GET    /api/v1/usage               Get API usage stats
```

## Mobile App

The `mobile/` directory contains an Expo/React Native app that connects to the REST API.

```bash
cd mobile
npm install
npm start         # Start Expo dev server
npm run ios       # iOS simulator
npm run android   # Android emulator
```

The app requires the API server running (`claude-tasks serve`) and configured via the setup screen.

## TUI Keybindings

| Key | Action |
|-----|--------|
| `a` | Add task |
| `e` | Edit task |
| `d` | Delete task (with confirmation) |
| `t` | Toggle enabled |
| `r` | Run immediately |
| `Enter` | View output |
| `s` | Settings |
| `/` | Search/filter tasks |
| `?` | Cron preset picker (in cron field) |
| `q` | Quit |
