package radar

import "time"

// CollectGit gathers working-tree and recent-commit telemetry from dir.
func CollectGit(dir string, now time.Time) (GitTelemetry, error) {
	return GitTelemetry{}, nil
}
