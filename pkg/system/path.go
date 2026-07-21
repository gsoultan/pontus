package system

import (
	"os"
	"path/filepath"
	"runtime"
)

// GetDefaultDataDir returns the default data directory for the current operating system.
func GetDefaultDataDir() string {
	// If environment variable is set, it takes precedence
	if envDir := os.Getenv("PONTUS_DATA_DIR"); envDir != "" {
		return envDir
	}

	switch runtime.GOOS {
	case "windows":
		// Use C:\ProgramData\Pontus if possible
		programData := os.Getenv("ProgramData")
		if programData != "" {
			return filepath.Join(programData, "Pontus")
		}
		// Fallback to %AppData%\Pontus
		appData := os.Getenv("AppData")
		if appData != "" {
			return filepath.Join(appData, "Pontus")
		}
	case "darwin":
		// Use /Library/Application Support/Pontus
		return "/Library/Application Support/Pontus"
	case "linux":
		// Use /var/lib/pontus for system-wide installation
		return "/var/lib/pontus"
	}

	// Fallback for all OS if not running as system service
	// Use user-specific data directory
	dataDir, err := os.UserConfigDir()
	if err == nil {
		return filepath.Join(dataDir, "pontus")
	}

	// Ultimate fallback to current directory
	return "."
}

// GetDatabasePath returns the full path to a database file, ensuring the directory exists.
// If baseDir is empty, it uses the default data directory.
func GetDatabasePath(filename string, baseDir string) (string, error) {
	// If filename is already an absolute path, return it as is
	if filepath.IsAbs(filename) {
		return filename, nil
	}

	if baseDir == "" {
		baseDir = GetDefaultDataDir()
	}

	// If it's the current directory, just return the filename
	if baseDir == "." {
		return filename, nil
	}

	// Ensure the directory exists
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		// If we can't create the directory (e.g. permission denied),
		// fallback to current directory
		return filename, nil
	}

	return filepath.Join(baseDir, filename), nil
}

// GetPostgresDataDirs returns the default PostgreSQL data directories for the current OS.
func GetPostgresDataDirs() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			"C:\\Program Files\\PostgreSQL",
			"C:\\PostgreSQL",
			filepath.Join(os.Getenv("ProgramData"), "PostgreSQL"),
		}
	case "darwin":
		return []string{
			"/Library/PostgreSQL",
			"/usr/local/var/postgres",
			"/opt/homebrew/var/postgres",
		}
	case "linux":
		return []string{
			"/var/lib/postgresql",
			"/etc/postgresql",
			"/var/lib/pgsql",
		}
	default:
		return []string{"/var/lib/postgresql"}
	}
}

// DetectPostgresDataDir attempts to find the actual PostgreSQL data directory.
func DetectPostgresDataDir() string {
	dirs := GetPostgresDataDirs()
	for _, dir := range dirs {
		if _, err := os.Stat(dir); err == nil {
			// Check for typical postgres files
			if _, err := os.Stat(filepath.Join(dir, "PG_VERSION")); err == nil {
				return dir
			}
			// Check subdirectories (common in /var/lib/postgresql/16/main)
			entries, err := os.ReadDir(dir)
			if err == nil {
				for _, entry := range entries {
					if entry.IsDir() {
						subDir := filepath.Join(dir, entry.Name())
						if _, err := os.Stat(filepath.Join(subDir, "PG_VERSION")); err == nil {
							return subDir
						}
						// One more level (e.g. /var/lib/postgresql/16/main)
						subEntries, err := os.ReadDir(subDir)
						if err == nil {
							for _, subEntry := range subEntries {
								if subEntry.IsDir() {
									mainDir := filepath.Join(subDir, subEntry.Name())
									if _, err := os.Stat(filepath.Join(mainDir, "PG_VERSION")); err == nil {
										return mainDir
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Default fallbacks based on OS
	switch runtime.GOOS {
	case "windows":
		return "C:\\Program Files\\PostgreSQL\\data"
	case "darwin":
		return "/usr/local/var/postgres"
	default:
		return "/var/lib/postgresql/data"
	}
}

// GetDefaultLogDir returns the default directory for log files.
func GetDefaultLogDir() string {
	switch runtime.GOOS {
	case "windows":
		programData := os.Getenv("ProgramData")
		if programData != "" {
			return filepath.Join(programData, "Pontus", "logs")
		}
	case "darwin", "linux":
		return "/var/log/pontus"
	}
	return "."
}

// OSInfo contains information about the current operating system.
type OSInfo struct {
	OS      string
	Arch    string
	DataDir string
}

// GetOSInfo returns information about the current operating system.
func GetOSInfo() OSInfo {
	return OSInfo{
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		DataDir: GetDefaultDataDir(),
	}
}
