package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dpolarov/KindleTeleSync-re/internal/config"
)

func TestWebSettingsRequireToken(t *testing.T) {
	cfg := config.DefaultConfig()
	server, err := newWebServer("secret-token", cfg)
	if err != nil {
		t.Fatalf("newWebServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://kindle/", nil)
	rr := httptest.NewRecorder()
	server.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestWebGetDoesNotMutateExtensions(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AllowedExtensions = []string{".epub", ".pdf"}
	server, err := newWebServer("secret-token", cfg)
	if err != nil {
		t.Fatalf("newWebServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://kindle/?token=secret-token", nil)
	rr := httptest.NewRecorder()
	server.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if len(cfg.AllowedExtensions) != 2 || cfg.AllowedExtensions[0] != ".epub" || cfg.AllowedExtensions[1] != ".pdf" {
		t.Fatalf("GET mutated AllowedExtensions: %#v", cfg.AllowedExtensions)
	}
}

func TestWebSaveKeepsBlankSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KINDLE_ROOT", root)
	cfg := config.DefaultConfig()
	cfg.BotToken = "existing-bot-token"
	cfg.ChatID = 12345
	cfg.Proxy.Password = "existing-proxy-password"
	cfg.Proxy.MTProtoSecret = "existing-mtproto-secret"

	server, err := newWebServer("secret-token", cfg)
	if err != nil {
		t.Fatalf("newWebServer: %v", err)
	}

	form := url.Values{
		"token":                {"secret-token"},
		"bot_token":            {""},
		"chat_id":              {"12345"},
		"allowed_extensions":   {".epub, .pdf"},
		"download_path":        {root + "/books"},
		"proxy_password":       {""},
		"proxy_mtproto_secret": {""},
	}
	req := httptest.NewRequest(http.MethodPost, "http://kindle/save?token=secret-token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	server.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if cfg.BotToken != "existing-bot-token" {
		t.Fatalf("bot token changed to %q", cfg.BotToken)
	}
	if cfg.Proxy.Password != "existing-proxy-password" {
		t.Fatalf("proxy password changed to %q", cfg.Proxy.Password)
	}
	if cfg.Proxy.MTProtoSecret != "existing-mtproto-secret" {
		t.Fatalf("MTProto secret changed to %q", cfg.Proxy.MTProtoSecret)
	}
}
