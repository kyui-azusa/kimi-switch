package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultListenAddrDoesNotConflictWithCCSwitch(t *testing.T) {
	if DefaultListenAddr != "127.0.0.1:15722" {
		t.Fatalf("default listen address = %q", DefaultListenAddr)
	}
	if DefaultListenAddr == LegacyDefaultListenAddr {
		t.Fatal("KimiSwitch default must not reuse the CC Switch default port")
	}
}

func TestEnsureMigratesLegacyDefaultListenAddr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := Default()
	c.ListenAddr = LegacyDefaultListenAddr
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	migrated, err := Ensure(path)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.ListenAddr != DefaultListenAddr {
		t.Fatalf("migrated listen address = %q", migrated.ListenAddr)
	}

	persistedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		ListenAddr string `json:"listen_addr"`
	}
	if err := json.Unmarshal(persistedBytes, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.ListenAddr != DefaultListenAddr {
		t.Fatalf("persisted listen address = %q", persisted.ListenAddr)
	}
}

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
