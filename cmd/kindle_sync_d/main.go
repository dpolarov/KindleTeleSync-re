package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/beevik/ntp"
	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/sessionMaker"
	"github.com/dpolarov/KindleTeleSync-re/internal/config"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/tg"
	"golang.org/x/net/proxy"
)

const (
	appID   = 2040
	appHash = "b18441a1ff607e10a989891a5462e627"
)

func init() {
	log.SetFlags(log.Ldate | log.Ltime)
}

func syncClock() {
	servers := []string{"pool.ntp.org", "time.cloudflare.com", "time.google.com"}
	for _, server := range servers {
		t, err := ntp.Time(server)
		if err != nil {
			log.Printf("NTP %s failed: %v", server, err)
			continue
		}
		tv := syscall.NsecToTimeval(t.UnixNano())
		if err := syscall.Settimeofday(&tv); err != nil {
			log.Printf("Cannot set system clock: %v", err)
			return
		}
		log.Printf("Clock synchronized via %s", server)
		return
	}
	log.Printf("All NTP servers failed; continuing with the current system time")
}

func setupResolver(cfg *config.Config) (dcs.Resolver, error) {
	if !cfg.Proxy.Enabled {
		return dcs.DefaultResolver(), nil
	}

	switch cfg.Proxy.Type {
	case "mtproto":
		secret, err := hex.DecodeString(cfg.Proxy.MTProtoSecret)
		if err != nil {
			secret, err = base64.RawURLEncoding.DecodeString(cfg.Proxy.MTProtoSecret)
		}
		if err != nil || len(secret) == 0 {
			return nil, fmt.Errorf("invalid MTProto proxy secret")
		}
		return dcs.MTProxy(cfg.Proxy.Address, secret, dcs.MTProxyOptions{})
	case "socks5":
		var auth *proxy.Auth
		if cfg.Proxy.Username != "" || cfg.Proxy.Password != "" {
			auth = &proxy.Auth{User: cfg.Proxy.Username, Password: cfg.Proxy.Password}
		}
		dialer, err := proxy.SOCKS5("tcp", cfg.Proxy.Address, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("configure SOCKS5 proxy: %w", err)
		}
		return dcs.Plain(dcs.PlainOptions{Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}}), nil
	case "http":
		return dcs.Plain(dcs.PlainOptions{Dial: httpProxyDialer(cfg.Proxy.Address, cfg.Proxy.Username, cfg.Proxy.Password)}), nil
	default:
		return nil, fmt.Errorf("unsupported proxy type %q", cfg.Proxy.Type)
	}
}

func httpProxyDialer(proxyAddr, user, pass string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := net.Dialer{Timeout: 30 * time.Second}
		conn, err := d.DialContext(ctx, network, proxyAddr)
		if err != nil {
			return nil, err
		}
		ok := false
		defer func() {
			if !ok {
				_ = conn.Close()
			}
		}()

		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: make(http.Header)}
		if user != "" || pass != "" {
			auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
			req.Header.Set("Proxy-Authorization", "Basic "+auth)
		}
		if err := req.Write(conn); err != nil {
			return nil, err
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		if err != nil {
			return nil, fmt.Errorf("read HTTP proxy response: %w", err)
		}
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP proxy CONNECT failed: %s", resp.Status)
		}
		ok = true
		return conn, nil
	}
}

func getUniqueFilename(baseDir, filename string) string {
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	out := filepath.Join(baseDir, filename)
	for counter := 1; ; counter++ {
		if _, err := os.Stat(out); os.IsNotExist(err) {
			return out
		}
		out = filepath.Join(baseDir, fmt.Sprintf("%s_%d%s", base, counter, ext))
	}
}

func getFilename(doc *tg.Document) string {
	for _, attr := range doc.Attributes {
		if nameAttr, ok := attr.(*tg.DocumentAttributeFilename); ok {
			return filepath.Base(nameAttr.FileName)
		}
	}
	return ""
}

func getPeerID(peer interface{}) int64 {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return p.UserID
	case *tg.PeerChat:
		return p.ChatID
	case *tg.PeerChannel:
		return p.ChannelID
	default:
		return 0
	}
}

func isAllowedExt(filename string, allowed []string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	for _, allowedExt := range allowed {
		if strings.ToLower(strings.TrimSpace(allowedExt)) == ext {
			return true
		}
	}
	return false
}

