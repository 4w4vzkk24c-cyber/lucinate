package radar

import "time"

// ScanTasks concurrently scans a kanban tasks directory for active cards.
func ScanTasks(dir string, budget time.Duration) ([]ActiveCard, error) {
	return nil, nil
}

// ParseRoadmap parses the ranked roadmap file into queued rows.
func ParseRoadmap(path string, maxLines int) ([]QueuedItem, error) {
	return nil, nil
}

// Build aggregates scan, roadmap, and git telemetry into a snapshot.
func Build(home string) (RadarSnapshot, error) {
	return RadarSnapshot{Timestamp: time.Now()}, nil
}
