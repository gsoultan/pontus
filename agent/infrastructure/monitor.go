package infrastructure

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/pontus/agent/services"
	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/pkg/observability"
	"github.com/gsoultan/pontus/pkg/system"
	"github.com/gsoultan/pontus/pkg/version"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type healthMetrics struct {
	ioWaitTrend    []float64
	latencyTrend   []float64
	lastCollection time.Time
}

// monitor implements the services.Monitor interface.
type monitor struct {
	dbCollector  observability.DatabaseCollector
	repoManager  services.RepositoryManager
	staticInfo   *endpoints.GetSystemInfoResponse
	staticMu     sync.RWMutex
	metricsMu    sync.Mutex
	history      healthMetrics
	repoVersions map[int]string
}

// NewMonitor creates a new monitor instance.
func NewMonitor(collector observability.DatabaseCollector, repoManager services.RepositoryManager) *monitor {
	return &monitor{
		dbCollector: collector,
		repoManager: repoManager,
	}
}

func (m *monitor) detectPostgresVersions() (string, []string) {
	var available []string
	detected := ""

	// Check common installation paths
	paths := []string{
		"/usr/lib/postgresql/",          // Debian/Ubuntu
		"/usr/pgsql-",                   // RHEL/CentOS
		"C:\\Program Files\\PostgreSQL", // Windows
	}

	for _, p := range paths {
		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				name := entry.Name()
				// Expecting version numbers
				if name[0] >= '0' && name[0] <= '9' {
					available = append(available, name)
				}
			}
		}
	}

	// Try pg_config if available
	if cmd := exec.Command("pg_config", "--version"); cmd.Run() == nil {
		out, _ := exec.Command("pg_config", "--version").Output()
		// "PostgreSQL 17.2" -> "17"
		parts := strings.Fields(string(out))
		if len(parts) >= 2 {
			versionParts := strings.Split(parts[1], ".")
			if len(versionParts) > 0 {
				detected = versionParts[0]
			}
		}
	}

	// If multiple available, pick latest as detected if not already set
	if detected == "" && len(available) > 0 {
		slices.Sort(available)
		detected = available[len(available)-1]
	}

	return detected, available
}

func (m *monitor) checkPostgresRunning() (bool, string) {
	// 1. Try pg_isready if available (most reliable)
	if _, err := exec.LookPath("pg_isready"); err == nil {
		if err := exec.Command("pg_isready", "-h", "localhost", "-p", "5432").Run(); err == nil {
			return true, "localhost:5432"
		}
	}

	// 2. Try to connect to default postgres port
	conn, err := net.DialTimeout("tcp", "localhost:5432", 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return true, "localhost:5432"
	}

	// 3. Check if process is running (fallback)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("tasklist", "/FI", "IMAGENAME eq postgres.exe")
	} else {
		cmd = exec.Command("pgrep", "postgres")
	}

	if err := cmd.Run(); err == nil {
		return true, "localhost:5432"
	}

	return false, ""
}

func (m *monitor) Start(ctx context.Context) error {
	// Initial collection
	m.refreshStaticInfo(ctx)

	// Periodic refresh
	go func() {
		ticker := time.NewTicker(1 * time.Hour) // Check every hour
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.refreshStaticInfo(ctx)
			}
		}
	}()
	return nil
}

func (m *monitor) refreshStaticInfo(ctx context.Context) {
	hostname, _ := os.Hostname()
	env := os.Environ()
	envMap := make(map[string]string, len(env))
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			envMap[k] = v
		}
	}

	detected, available := m.detectPostgresVersions()
	isRunning, pgAddr := m.checkPostgresRunning()
	postgresDataDir := system.DetectPostgresDataDir()
	var repoVersions map[int]string
	var err error

	if m.repoManager != nil {
		repoVersions, err = m.repoManager.GetPostgresVersions(ctx)
		if err == nil {
			m.metricsMu.Lock()
			m.repoVersions = repoVersions
			m.metricsMu.Unlock()

			for major := range repoVersions {
				available = append(available, strconv.Itoa(major))
			}
		}
	}

	slices.Sort(available)
	available = slices.Compact(available)

	// Calculate recommended version
	recommended := ""
	if detected != "" {
		major, _ := strconv.Atoi(detected)
		if major > 0 && repoVersions != nil {
			// Find latest major version
			latestMajor := 0
			for majorInRepo := range repoVersions {
				if majorInRepo > latestMajor {
					latestMajor = majorInRepo
				}
			}
			if latestMajor > major {
				recommended = strconv.Itoa(latestMajor)
			}
		}
	}

	m.staticMu.Lock()
	m.staticInfo = &endpoints.GetSystemInfoResponse{
		Os:                 runtime.GOOS,
		Hostname:           hostname,
		EnvVars:            envMap,
		DetectedVersion:    detected,
		AvailableVersions:  available,
		AgentVersion:       version.Version,
		RecommendedVersion: recommended,
		PostgresRunning:    isRunning,
		PostgresAddress:    pgAddr,
		PostgresDataDir:    postgresDataDir,
	}
	m.staticMu.Unlock()
}

