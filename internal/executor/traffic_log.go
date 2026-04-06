package executor

import (
	"log"
	"strings"
	"time"
)

const trafficLogMaxPayload = 16384

func truncateTrafficPayload(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= trafficLogMaxPayload {
		return s
	}
	return s[:trafficLogMaxPayload] + "...(truncated)"
}

func logExecutorTraffic(enabled bool, source, direction, msg string) {
	if !enabled {
		return
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	log.Printf("[traffic] time=%s source=%s direction=%s msg=%s", ts, source, direction, truncateTrafficPayload(msg))
}

func (r *runner) logTraffic(source, direction, msg string) {
	if r == nil {
		return
	}
	logExecutorTraffic(r.trafficLogs, source, direction, msg)
}
