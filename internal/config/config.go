package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultKindleRoot = "/mnt/us"

const (
	DefaultMaxFileBytes       int64 = 100 * 1024 * 1024
	DefaultMaxTotalBytes      int64 = 250 * 1024 * 1024
	DefaultMaxFilesPerSync          = 20
	DefaultSyncTimeoutSeconds       = 600
	DefaultWebTimeoutSeconds        = 180
)

type ProxyConfig struct {
	Enabled       bool   `json:"enabled"`
	Type          string `json:"type"` // socks5, http, mtproto
	Address       string `json:"address"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	MTProtoSecret string `json:"mtproto_secret"`
}

type TelegramUpdatesState struct {
	Pts  int `json:"pts"`
	Date int `json:"date"`
	Qts  int `json:"qts"`
}

type Config struct {
	BotToken          string               `json:"bot_token"`
	ChatID            int64                `json:"chat_id"`
	AllowedExtensions []string             `json:"allowed_extensions"`
	RootPath          string               `json:"root_path"`
	DownloadPath      string               `json:"download_path"`
	Proxy             ProxyConfig          `json:"proxy"`
	UpdatesState      TelegramUpdatesState `json:"updates_state"`
	MaxFileBytes      int64                `json:"max_file_bytes"`
	MaxFilesPerSync   int                  `json:"max_files_per_sync"`
	MaxTotalBytes     int64                `json:"max_total_bytes"`
	SyncTimeout       int                  `json:"sync_timeout_seconds"`
	WebTimeout        int                  `json:"web_timeout_seconds"`
	SendNotifications bool                 `json:"send_notifications"`
}

func KindleRoot() string {
	if p := strings.TrimSpace(os.Getenv("KINDLE_ROOT")); p != "" {
		return filepath.Clean(p)
	}
	return defaultKindleRoot
}

func AppDir() string {
	return filepath.Join(KindleRoot(), "extensions", "KindleTeleSync")
}

func ConfigPath() string {
	return filepath.Join(AppDir(), "config.json")
}

func DefaultConfig() *Config {
	root := KindleRoot()
	return &Config{
		AllowedExtensions: []string{".epub", ".mobi", ".pdf", ".zip", ".fb2"},
		RootPath:          root,
		DownloadPath:      filepath.Join(root, "books"),
		Proxy: ProxyConfig{
			Type: "socks5",
		},
		MaxFileBytes:      DefaultMaxFileBytes,
		MaxFilesPerSync:   DefaultMaxFilesPerSync,
		MaxTotalBytes:     DefaultMaxTotalBytes,
		SyncTimeout:       DefaultSyncTimeoutSeconds,
		WebTimeout:        DefaultWebTimeoutSeconds,
		SendNotifications: true,
	}
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0600)

	c := DefaultConfig()
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	c.Normalize()
	return c, nil
}

func LoadOrCreate(path string) (*Config, error) {
	c, err := Load(path)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	c = DefaultConfig()
	if err := c.Save(path); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) Normalize() {
	if c.RootPath == "" {
		c.RootPath = KindleRoot()
	}
	if c.DownloadPath == "" {
		c.DownloadPath = filepath.Join(c.RootPath, "books")
	}
	if len(c.AllowedExtensions) == 0 {
		c.AllowedExtensions = append([]string(nil), DefaultConfig().AllowedExtensions...)
	}

	seen := make(map[string]struct{}, len(c.AllowedExtensions))
	normalized := make([]string, 0, len(c.AllowedExtensions))
	for _, ext := range c.AllowedExtensions {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		if _, ok := seen[ext]; ok {
			continue
		}
		seen[ext] = struct{}{}
		normalized = append(normalized, ext)
	}
	c.AllowedExtensions = normalized

	c.Proxy.Type = strings.ToLower(strings.TrimSpace(c.Proxy.Type))
	if c.Proxy.Type == "" {
		c.Proxy.Type = "socks5"
	}
	if c.MaxFileBytes <= 0 {
		c.MaxFileBytes = DefaultMaxFileBytes
	}
	if c.MaxFilesPerSync <= 0 {
		c.MaxFilesPerSync = DefaultMaxFilesPerSync
	}
	if c.MaxTotalBytes <= 0 {
		c.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if c.SyncTimeout <= 0 {
		c.SyncTimeout = DefaultSyncTimeoutSeconds
	}
	if c.WebTimeout <= 0 {
		c.WebTimeout = DefaultWebTimeoutSeconds
	}
}

func (c *Config) ValidateForSync() error {
	c.Normalize()
	if strings.TrimSpace(c.BotToken) == "" || c.BotToken == "YOUR_TOKEN" {
		return errors.New("bot token is empty")
	}
	if c.ChatID == 0 {
		return errors.New("chat ID is empty")
	}
	if len(c.AllowedExtensions) == 0 {
		return errors.New("allowed extensions list is empty")
	}
	if strings.TrimSpace(c.DownloadPath) == "" {
		return errors.New("download path is empty")
	}
	if c.Proxy.Enabled {
		if strings.TrimSpace(c.Proxy.Address) == "" {
			return errors.New("proxy address is empty")
		}
		switch c.Proxy.Type {
		case "socks5", "http":
		case "mtproto":
			if strings.TrimSpace(c.Proxy.MTProtoSecret) == "" {
				return errors.New("MTProto proxy secret is empty")
			}
		default:
			return fmt.Errorf("unsupported proxy type %q", c.Proxy.Type)
		}
	}
	return nil
}

func (c *Config) Save(path string) error {
	c.Normalize()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, b, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Chmod(path, 0600)
}
