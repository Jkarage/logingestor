package logapp

import (
	"testing"

	"github.com/jkarage/logingestor/business/domain/logbus"
)

func Test_parseSourceType(t *testing.T) {
	// Empty -> nil (all source types).
	if got, err := parseSourceType(""); err != nil || got != nil {
		t.Errorf(`parseSourceType("") = %v, %v; want nil, nil`, got, err)
	}

	for _, valid := range []string{logbus.SourceTypeApp, logbus.SourceTypeInfra} {
		got, err := parseSourceType(valid)
		if err != nil {
			t.Errorf("parseSourceType(%q) unexpected err: %v", valid, err)
			continue
		}
		if got == nil || *got != valid {
			t.Errorf("parseSourceType(%q) = %v; want %q", valid, got, valid)
		}
	}

	if _, err := parseSourceType("bogus"); err == nil {
		t.Error("parseSourceType(\"bogus\") should error")
	}
}
