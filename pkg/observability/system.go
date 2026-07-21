package observability

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// SystemMetricsData holds the collected system performance data.
type SystemMetricsData struct {
	CPUUsagePercent     float64
	MemoryTotalBytes    uint64
	MemoryUsedBytes     uint64
	MemoryUsagePercent  float64
	StorageTotalBytes   uint64
	StorageUsedBytes    uint64
	StorageUsagePercent float64
	Load1               float64
	Load5               float64
	Load15              float64

	// Advanced Metrics
	ReadBytesPerSec  uint64
	WriteBytesPerSec uint64
	ReadCountPerSec  uint64
	WriteCountPerSec uint64
	IOTimeMS         float64

	BytesSentPerSec   uint64
	BytesRecvPerSec   uint64
	PacketsSentPerSec uint64
	PacketsRecvPerSec uint64
	Goroutines        int
	CPUCores          int
	OpenFilesLimit    uint64
	MaxProcessesLimit uint64
}

var (
	metricsMu   sync.RWMutex
	lastIOStat  map[string]disk.IOCountersStat
	lastNetStat []net.IOCountersStat
	lastSample  time.Time
	cachedData  SystemMetricsData
)

// CollectSystemMetrics gathers current system performance data.
func CollectSystemMetrics() SystemMetricsData {
	metricsMu.Lock()
	defer metricsMu.Unlock()

	now := time.Now()

	// If we collected less than 500ms ago, return cached to save CPU
	if !lastSample.IsZero() && now.Sub(lastSample) < 500*time.Millisecond {
		return cachedData
	}

	data := SystemMetricsData{}

	// CPU Usage
	if percentages, err := cpu.Percent(0, false); err == nil && len(percentages) > 0 {
		data.CPUUsagePercent = percentages[0]
	}

	// Memory Usage
	if v, err := mem.VirtualMemory(); err == nil {
		data.MemoryTotalBytes = v.Total
		data.MemoryUsedBytes = v.Used
		data.MemoryUsagePercent = v.UsedPercent
	}

	// Storage Usage (Root partition)
	if d, err := disk.Usage("/"); err == nil {
		data.StorageTotalBytes = d.Total
		data.StorageUsedBytes = d.Used
		data.StorageUsagePercent = d.UsedPercent
	}

	// System Load
	if l, err := load.Avg(); err == nil {
		data.Load1 = l.Load1
		data.Load5 = l.Load5
		data.Load15 = l.Load15
	}

	// Disk I/O
	if ioStats, err := disk.IOCounters(); err == nil {
		if !lastSample.IsZero() {
			duration := now.Sub(lastSample).Seconds()
			var totalRead, totalWrite, totalReadCount, totalWriteCount uint64
			var totalIOTime uint64
			for k, v := range ioStats {
				if last, ok := lastIOStat[k]; ok {
					totalRead += v.ReadBytes - last.ReadBytes
					totalWrite += v.WriteBytes - last.WriteBytes
					totalReadCount += v.ReadCount - last.ReadCount
					totalWriteCount += v.WriteCount - last.WriteCount
					totalIOTime += v.IoTime - last.IoTime
				}
			}
			data.ReadBytesPerSec = uint64(float64(totalRead) / duration)
			data.WriteBytesPerSec = uint64(float64(totalWrite) / duration)
			data.ReadCountPerSec = uint64(float64(totalReadCount) / duration)
			data.WriteCountPerSec = uint64(float64(totalWriteCount) / duration)
			data.IOTimeMS = float64(totalIOTime) / duration
		}
		lastIOStat = ioStats
	}

	// Network I/O
	if netStats, err := net.IOCounters(false); err == nil && len(netStats) > 0 {
		if !lastSample.IsZero() && len(lastNetStat) > 0 {
			duration := now.Sub(lastSample).Seconds()
			v := netStats[0]
			last := lastNetStat[0]
			data.BytesSentPerSec = uint64(float64(v.BytesSent-last.BytesSent) / duration)
			data.BytesRecvPerSec = uint64(float64(v.BytesRecv-last.BytesRecv) / duration)
			data.PacketsSentPerSec = uint64(float64(v.PacketsSent-last.PacketsSent) / duration)
			data.PacketsRecvPerSec = uint64(float64(v.PacketsRecv-last.PacketsRecv) / duration)
		}
		lastNetStat = netStats
	}

	data.Goroutines = runtime.NumGoroutine()
	data.CPUCores = runtime.NumCPU()

	lastSample = now
	cachedData = data
	return data
}

// StartSystemMetricsReporting starts a background loop to update Prometheus metrics.
func StartSystemMetricsReporting(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				data := CollectSystemMetrics()
				CPUUsage.Set(data.CPUUsagePercent)
				MemoryUsage.Set(data.MemoryUsagePercent)
				StorageUsage.Set(data.StorageUsagePercent)
				Load1.Set(data.Load1)
			case <-ctx.Done():
				return
			}
		}
	}()
}
