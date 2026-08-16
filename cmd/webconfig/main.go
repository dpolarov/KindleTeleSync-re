package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
}

func init() {
	log.SetFlags(log.Ldate | log.Ltime)
}

func tokenPath() string { return filepath.Join(config.AppDir(), ".web-token") }

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

func localIP() string {
	conn, err := net.DialTimeout("udp", "1.1.1.1:53", 2*time.Second)
	if err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
			return addr.IP.String()
		}
	}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.To4() != nil && !ip.IsLoopback() {
				return ip.String()
			}
		}
	}
	return "127.0.0.1"
}

func webURL(token string) string {
	return fmt.Sprintf("http://%s:8880/?token=%s", localIP(), token)
}

func authorized(r *http.Request, token string) bool {
	return r.URL.Query().Get("token") == token
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

func loadConfig() *config.Config {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return config.DefaultConfig()
	}
	return cfg
}

func runServer(token string) error {
	cfg := loadConfig()
	templates, err := template.ParseFS(tmplFS, "templates/*.html")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		data := pageData{Config: cfg, AllowedExtensions: strings.Join(cfg.AllowedExtensions, ", "), Token: token}
		if err := templates.ExecuteTemplate(w, "index.html", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
		chatID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("chat_id")), 10, 64)
		if err != nil || chatID == 0 {
			http.Error(w, "Chat ID must be a non-zero integer", http.StatusBadRequest)
			return
		}
		botToken := strings.TrimSpace(r.FormValue("bot_token"))
		if botToken == "" {
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
		cfg.Proxy.Enabled = r.FormValue("proxy_enabled") == "on"
		cfg.Proxy.Type = r.FormValue("proxy_type")
		cfg.Proxy.Address = strings.TrimSpace(r.FormValue("proxy_address"))
		cfg.Proxy.Username = r.FormValue("proxy_username")
		cfg.Proxy.Password = r.FormValue("proxy_password")
		cfg.Proxy.MTProtoSecret = strings.TrimSpace(r.FormValue("proxy_mtproto_secret"))
		cfg.Normalize()
		if err := cfg.Save(config.ConfigPath()); err != nil {
			http.Error(w, "Failed to save configuration: "+err.Error(), http.StatusInternalServerError)
			return
		}
		_ = templates.ExecuteTemplate(w, "save.html", nil)
	})

	server := &http.Server{Addr: listenAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("Web settings: %s", webURL(token))
	return server.ListenAndServe()
}

func kindleUI(token string) error {
	url := webURL(token)
	qrPath := filepath.Join(os.TempDir(), "kindletelesync-qr.png")
	if err := qrcode.WriteFile(url, qrcode.Medium, 350, qrPath); err != nil {
		return err
	}
	defer os.Remove(qrPath)

	if _, err := exec.LookPath("eips"); err == nil {
		_ = exec.Command("eips", "-c").Run()
		_ = exec.Command("eips", "10", "2", "KindleTeleSync settings:").Run()
		_ = exec.Command("eips", "10", "3", url).Run()
		_ = exec.Command("eips", "-g", qrPath, "-x", "350", "-y", "350").Run()
	}

	done := make(chan error, 1)
	go func() { done <- runServer(token) }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Minute):
		return nil
	}
}

func main() {
	token, err := webToken()
	if err != nil {
		log.Fatalf("Failed to initialize web access token: %v", err)
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "url", "--print-url":
			fmt.Println(webURL(token))
			return
		case "kindle-ui", "--kindle-ui":
			if err := kindleUI(token); err != nil && err != http.ErrServerClosed {
				log.Fatal(err)
			}
			return
		}
	}

	ruleAdded := addIptablesRule()
	if ruleAdded {
		defer removeIptablesRule()
	}
	if err := runServer(token); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
