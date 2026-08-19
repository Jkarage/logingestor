package auditexport

import (
	"testing"
	"time"
)

func Test_Config_Enabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Error("an unset URL must disable export, not post to nowhere")
	}
	if !(Config{URL: "https://siem.example.com/ingest"}).Enabled() {
		t.Error("a set URL must enable export")
	}
}

func Test_Config_withDefaults(t *testing.T) {
	// A zero batch size would issue LIMIT 0 and never make progress.
	c := (Config{}).withDefaults()
	if c.BatchSize <= 0 {
		t.Errorf("BatchSize = %d, want a positive default", c.BatchSize)
	}
	if c.Timeout <= 0 {
		t.Errorf("Timeout = %v, want a positive default", c.Timeout)
	}

	c2 := (Config{BatchSize: 25, Timeout: time.Second}).withDefaults()
	if c2.BatchSize != 25 || c2.Timeout != time.Second {
		t.Errorf("caller values overridden: %+v", c2)
	}
}
