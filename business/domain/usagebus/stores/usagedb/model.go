package usagedb

import "time"

// sourceCountersDB is one source's totals as the counters query returns them.
type sourceCountersDB struct {
	SourceID string `db:"source_id"`
	Events   int64  `db:"events"`
	Errors   int64  `db:"errors"`
	Dropped  int64  `db:"dropped"`
}

// hourCountersDB is one hour of a source's counters.
type hourCountersDB struct {
	Hour    time.Time `db:"hour"`
	Events  int64     `db:"events"`
	Errors  int64     `db:"errors"`
	Dropped int64     `db:"dropped"`
}
