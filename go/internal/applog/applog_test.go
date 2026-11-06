package applog

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewLogsInfoOutsideTests(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, "production").Info("inserted rates", "provider", "ECB", "count", 3)
	if !strings.Contains(buf.String(), "inserted rates") || !strings.Contains(buf.String(), "provider=ECB") {
		t.Errorf("got %q", buf.String())
	}
}

func TestNewLogsOnlyErrorsUnderTest(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "test")
	log.Info("quiet")
	log.Warn("quiet")
	log.Error("loud")
	if strings.Contains(buf.String(), "quiet") || !strings.Contains(buf.String(), "loud") {
		t.Errorf("got %q", buf.String())
	}
}
