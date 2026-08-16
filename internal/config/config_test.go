package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOldConfigAddsDefaults(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KINDLE_ROOT", root)
	path := filepath.Join(root, "extensions", "KindleTeleSync", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	old := `{"bot_token":"token","chat_id":123,"allowed_extensions":["epub",".PDF"],"download_path":"` + filepath.Join(root, "books") + `"}`
	if err := os.WriteFile(path, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MaxFileBytes != DefaultMaxFileBytes || cfg.MaxTotalBytes != DefaultMaxTotalBytes || cfg.MaxFilesPerSync != DefaultMaxFilesPerSync {
		t.Fatalf("safety defaults were not migrated: %+v", cfg)
	}
	if cfg.SyncTimeout != DefaultSyncTimeoutSeconds || cfg.WebTimeout != DefaultWebTimeoutSeconds {
		t.Fatalf("timeout defaults were not migrated: %+v", cfg)
	}
	if !cfg.SendNotifications {
		t.Fatal("SendNotifications default should remain enabled for legacy config")
	}
	if len(cfg.AllowedExtensions) != 2 || cfg.AllowedExtensions[0] != ".epub" || cfg.AllowedExtensions[1] != ".pdf" {
		t.Fatalf("extensions not normalized: %#v", cfg.AllowedExtensions)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSaveAtomicAndPrivate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KINDLE_ROOT", root)
	path := ConfigPath()
	cfg := DefaultConfig()
	cfg.BotToken = "secret"
	cfg.ChatID = 42
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary config remains after save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestValidateProxy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BotToken = "token"
	cfg.ChatID = 1
	cfg.Proxy.Enabled = true
	cfg.Proxy.Type = "mtproto"
	cfg.Proxy.Address = "proxy.example:443"
	if err := cfg.ValidateForSync(); err == nil {
		t.Fatal("MTProto proxy without secret should fail validation")
	}
	cfg.Proxy.MTProtoSecret = "abcdef"
	if err := cfg.ValidateForSync(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateRejectsDownloadOutsideKindleRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KINDLE_ROOT", root)
	cfg := DefaultConfig()
	cfg.BotToken = "token"
	cfg.ChatID = 1
	cfg.DownloadPath = filepath.Join(filepath.Dir(root), "outside")
	if err := cfg.ValidateForSync(); err == nil {
		t.Fatal("download path outside Kindle root should fail validation")
	}
}
