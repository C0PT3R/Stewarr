package cleanup

import (
	"fmt"
	"syscall"
	"togetharr/internal/model"
)

type Plan struct {
	TargetUsagePercent   float64       `json:"targetUsagePercent"`
	CriticalUsagePercent float64       `json:"criticalUsagePercent"`
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

func Build(path string, targetUsage, criticalUsage float64, items []model.Media) (Plan, error) {
	p := Plan{Path: path, TargetUsagePercent: targetUsage, CriticalUsagePercent: criticalUsage}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		p.Message = "Storage unavailable; cleanup planning disabled"
		p.Error = err.Error()
		return p, fmt.Errorf("storage path %q: %w", path, err)
	}
	total := st.Blocks * uint64(st.Bsize)
	if total == 0 {
		err := fmt.Errorf("filesystem reports zero total capacity")
		p.Message = "Storage unavailable; cleanup planning disabled"
		p.Error = err.Error()
		return p, fmt.Errorf("storage path %q: %w", path, err)
	}
	p.Available = true
	p.TotalBytes = total
	free := st.Bavail * uint64(st.Bsize)
	used := total - free
	p.FreeBytes = free
	p.UsedBytes = used
	usagePct := float64(used) / float64(total) * 100
	p.UsagePercent = usagePct
	if usagePct < criticalUsage {
		p.Message = fmt.Sprintf("No cleanup: %.2f%% used (critical %.1f%%, target %.1f%%)", usagePct, criticalUsage, targetUsage)
		return p, nil
	}
	targetUsed := uint64(float64(total) * targetUsage / 100)
	if used > targetUsed {
		p.NeedBytes = used - targetUsed
	}
	for _, m := range items {
		p.Selected = append(p.Selected, m)
		if m.SizeBytes > 0 {
			p.SelectedBytes += uint64(m.SizeBytes)
		}
		if p.SelectedBytes >= p.NeedBytes {
			break
		}
	}
	p.Message = fmt.Sprintf("Need to reclaim %s to reach %.1f%% usage", Human(p.NeedBytes), targetUsage)
	return p, nil
}

func Human(n uint64) string {
	const u = 1024
	v := float64(n)
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for v >= u && i < len(units)-1 {
		v /= u
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
