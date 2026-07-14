package ingest

import "github.com/jkarage/logingestor/business/domain/logbus"

// KeepRecord reports whether a record at the given level should be kept under a
// sampling policy that always retains WARN/ERROR and keeps DEBUG/INFO at
// keepRate (0..1). r is a caller-supplied uniform random value in [0,1) — kept
// as a parameter so the decision is deterministically testable.
func KeepRecord(level logbus.Level, keepRate float64, r float64) bool {
	if level.Equal(logbus.LevelWarn) || level.Equal(logbus.LevelError) {
		return true
	}
	if keepRate >= 1 {
		return true
	}
	if keepRate <= 0 {
		return false
	}
	return r < keepRate
}
