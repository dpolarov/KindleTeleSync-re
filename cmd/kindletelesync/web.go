package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dpolarov/KindleTeleSync-re/internal/config"
	"github.com/skip2/go-qrcode"
)

//go:embed templates/*
var tmplFS embed.FS

const listenAddr = ":8880"

type pageData struct {
	Config            *config.Config
	AllowedExtensions string
	Token             string
	MaxFileMB         int64
	MaxTotalMB        int64
}

func tokenPath() string { return filepath.Join(config.AppDir(), ".web-token") }
func webPIDPath() string { return filepath.Join(config.AppDir(), ".web.pid") }

func webToken() (string, error) {
	if b, err := os.ReadFile(tokenPath()); err == nil {
		if token := strings.TrimSpace(string(b)); len(token) >= 32 {
			return token, nil
		}
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	if err := os.WriteFile(tokenPath(), []byte(token+"\n"), 0600); err != nil {
		return "", err
	}
	return token, nil
}

func webURL(token string) string {
	return fmt.Sprintf("http://%s:8880/?token=%s", localIP(), token)
}

func authorized(r *http.Request, token string) bool {
	return r.URL.Query().Get("token") == token || r.FormValue("token") == token
}

func runWebServer(kindleUI bool) Result {
	token, err := webToken()
	if err != nil {
		return failure("web", "Failed to initialize web access token", err)
	}
	cfg, err := config.LoadOrCreate(config.ConfigPath())
	if err != nil {
		return failure("web", "Failed to load configuration", err)
	}
	url := webURL(token)

	if err := os.WriteFile(webPIDPath(), []byte(strconv.Itoa(os.Getpid())+"\n"), 0600); err != nil {
		return failure("web", "Failed to write web server PID", err)
	}
	defer os.Remove(webPIDPath())

	ruleAdded := addIptablesRule()
	if ruleAdded {
		defer removeIptablesRule()
	}
	if kindleUI {
		if err := drawKindleWebUI(url); err != nil {
			log.Printf("Kindle display warning: %v", err)
		}
	}

	server, err := newWebServer(token, cfg)
	if err != nil {
		return failure("web", "Failed to initialize web settings", err)
	}

	errCh := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if err == http.ErrServerClosed {
			err = nil
		}
		errCh <- err
	}()
	log.Printf("Web settings: %s", url)

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	timer := time.NewTimer(timeoutDuration(cfg.WebTimeout, config.DefaultWebTimeoutSeconds))
	defer timer.Stop()

	var reason string
	select {
	case err := <-errCh:
		if err != nil {
			return failure("web", "Web settings server failed", err)
		}
		reason = "server stopped"
	case <-sigCtx.Done():
		reason = "server stopped"
	case <-timer.C:
		reason = "settings timeout reached"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	if kindleUI {
		clearKindleScreen()
	}
	return Result{OK: true, Action: "web", Message: "Web settings stopped: " + reason + ".", Details: map[string]any{"url": url}}
}

func newWebServer(token string, cfg *config.Config) (*http.Server, error) {
	templates, err := template.ParseFS(tmplFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		if !authorized(r, token) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		copyCfg := *cfg
		copyCfg.Proxy = cfg.Proxy
		copyCfg.AllowedExtensions = append([]string(nil), cfg.AllowedExtensions...)
		mu.Unlock()
		data := pageData{
			Config:            &copyCfg,
			AllowedExtensions: strings.Join(copyCfg.AllowedExtensions, ", "),
			Token:             token,
			MaxFileMB:         copyCfg.MaxFileBytes / (1024 * 1024),
			MaxTotalMB:        copyCfg.MaxTotalBytes / (1024 * 1024),
		}
		if err := templates.ExecuteTemplate(w, "index.html", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
		if !authorized(r, token) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		mu.Lock()
		defer mu.Unlock()
		chatID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("chat_id")), 10, 64)
		if err != nil || chatID == 0 {
			http.Error(w, "Chat ID must be a non-zero integer", http.StatusBadRequest)
			return
		}
		botToken := strings.TrimSpace(r.FormValue("bot_token"))
		if botToken == "" {
			botToken = cfg.BotToken
		}
		if botToken == "" || botToken == "YOUR_TOKEN" {
			http.Error(w, "Bot token is required", http.StatusBadRequest)
			return
		}
		if cfg.ChatID != chatID || cfg.BotToken != botToken {
			cfg.UpdatesState = config.TelegramUpdatesState{}
		}
		cfg.BotToken = botToken
		cfg.ChatID = chatID
		cfg.AllowedExtensions = strings.Split(r.FormValue("allowed_extensions"), ",")
		cfg.DownloadPath = strings.TrimSpace(r.FormValue("download_path"))
		cfg.MaxFileBytes = parseMB(r.FormValue("max_file_mb"), cfg.MaxFileBytes)
		cfg.MaxFilesPerSync = parsePositiveInt(r.FormValue("max_files_per_sync"), cfg.MaxFilesPerSync)
		cfg.MaxTotalBytes = parseMB(r.FormValue("max_total_mb"), cfg.MaxTotalBytes)
		cfg.SyncTimeout = parsePositiveInt(r.FormValue("sync_timeout_seconds"), cfg.SyncTimeout)
		cfg.WebTimeout = parsePositiveInt(r.FormValue("web_timeout_seconds"), cfg.WebTimeout)
		cfg.SendNotifications = r.FormValue("send_notifications") == "on"
		cfg.Proxy.Enabled = r.FormValue("proxy_enabled") == "on"
		cfg.Proxy.Type = r.FormValue("proxy_type")
		cfg.Proxy.Address = strings.TrimSpace(r.FormValue("proxy_address"))
		cfg.Proxy.Username = strings.TrimSpace(r.FormValue("proxy_username"))
		if v := r.FormValue("proxy_password"); v != "" {
			cfg.Proxy.Password = v
		}
		if v := strings.TrimSpace(r.FormValue("proxy_mtproto_secret")); v != "" {
			cfg.Proxy.MTProtoSecret = v
		}
		cfg.Normalize()
		if err := cfg.ValidateForSync(); err != nil {
			http.Error(w, "Invalid configuration: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := cfg.Save(config.ConfigPath()); err != nil {
			http.Error(w, "Failed to save configuration: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := templates.ExecuteTemplate(w, "save.html", struct{ Token string }{Token: token}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil || !authorized(r, token) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("Stopping KindleTeleSync settings server.\n"))
		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		}()
	})

	return &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}, nil
}

