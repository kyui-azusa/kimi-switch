package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveWritesConfigWith0600Permissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := Default()
	c.Keys = []KeyConfig{{Name: "work", APIKey: "sk-local", Enabled: true}}
	c.ActiveKey = "work"
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %#o, want 0600", got)
	}
}
