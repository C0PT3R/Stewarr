package cleanup

import (
	"fmt"
	"syscall"

	"connarr/internal/model"
)

type Plan struct {
	TargetUsagePercent   float64       `json:"targetUsagePercent"`
	CriticalUsagePercent float64       `json:"criticalUsagePercent"`
	Reliable             bool          `json:"reliable"`
	Available            bool          `json:"available"`
	Path                 string        `json:"path"`
	TotalBytes           uint64        `json:"totalBytes"`
	UsedBytes            uint64        `json:"usedBytes"`
	FreeBytes            uint64        `json:"freeBytes"`
	UsagePercent         float64       `json:"usagePercent"`
	NeedBytes            uint64        `json:"needBytes"`
	Selected             []model.Media `json:"selected"`
	SelectedBytes        uint64        `json:"selectedBytes"`
	Message              string        `json:"message"`
	Error                string        `json:"error,omitempty"`
}

// Build uses Target as the sole storage-reclamation threshold. Critical is
// carried in the response for configuration compatibility, but belongs to a
// future emergency/alarm policy and never gates cleanup planning.
func Build(path string, targetUsage, criticalUsage float64, items []model.Media, reliable bool) (Plan, error) {
	plan := Plan{Path: path, TargetUsagePercent: targetUsage, CriticalUsagePercent: criticalUsage, Reliable: reliable}
	var filesystemStats syscall.Statfs_t
	if err := syscall.Statfs(path, &filesystemStats); err != nil {
		plan.Message = "Storage unavailable; cleanup planning disabled"
		plan.Error = err.Error()
		return plan, fmt.Errorf("storage path %q: %w", path, err)
	}
	totalBytes := filesystemStats.Blocks * uint64(filesystemStats.Bsize)
	if totalBytes == 0 {
		err := fmt.Errorf("filesystem reports zero total capacity")
		plan.Message = "Storage unavailable; cleanup planning disabled"
		plan.Error = err.Error()
		return plan, fmt.Errorf("storage path %q: %w", path, err)
	}
	plan.Available = true
	plan.TotalBytes = totalBytes
	freeBytes := filesystemStats.Bavail * uint64(filesystemStats.Bsize)
	usedBytes := totalBytes - freeBytes
	plan.FreeBytes = freeBytes
	plan.UsedBytes = usedBytes
	usagePercent := float64(usedBytes) / float64(totalBytes) * 100
	plan.UsagePercent = usagePercent
	if usagePercent <= targetUsage {
		plan.Message = fmt.Sprintf("No cleanup: %.2f%% used (target %.1f%%)", usagePercent, targetUsage)
		return plan, nil
	}
	targetUsedBytes := uint64(float64(totalBytes) * targetUsage / 100)
	if usedBytes > targetUsedBytes {
		plan.NeedBytes = usedBytes - targetUsedBytes
	}
	if !reliable {
		plan.Message = "Cleanup planning paused: valuation or File topology is incomplete or stale"
		return plan, nil
	}
	if plan.NeedBytes == 0 {
		plan.Message = fmt.Sprintf("No cleanup: %.2f%% used (target %.1f%%)", usagePercent, targetUsage)
		return plan, nil
	}
	for _, mediaItem := range items {
		if mediaItem.Protected || !mediaItem.ReclaimableKnown || mediaItem.ReclaimableBytes <= 0 {
			continue
		}
		plan.Selected = append(plan.Selected, mediaItem)
		plan.SelectedBytes += uint64(mediaItem.ReclaimableBytes)
		if plan.SelectedBytes >= plan.NeedBytes {
			break
		}
	}
	plan.Message = fmt.Sprintf("Need to reclaim %s to reach %.1f%% usage", Human(plan.NeedBytes), targetUsage)
	return plan, nil
}

func Human(bytes uint64) string {
	const unitSize = 1024
	value := float64(bytes)
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	unitIndex := 0
	for value >= unitSize && unitIndex < len(units)-1 {
		value /= unitSize
		unitIndex++
	}
	return fmt.Sprintf("%.1f %s", value, units[unitIndex])
}