func processMessages(ctx context.Context, api *tg.Client, sender *message.Sender, dl *downloader.Downloader, messages []tg.MessageClass, peer *tg.InputPeerUser, cfg *config.Config) {
	var successDownloads, failedDownloads []string
	for _, item := range messages {
		msg, ok := item.(*tg.Message)
		if !ok || getPeerID(msg.PeerID) != peer.UserID {
			continue
		}
		media, ok := msg.Media.(*tg.MessageMediaDocument)
		if !ok {
			continue
		}
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			continue
		}
		filename := getFilename(doc)
		if filename == "" || !isAllowedExt(filename, cfg.AllowedExtensions) {
			continue
		}
		outPath := getUniqueFilename(cfg.DownloadPath, filename)
		if _, err := dl.Download(api, doc.AsInputDocumentFileLocation()).ToPath(ctx, outPath); err != nil {
			log.Printf("Failed to download %s: %v", filename, err)
			failedDownloads = append(failedDownloads, filename)
			continue
		}
		log.Printf("Downloaded: %s", outPath)
		successDownloads = append(successDownloads, filename)
	}

	var parts []string
	if len(successDownloads) > 0 {
		parts = append(parts, fmt.Sprintf("Saved %d file(s):\n\n<code>%s</code>", len(successDownloads), strings.Join(successDownloads, "\n")))
	}
	if len(failedDownloads) > 0 {
		parts = append(parts, fmt.Sprintf("Failed to download %d file(s):\n\n<code>%s</code>", len(failedDownloads), strings.Join(failedDownloads, "\n")))
	}
	if len(parts) == 0 {
		return
	}
	if _, err := sender.To(peer).StyledText(ctx, html.String(nil, strings.Join(parts, "\n\n"))); err != nil {
		log.Printf("Failed to send notification: %v", err)
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	syncClock()

	configPath := config.ConfigPath()
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	if err := cfg.ValidateForSync(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}
	if err := os.MkdirAll(cfg.DownloadPath, 0755); err != nil {
		log.Fatalf("Failed to create download directory: %v", err)
	}

	resolver, err := setupResolver(cfg)
	if err != nil {
		log.Fatalf("Proxy configuration error: %v", err)
	}
	type clientResult struct {
		client *gotgproto.Client
		err    error
	}
	ch := make(chan clientResult, 1)
	opts := &gotgproto.ClientOpts{InMemory: true, Session: sessionMaker.SimpleSession(), Resolver: resolver, DialTimeout: 90 * time.Second, DisableCopyright: true}
	go func() {
		client, err := gotgproto.NewClient(appID, appHash, gotgproto.ClientTypeBot(cfg.BotToken), opts)
		ch <- clientResult{client: client, err: err}
	}()

	var client *gotgproto.Client
	select {
	case <-ctx.Done():
		log.Fatalf("Timed out waiting for Telegram login: %v", ctx.Err())
	case result := <-ch:
		if result.err != nil {
			log.Fatalf("Telegram login failed: %v", result.err)
		}
		client = result.client
	}
	defer client.Stop()

	api := client.API()
	sender := message.NewSender(api)
	peer := &tg.InputPeerUser{UserID: cfg.ChatID, AccessHash: 0}

	if cfg.UpdatesState.Pts == 0 {
		state, err := api.UpdatesGetState(ctx)
		if err != nil {
			log.Fatalf("Failed to get Telegram state: %v", err)
		}
		cfg.UpdatesState = config.TelegramUpdatesState{Pts: state.Pts, Date: state.Date, Qts: state.Qts}
		if err := cfg.Save(configPath); err != nil {
			log.Fatalf("Failed to save initial state: %v", err)
		}
		_, _ = sender.To(peer).StyledText(ctx, html.String(nil, fmt.Sprintf("Chat %d initialized successfully.", cfg.ChatID)))
		log.Println("Initialized successfully. New files will be processed on the next sync.")
		return
	}

	dl := downloader.NewDownloader()
	for {
		diff, err := api.UpdatesGetDifference(ctx, &tg.UpdatesGetDifferenceRequest{Pts: cfg.UpdatesState.Pts, Date: cfg.UpdatesState.Date, Qts: cfg.UpdatesState.Qts})
		if err != nil {
			log.Fatalf("Failed to get Telegram updates: %v", err)
		}

		done := true
		switch d := diff.(type) {
		case *tg.UpdatesDifferenceEmpty:
			log.Println("No new messages.")
			cfg.UpdatesState.Date = d.Date
		case *tg.UpdatesDifference:
			processMessages(ctx, api, sender, dl, d.NewMessages, peer, cfg)
			cfg.UpdatesState = config.TelegramUpdatesState{Pts: d.State.Pts, Date: d.State.Date, Qts: d.State.Qts}
		case *tg.UpdatesDifferenceSlice:
			processMessages(ctx, api, sender, dl, d.NewMessages, peer, cfg)
			cfg.UpdatesState = config.TelegramUpdatesState{Pts: d.IntermediateState.Pts, Date: d.IntermediateState.Date, Qts: d.IntermediateState.Qts}
			done = false
		case *tg.UpdatesDifferenceTooLong:
			log.Printf("Telegram update gap is too long; advancing state to PTS %d", d.Pts)
			cfg.UpdatesState.Pts = d.Pts
		default:
			log.Printf("Unexpected Telegram update type: %T", diff)
		}
		if err := cfg.Save(configPath); err != nil {
			log.Fatalf("Failed to save Telegram state: %v", err)
		}
		if done {
			break
		}
	}
	log.Println("Sync completed successfully.")
}
