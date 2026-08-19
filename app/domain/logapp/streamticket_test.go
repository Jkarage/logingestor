package logapp

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func Test_ticketStore_RoundTrip(t *testing.T) {
	s := newTicketStore()
	user, proj := uuid.New(), uuid.New()
	now := time.Now()

	ticket, ttl, err := s.issue(user, proj, now)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if ttl != streamTicketTTL {
		t.Errorf("ttl = %v, want %v", ttl, streamTicketTTL)
	}
	if len(ticket) < 32 {
		t.Errorf("ticket too short to be unguessable: %d chars", len(ticket))
	}

	got, err := s.consume(ticket, proj, now)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got != user {
		t.Errorf("userID = %s, want %s", got, user)
	}
}

func Test_ticketStore_IsSingleUse(t *testing.T) {
	s := newTicketStore()
	user, proj := uuid.New(), uuid.New()
	now := time.Now()

	ticket, _, _ := s.issue(user, proj, now)

	if _, err := s.consume(ticket, proj, now); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, err := s.consume(ticket, proj, now); !errors.Is(err, errTicketInvalid) {
		t.Errorf("replay err = %v, want errTicketInvalid", err)
	}
}

func Test_ticketStore_Rejections(t *testing.T) {
	user, proj, other := uuid.New(), uuid.New(), uuid.New()
	now := time.Now()

	t.Run("expired", func(t *testing.T) {
		s := newTicketStore()
		ticket, _, _ := s.issue(user, proj, now)
		later := now.Add(streamTicketTTL + time.Second)
		if _, err := s.consume(ticket, proj, later); !errors.Is(err, errTicketInvalid) {
			t.Errorf("err = %v, want errTicketInvalid", err)
		}
	})

	// A ticket for project A must not open a stream on project B — that is the
	// difference between 403 and 401 at the handshake.
	t.Run("wrong project", func(t *testing.T) {
		s := newTicketStore()
		ticket, _, _ := s.issue(user, proj, now)
		if _, err := s.consume(ticket, other, now); !errors.Is(err, errTicketProject) {
			t.Errorf("err = %v, want errTicketProject", err)
		}
	})

	t.Run("unknown and empty", func(t *testing.T) {
		s := newTicketStore()
		for _, raw := range []string{"", "not-a-real-ticket"} {
			if _, err := s.consume(raw, proj, now); !errors.Is(err, errTicketInvalid) {
				t.Errorf("consume(%q) err = %v, want errTicketInvalid", raw, err)
			}
		}
	})

	// A rejected ticket must still be burned, so a wrong-project attempt can't
	// be retried against the right project.
	t.Run("rejected ticket is still burned", func(t *testing.T) {
		s := newTicketStore()
		ticket, _, _ := s.issue(user, proj, now)
		if _, err := s.consume(ticket, other, now); !errors.Is(err, errTicketProject) {
			t.Fatalf("setup: %v", err)
		}
		if _, err := s.consume(ticket, proj, now); !errors.Is(err, errTicketInvalid) {
			t.Errorf("err = %v, want errTicketInvalid", err)
		}
	})
}

func Test_ticketStore_PrunesExpired(t *testing.T) {
	s := newTicketStore()
	now := time.Now()

	for range 5 {
		s.issue(uuid.New(), uuid.New(), now)
	}
	if len(s.m) != 5 {
		t.Fatalf("len = %d, want 5", len(s.m))
	}

	// Issuing after the TTL has passed sweeps the stale entries, leaving only
	// the new one.
	s.issue(uuid.New(), uuid.New(), now.Add(streamTicketTTL+time.Second))
	if len(s.m) != 1 {
		t.Errorf("len = %d after sweep, want 1", len(s.m))
	}
}
