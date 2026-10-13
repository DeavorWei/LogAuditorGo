package tray

import (
	"testing"
)

func TestTrayGetURL(t *testing.T) {
	cfg := Config{
		Port: 8080,
		Host: "localhost",
	}
	tr := New(cfg)
	if url := tr.GetURL("/"); url != "http://localhost:8080" {
		t.Errorf("Expected http://localhost:8080, got %s", url)
	}
	if url := tr.GetURL("/audit"); url != "http://localhost:8080/audit" {
		t.Errorf("Expected http://localhost:8080/audit, got %s", url)
	}
	if url := tr.GetURL("knowledge"); url != "http://localhost:8080/knowledge" {
		t.Errorf("Expected http://localhost:8080/knowledge, got %s", url)
	}
}
