package main

import (
	"errors"
	"time"

	"squidadmin/backend/internal/monitor"
	"squidadmin/backend/internal/squid"
)

// liveProbe answers the alert engine's questions from the real system.
type liveProbe struct {
	mgr    *squid.Manager
	ingest *monitor.Ingestor
	disks  []string
}

func (p *liveProbe) SquidRunning() bool { return p.mgr.GetStatus().Running }

func (p *liveProbe) Recent(w time.Duration) (int, int, int) { return p.ingest.Recent(w) }

// Disk reports the fullest of the watched filesystems.
func (p *liveProbe) Disk() (string, float64, error) {
	var (
		worst string
		pct   float64
		found bool
	)
	for _, path := range p.disks {
		u, err := monitor.DiskUsage(path)
		if err != nil {
			continue
		}
		if !found || u.UsedPercent > pct {
			worst, pct, found = path, u.UsedPercent, true
		}
	}
	if !found {
		return "", 0, errors.New("no watched filesystem could be read")
	}
	return worst, pct, nil
}