func parsePositiveInt(value string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func parseMB(value string, fallback int64) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n <= 0 || n > 1024*1024 {
		return fallback
	}
	return n * 1024 * 1024
}

func stopWebServer() Result {
	b, err := os.ReadFile(webPIDPath())
	if err != nil {
		if os.IsNotExist(err) {
			return Result{OK: true, Action: "web-stop", Message: "Web settings server is not running."}
		}
		return failure("web-stop", "Failed to read web server PID", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		_ = os.Remove(webPIDPath())
		return failure("web-stop", "Invalid web server PID file", fmt.Errorf("invalid PID"))
	}
	if !processExists(pid) {
		_ = os.Remove(webPIDPath())
		return Result{OK: true, Action: "web-stop", Message: "Removed stale web server PID file."}
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return failure("web-stop", "Failed to stop web settings server", err)
	}
	return Result{OK: true, Action: "web-stop", Message: fmt.Sprintf("Stop signal sent to web settings server (PID %d).", pid)}
}

func addIptablesRule() bool {
	if _, err := exec.LookPath("iptables"); err != nil {
		return false
	}
	if err := exec.Command("iptables", "-I", "INPUT", "-p", "tcp", "--dport", "8880", "-j", "ACCEPT").Run(); err != nil {
		log.Printf("Warning: could not add iptables rule: %v", err)
		return false
	}
	return true
}

func removeIptablesRule() {
	if err := exec.Command("iptables", "-D", "INPUT", "-p", "tcp", "--dport", "8880", "-j", "ACCEPT").Run(); err != nil {
		log.Printf("Warning: could not remove iptables rule: %v", err)
	}
}

func drawKindleWebUI(url string) error {
	qrPath := filepath.Join(os.TempDir(), "kindletelesync-qr.png")
	if err := qrcode.WriteFile(url, qrcode.Medium, 350, qrPath); err != nil {
		return err
	}
	defer os.Remove(qrPath)
	if _, err := exec.LookPath("eips"); err != nil {
		return nil
	}
	_ = exec.Command("eips", "-c").Run()
	_ = exec.Command("eips", "10", "2", "KindleTeleSync settings:").Run()
	_ = exec.Command("eips", "10", "3", url).Run()
	return exec.Command("eips", "-g", qrPath, "-x", "350", "-y", "350").Run()
}

func clearKindleScreen() {
	if _, err := exec.LookPath("eips"); err == nil {
		_ = exec.Command("eips", "-c").Run()
	}
}
