package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the current version of the application.
	Version = "dev"
	// Commit is the git commit hash.
	Commit = "none"
	// BuildTime is the time when the binary was built.
	BuildTime = "unknown"
)

// Info returns a formatted string with version information.
func Info() string {
	return fmt.Sprintf("Version: %s\nCommit: %s\nBuildTime: %s\nGoVersion: %s\nOS/Arch: %s/%s",
		Version, Commit, BuildTime, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
