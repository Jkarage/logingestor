// Package ingest provides the shared normalization pipeline for infrastructure
// log ingestion: severity mapping, format sniffing, timestamp normalization and
// secret redaction. It is protocol-agnostic — each listener maps its wire
// format into a Record and runs it through Normalize.
package ingest

import "github.com/jkarage/logingestor/business/domain/logbus"

// SeverityAttrKey is where the original (pre-mapping) severity is preserved so
// nothing is lost when collapsing to the four canonical levels.
const SeverityAttrKey = "_severity"

// SyslogSeverityToLevel maps an RFC 5424 syslog severity (0–7) to a canonical
// level: 0–3 -> ERROR, 4 -> WARN, 5–6 -> INFO, 7 -> DEBUG. Out-of-range values
// default to INFO.
func SyslogSeverityToLevel(sev int) logbus.Level {
	switch {
	case sev >= 0 && sev <= 3:
		return logbus.LevelError
	case sev == 4:
		return logbus.LevelWarn
	case sev == 5 || sev == 6:
		return logbus.LevelInfo
	case sev == 7:
		return logbus.LevelDebug
	default:
		return logbus.LevelInfo
	}
}

// OTelSeverityToLevel maps an OpenTelemetry SeverityNumber (1–24) to a canonical
// level: 1–8 -> DEBUG, 9–12 -> INFO, 13–16 -> WARN, 17–24 -> ERROR. A zero or
// out-of-range number defaults to INFO.
func OTelSeverityToLevel(num int) logbus.Level {
	switch {
	case num >= 1 && num <= 8:
		return logbus.LevelDebug
	case num >= 9 && num <= 12:
		return logbus.LevelInfo
	case num >= 13 && num <= 16:
		return logbus.LevelWarn
	case num >= 17 && num <= 24:
		return logbus.LevelError
	default:
		return logbus.LevelInfo
	}
}
