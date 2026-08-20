package viewbus

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func Test_validatePermalink(t *testing.T) {
	projectID := uuid.New()
	logID := uuid.New()

	cases := []struct {
		name string
		np   NewPermalink
		want error
	}{
		{
			name: "a log permalink needs both a project and a log",
			np:   NewPermalink{Kind: PermalinkLog, ProjectID: &projectID, LogID: &logID},
		},
		{
			// Without the project there is nothing to authorize the read against,
			// and no endpoint to fetch the log from.
			name: "a log permalink without a project is rejected",
			np:   NewPermalink{Kind: PermalinkLog, LogID: &logID},
			want: ErrPermalinkLogID,
		},
		{
			name: "a log permalink without a log is rejected",
			np:   NewPermalink{Kind: PermalinkLog, ProjectID: &projectID},
			want: ErrPermalinkLogID,
		},
		{
			name: "a query permalink needs neither",
			np:   NewPermalink{Kind: PermalinkQuery},
		},
		{
			// Carrying both would leave the resolver guessing which one the link
			// meant, and the two can disagree.
			name: "a query permalink carrying a log is rejected",
			np:   NewPermalink{Kind: PermalinkQuery, LogID: &logID},
			want: ErrPermalinkQueryID,
		},
		{
			name: "an unknown kind is rejected",
			np:   NewPermalink{Kind: "trace"},
			want: ErrPermalinkKind,
		},
		{
			name: "an empty kind is rejected rather than defaulted",
			np:   NewPermalink{},
			want: ErrPermalinkKind,
		},
		{
			name: "a query larger than the definition limit is rejected",
			np:   NewPermalink{Kind: PermalinkQuery, Query: json.RawMessage(`{"q":"` + strings.Repeat("x", MaxDefinitionBytes) + `"}`)},
			want: ErrDefTooLarge,
		},
		{
			name: "a query that is not JSON is rejected",
			np:   NewPermalink{Kind: PermalinkQuery, Query: json.RawMessage(`{oops`)},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := validatePermalink(c.np)

			switch {
			case c.want != nil:
				if !errors.Is(err, c.want) {
					t.Fatalf("err = %v, want %v", err, c.want)
				}
			case c.name == "a query that is not JSON is rejected":
				if err == nil {
					t.Fatalf("invalid JSON was accepted")
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				// An absent query is stored as an empty object, so a resolved
				// permalink always hands the client valid JSON.
				if string(got.Query) != "{}" && len(c.np.Query) == 0 {
					t.Errorf("query = %q, want {}", got.Query)
				}
			}
		})
	}
}

// The slug travels in URLs and is the only thing standing between a link and the
// view it names, so it has to be URL-safe and unguessable.
func Test_GenerateSlug(t *testing.T) {
	seen := make(map[string]struct{}, 1000)

	for i := 0; i < 1000; i++ {
		slug, err := GenerateSlug()
		if err != nil {
			t.Fatalf("GenerateSlug: %v", err)
		}

		if _, dup := seen[slug]; dup {
			t.Fatalf("GenerateSlug returned %q twice in 1000 draws", slug)
		}
		seen[slug] = struct{}{}

		if strings.ContainsAny(slug, "+/=") {
			t.Errorf("slug %q contains characters that need URL escaping", slug)
		}
		if len(slug) < 16 {
			t.Errorf("slug %q is %d characters, too short to be unguessable", slug, len(slug))
		}
	}
}
