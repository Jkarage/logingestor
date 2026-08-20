package annotationbus

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func Test_validateBody(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		err  error
	}{
		{name: "text is kept", body: "deployed 4.12", want: "deployed 4.12"},
		{name: "surrounding whitespace is trimmed", body: "  noted  ", want: "noted"},
		{name: "empty is rejected", body: "", err: ErrBodyRequired},
		{name: "whitespace only is rejected", body: "   \n\t ", err: ErrBodyRequired},
		{name: "at the limit is accepted", body: strings.Repeat("x", MaxBodyLen), want: strings.Repeat("x", MaxBodyLen)},
		{name: "over the limit is rejected", body: strings.Repeat("x", MaxBodyLen+1), err: ErrBodyTooLong},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := validateBody(c.body)

			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Fatalf("err = %v, want %v", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("body = %q, want %q", got, c.want)
			}
		})
	}
}

// An org admin may tidy up a note they did not write; nobody else may.
func Test_CanModify(t *testing.T) {
	author := uuid.New()
	other := uuid.New()
	note := Annotation{CreatedBy: author}

	if !CanModify(note, author, false) {
		t.Errorf("the author cannot modify their own note")
	}
	if !CanModify(note, other, true) {
		t.Errorf("an org admin cannot modify a member's note")
	}
	if CanModify(note, other, false) {
		t.Errorf("an ordinary member can modify someone else's note")
	}
}
