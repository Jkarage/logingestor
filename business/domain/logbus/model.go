// Package logbus provides business access to the log domain.
package logbus

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound     = errors.New("log not found")
	ErrInvalidLevel = errors.New("invalid log level")
)

// Level represents a log severity level.
type Level struct{ value string }

func (l Level) String() string      { return l.value }
func (l Level) Equal(l2 Level) bool { return l.value == l2.value }

var (
	LevelDebug = Level{"DEBUG"}
	LevelInfo  = Level{"INFO"}
	LevelWarn  = Level{"WARN"}
	LevelError = Level{"ERROR"}
)

var levels = map[string]Level{
	"DEBUG": LevelDebug,
	"INFO":  LevelInfo,
	"WARN":  LevelWarn,
	"ERROR": LevelError,
}

// ParseLevel parses the string value into a Level.
func ParseLevel(s string) (Level, error) {
	l, ok := levels[s]
	if !ok {
		return Level{}, fmt.Errorf("%w: %q", ErrInvalidLevel, s)
	}
	return l, nil
}

// Log is a single log entry.
type Log struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Level     Level
	Message   string
	Source    string
	Ts        time.Time
	Tags      []string
	Meta      map[string]any
}

// NewLog contains the data needed to create a log entry.
type NewLog struct {
	ProjectID uuid.UUID
	Level     Level
	Message   string
	Source    string
	Ts        time.Time
	Tags      []string
	Meta      map[string]any
}

// QueryFilter holds filters for a log query.
type QueryFilter struct {
	ProjectID uuid.UUID
	Level     *Level
	Search    *string
	From      *time.Time
	To        *time.Time
}

// QueryResult holds a page of log results.
type QueryResult struct {
	Logs       []Log
	NextCursor *string
	Total      int
}
