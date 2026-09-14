package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConf(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInitAndGetString(t *testing.T) {
	path := writeConf(t, "web:\n  address: \"127.0.0.1:9999\"\nnacos:\n  port: 8848\n  syncInterval: \"45s\"\n")
	if err := Init(path); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if got := GetString("web.address"); got != "127.0.0.1:9999" {
		t.Errorf("web.address = %q", got)
	}
	if got := GetInt("nacos.port"); got != 8848 {
		t.Errorf("nacos.port = %d", got)
	}
	if got := GetDuration("nacos.syncInterval").String(); got != "45s" {
		t.Errorf("nacos.syncInterval = %s", got)
	}
}

func TestEnvOverride(t *testing.T) {
	path := writeConf(t, "nacos:\n  port: 8848\n")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("xds_nacos_port", "9999")
	if got := GetInt("nacos.port"); got != 9999 {
		t.Errorf("env override failed, nacos.port = %d", got)
	}
}