func (m *monitor) GetSystemInfo(ctx context.Context) (*endpoints.GetSystemInfoResponse, error) {
	m.staticMu.RLock()
	static := m.staticInfo
	m.staticMu.RUnlock()

	if static == nil {
		m.refreshStaticInfo(ctx)
		m.staticMu.RLock()
		static = m.staticInfo
		m.staticMu.RUnlock()
	}

	systemMetrics := observability.CollectSystemMetrics()
	var dbMetrics observability.DatabaseMetricsData
	if m.dbCollector != nil {
		dbMetrics, _ = m.dbCollector.Collect(ctx)
	}

	// Predictive Health Monitoring
	m.metricsMu.Lock()
	m.history.ioWaitTrend = append(m.history.ioWaitTrend, float64(systemMetrics.IOTimeMS))
	if len(m.history.ioWaitTrend) > 60 {
		m.history.ioWaitTrend = m.history.ioWaitTrend[1:]
	}
	prediction := m.predictHealth()
	m.metricsMu.Unlock()

	tuning := m.getTuningSuggestions(systemMetrics.MemoryTotalBytes)

	return &endpoints.GetSystemInfoResponse{
		Os:                 m.staticInfo.Os,
		Hostname:           m.staticInfo.Hostname,
		EnvVars:            m.staticInfo.EnvVars,
		DetectedVersion:    m.staticInfo.DetectedVersion,
		AvailableVersions:  m.staticInfo.AvailableVersions,
		AgentVersion:       m.staticInfo.AgentVersion,
		RecommendedVersion: m.staticInfo.RecommendedVersion,
		PostgresRunning:    m.staticInfo.PostgresRunning,
		PostgresAddress:    m.staticInfo.PostgresAddress,
		PostgresDataDir:    m.staticInfo.PostgresDataDir,
		HealthPrediction:   prediction,
		TuningSuggestions:  tuning,
		Metrics: &domain.SystemMetrics{
			CpuUsagePercent:     float32(systemMetrics.CPUUsagePercent),
			CpuCores:            uint32(systemMetrics.CPUCores),
			MemoryTotalBytes:    systemMetrics.MemoryTotalBytes,
			MemoryUsedBytes:     systemMetrics.MemoryUsedBytes,
			MemoryUsagePercent:  float32(systemMetrics.MemoryUsagePercent),
			StorageTotalBytes:   systemMetrics.StorageTotalBytes,
			StorageUsedBytes:    systemMetrics.StorageUsedBytes,
			StorageUsagePercent: float32(systemMetrics.StorageUsagePercent),
			Load_1:              float32(systemMetrics.Load1),
			Load_5:              float32(systemMetrics.Load5),
			Load_15:             float32(systemMetrics.Load15),
			Goroutines:          uint64(systemMetrics.Goroutines),
			OpenFilesLimit:      systemMetrics.OpenFilesLimit,
			MaxProcessesLimit:   systemMetrics.MaxProcessesLimit,
			DiskIo: &domain.DiskMetrics{
				ReadBytesPerSec:  systemMetrics.ReadBytesPerSec,
				WriteBytesPerSec: systemMetrics.WriteBytesPerSec,
				ReadCountPerSec:  systemMetrics.ReadCountPerSec,
				WriteCountPerSec: systemMetrics.WriteCountPerSec,
				IotimeMs:         float32(systemMetrics.IOTimeMS),
			},
			NetworkIo: &domain.NetworkMetrics{
				BytesSentPerSec:   systemMetrics.BytesSentPerSec,
				BytesRecvPerSec:   systemMetrics.BytesRecvPerSec,
				PacketsSentPerSec: systemMetrics.PacketsSentPerSec,
				PacketsRecvPerSec: systemMetrics.PacketsRecvPerSec,
			},
			DbMetrics: &domain.DatabaseMetrics{
				ActiveBackends:         dbMetrics.ActiveBackends,
				MaxBackends:            dbMetrics.MaxBackends,
				TransactionsCommitted:  dbMetrics.TransactionsCommitted,
				TransactionsRolledBack: dbMetrics.TransactionsRolledBack,
				BlocksRead:             dbMetrics.BlocksRead,
				BlocksHit:              dbMetrics.BlocksHit,
				CacheHitRatio:          float32(dbMetrics.CacheHitRatio),
				Conflicts:              dbMetrics.Conflicts,
				Deadlocks:              dbMetrics.Deadlocks,
				ReplicationLagBytes:    dbMetrics.ReplicationLagBytes,
				IsRecovery:             dbMetrics.IsRecovery,
			},
		},
	}, nil
}

