package management

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// EnsureUIBuilt checks if the UI needs to be built and builds it if necessary.
// It looks for the "web" directory and runs bun install and bun build.
func EnsureUIBuilt() error {
	// Check if web directory exists
	webDir := "web"
	if _, err := os.Stat(webDir); os.IsNotExist(err) {
		// If we're running from cmd/pontus, web is at ../../web
		webDir = filepath.Join("..", "..", "web")
		if _, err := os.Stat(webDir); os.IsNotExist(err) {
			return fmt.Errorf("web directory not found")
		}
	}

	log.Println("UI: Ensuring UI is built...")

	// 1. Run bun install
	args := []string{"install"}
	if runtime.GOOS == "windows" {
		args = append(args, "--backend=copyfile")
	}
	if err := runCommand(webDir, "bun", args...); err != nil {
		return fmt.Errorf("failed to run bun install: %w", err)
	}

	// 2. Run bun run build
	if err := runCommand(webDir, "bun", "run", "build"); err != nil {
		return fmt.Errorf("failed to run bun build: %w", err)
	}

	log.Println("UI: Build completed successfully")
	return nil
}

func runCommand(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
