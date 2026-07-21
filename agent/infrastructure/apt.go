package infrastructure

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/gsoultan/pontus/agent/services"
)

type aptManager struct {
}

// NewAptManager creates a new instance of RepositoryManager for APT-based systems.
func NewAptManager() services.RepositoryManager {
	return &aptManager{}
}

func (a *aptManager) GetPostgresVersions(ctx context.Context) (map[int]string, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("only Linux is supported")
	}

	distro, err := a.getDistro()
	if err != nil {
		return nil, err
	}

	arch := runtime.GOARCH
	if arch == "386" {
		arch = "i386"
	}

	url := fmt.Sprintf("https://apt.postgresql.org/pub/repos/apt/dists/%s-pgdg/main/binary-%s/Packages", distro, arch)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch packages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch packages, status: %s", resp.Status)
	}

	return a.parsePackages(resp.Body)
}

func (a *aptManager) IsOSVersionOutdated(ctx context.Context, major int) (bool, error) {
	if runtime.GOOS != "linux" {
		return false, fmt.Errorf("only Linux is supported")
	}

	// Get repo version
	repoVersions, err := a.GetPostgresVersions(ctx)
	if err != nil {
		return false, err
	}

	repoVersion, ok := repoVersions[major]
	if !ok {
		return false, fmt.Errorf("major version %d not found in repository", major)
	}

	// Get OS version
	osVersion, err := a.getOSVersion(ctx, major)
	if err != nil {
		// If not found in OS, we consider it "outdated" or just missing,
		// but the requirement says "if default version from OS is lower".
		// If it's not in OS at all, we'll probably want to install it from repo anyway.
		return true, nil
	}

	return a.compareVersions(osVersion, repoVersion) < 0, nil
}

func (a *aptManager) AddPostgresRepository(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("only Linux is supported")
	}

	distro, err := a.getDistro()
	if err != nil {
		return err
	}

	// Standard commands for adding apt.postgresql.org
	// 1. Add GPG key
	// 2. Add repository to sources.list.d
	// 3. apt-get update

	commands := [][]string{
		{"sh", "-c", "curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | gpg --dearmor -o /etc/apt/trusted.gpg.d/postgresql.gpg"},
		{"sh", "-c", fmt.Sprintf("echo \"deb http://apt.postgresql.org/pub/repos/apt %s-pgdg main\" > /etc/apt/sources.list.d/pgdg.list", distro)},
		{"apt-get", "update"},
	}

	for _, cmdArgs := range commands {
		cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to execute command %v: %w", cmdArgs, err)
		}
	}

	return nil
}

func (a *aptManager) getDistro() (string, error) {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VERSION_CODENAME=") {
			return strings.Trim(strings.TrimPrefix(line, "VERSION_CODENAME="), "\""), nil
		}
	}
	return "", fmt.Errorf("could not determine distro codename")
}

func (a *aptManager) parsePackages(r io.Reader) (map[int]string, error) {
	versions := make(map[int]string)
	scanner := bufio.NewScanner(r)

	var currentPackage string
	var currentVersion string

	pkgRegex := regexp.MustCompile(`^Package: postgresql-(\d+)$`)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// End of block
			if currentPackage != "" && currentVersion != "" {
				matches := pkgRegex.FindStringSubmatch(currentPackage)
				if len(matches) > 1 {
					major, _ := strconv.Atoi(matches[1])
					// Keep the latest version found for this major
					if existing, ok := versions[major]; !ok || a.compareVersions(existing, currentVersion) < 0 {
						versions[major] = currentVersion
					}
				}
			}
			currentPackage = ""
			currentVersion = ""
			continue
		}

		if strings.HasPrefix(line, "Package: ") {
			currentPackage = line
		} else if strings.HasPrefix(line, "Version: ") {
			currentVersion = strings.TrimPrefix(line, "Version: ")
		}
	}

	return versions, nil
}

func (a *aptManager) getOSVersion(ctx context.Context, major int) (string, error) {
	cmd := exec.CommandContext(ctx, "apt-cache", "policy", fmt.Sprintf("postgresql-%d", major))
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	// Parse output to find candidate version in OS repo
	// Usually look for lines with the distro name and NOT 'pgdg'
	// Simplified: just get the Version line if it's available
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Candidate: ") {
			version := strings.TrimPrefix(line, "Candidate: ")
			if version == "(none)" {
				return "", fmt.Errorf("package not found")
			}
			return version, nil
		}
	}
	return "", fmt.Errorf("could not find version info")
}

func (a *aptManager) compareVersions(v1, v2 string) int {
	// Very basic version comparison (lexicographical for now, should be improved for real production)
	// In a real scenario, we might want to use something like 'dpkg --compare-versions'
	if v1 == v2 {
		return 0
	}
	if v1 < v2 {
		return -1
	}
	return 1
}