func (m *monitor) getTuningSuggestions(totalRam uint64) []*domain.TuningSuggestion {
	if totalRam == 0 {
		return nil
	}

	suggestions := []*domain.TuningSuggestion{}

	// Helper to format bytes
	formatBytes := func(bytes uint64) string {
		if bytes > 1024*1024*1024 {
			return fmt.Sprintf("%dGB", bytes/(1024*1024*1024))
		}
		return fmt.Sprintf("%dMB", bytes/(1024*1024))
	}

	// 1. shared_buffers: 25% of RAM
	sharedBuffers := totalRam / 4
	suggestions = append(suggestions, &domain.TuningSuggestion{
		Parameter:      "shared_buffers",
		SuggestedValue: formatBytes(sharedBuffers),
		Reason:         "PostgreSQL usually performs best with 25% of system RAM dedicated to shared buffers.",
	})

	// 2. effective_cache_size: 75% of RAM
	effectiveCache := (totalRam * 3) / 4
	suggestions = append(suggestions, &domain.TuningSuggestion{
		Parameter:      "effective_cache_size",
		SuggestedValue: formatBytes(effectiveCache),
		Reason:         "Setting this to 75% of total RAM helps the planner estimate how much memory is available for disk caching.",
	})

	// 3. maintenance_work_mem: 1/16 of RAM, max 2GB
	maintenanceWorkMem := totalRam / 16
	if maintenanceWorkMem > 2*1024*1024*1024 {
		maintenanceWorkMem = 2 * 1024 * 1024 * 1024
	}
	suggestions = append(suggestions, &domain.TuningSuggestion{
		Parameter:      "maintenance_work_mem",
		SuggestedValue: formatBytes(maintenanceWorkMem),
		Reason:         "Larger values improve performance for maintenance operations like VACUUM and CREATE INDEX.",
	})

	return suggestions
}

func (m *monitor) predictHealth() *endpoints.HealthPrediction {
	if len(m.history.ioWaitTrend) < 10 {
		return nil
	}

	// Simple linear regression or trend analysis for IO wait
	// If IO wait is increasing consistently, mark as degrading
	increasing := true
	for i := 1; i < len(m.history.ioWaitTrend); i++ {
		if m.history.ioWaitTrend[i] < m.history.ioWaitTrend[i-1] {
			increasing = false
			break
		}
	}

	if increasing && m.history.ioWaitTrend[len(m.history.ioWaitTrend)-1] > 100 {
		return &endpoints.HealthPrediction{
			DegradationDetected:  true,
			Reason:               "Consistent increase in Disk I/O wait time detected",
			Confidence:           0.85,
			EstimatedFailureTime: timestamppb.New(time.Now().Add(1 * time.Hour)),
		}
	}

	return &endpoints.HealthPrediction{DegradationDetected: false, Confidence: 1.0}
}

func (m *monitor) StreamLogs(ctx context.Context, req *endpoints.LogStreamRequest) (<-chan *endpoints.LogStreamResponse, error) {
	out := make(chan *endpoints.LogStreamResponse)

	go func() {
		defer close(out)

		file, err := os.Open(req.LogPath)
		if err != nil {
			return
		}
		defer file.Close()

		if req.Follow {
			file.Seek(0, io.SeekEnd)
		}

		reader := bufio.NewReader(file)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				line, err := reader.ReadString('\n')
				if line != "" {
					out <- &endpoints.LogStreamResponse{Line: strings.TrimSuffix(line, "\n")}
				}
				if err != nil {
					if err == io.EOF {
						if !req.Follow {
							return
						}
						time.Sleep(500 * time.Millisecond)
						continue
					}
					return
				}
			}
		}
	}()

	return out, nil
}

func (m *monitor) GetPostgresInsights(ctx context.Context, req *endpoints.GetPostgresInsightsRequest) (*endpoints.GetPostgresInsightsResponse, error) {
	if m.dbCollector == nil {
		return &endpoints.GetPostgresInsightsResponse{}, nil
	}

	queries, locks, replication, err := m.dbCollector.GetInsights(ctx)
	if err != nil {
		return nil, err
	}

	return &endpoints.GetPostgresInsightsResponse{
		TopQueries:        queries,
		ActiveLocks:       locks,
		ReplicationStatus: replication,
	}, nil
}
