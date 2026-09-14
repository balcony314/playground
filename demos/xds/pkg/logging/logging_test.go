package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func newTestLogger(buf *bytes.Buffer) Logger {
	return newSlogLogger(buf, "debug", "json")
}

func TestWithFieldsInfow(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.With("service", "svc-a", "broadcast", true).Infof("handle %s", "change")

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not json: %v, got: %s", err, buf.String())
	}
	if out["msg"] != "handle change" {
		t.Errorf("msg = %v, want 'handle change'", out["msg"])
	}
	if out["service"] != "svc-a" || out["broadcast"] != true {
		t.Errorf("fields not recorded: %v", out)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := newSlogLogger(&buf, "warn", "json")
	l.Infof("should be filtered")
	if buf.Len() != 0 {
		t.Errorf("info should be filtered at warn level, got: %s", buf.String())
	}
	l.Warnf("kept")
	if !strings.Contains(buf.String(), "kept") {
		t.Errorf("warn should be kept, got: %s", buf.String())
	}
}

func TestTextFormat(t *testing.T) {
	var buf bytes.Buffer
	l := newSlogLogger(&buf, "info", "console")
	l.Infof("hello")
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("text output missing message: %s", buf.String())
	}
}

func TestGlobalFacade(t *testing.T) {
	var buf bytes.Buffer
	SetLogger(newSlogLogger(&buf, "debug", "json"))
	With("k", "v").Errorf("boom")
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("global facade broken: %s", buf.String())
	}
	if GetLogger() == nil {
		t.Error("GetLogger must not be nil")
	}
}
