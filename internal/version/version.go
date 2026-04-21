package version

import (
	"fmt"
	"runtime"
)

// These variables are set at build time using ldflags
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info returns formatted version information
func Info() string {
	return fmt.Sprintf("ai-tasks %s\nCommit: %s\nBuilt: %s\nGo: %s\nOS/Arch: %s/%s",
		Version, Commit, BuildDate, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// Short returns just the version string
func Short() string {
	return Version
}

// UserAgent returns a user agent string for HTTP requests
func UserAgent() string {
	return fmt.Sprintf("ai-tasks/%s", Version)
}
