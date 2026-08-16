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
	"time"

	"github.com/dpolarov/KindleTeleSync-re/internal/config"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"
	"golang.org/x/net/proxy"
)

const (
	appID   = 2040
	appHash = "b18441a1ff607e10a989891a5462e627"
)

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

type syncStats struct {
	downloaded []string
	skipped    []string
	errors     []string
	totalBytes int64
}

func runSync() Result {
	cfg, err := config.LoadOrCreate(config.ConfigPath())
	if err != nil {
		return failure("sync", "Failed to load configuration", err)
	}
	if err := cfg.ValidateForSync(); err != nil {
		return failure("sync", "Invalid configuration", err)
	}
	if err := ensureWritableDirectory(cfg.DownloadPath); err != nil {
		return failure("sync", "Download directory is not writable", err)
	}

	if server, err := syncClock(); err != nil {
		log.Printf("Clock synchronization warning: %v", err)
	} else {
		log.Printf("Clock synchronized via %s", server)
	}

	resolver, err := setupResolver(cfg)
	if err != nil {
		return failure("sync", "Proxy configuration error", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration(cfg.SyncTimeout, config.DefaultSyncTimeoutSeconds))
	defer cancel()

	client := telegram.NewClient(appID, appHash, telegram.Options{
		Resolver:    resolver,
		DialTimeout: 90 * time.Second,
		NoUpdates:   true,
		AllowCDN:    true,
	})
	stats := &syncStats{}
	initialized := false

	err = client.Run(ctx, func(ctx context.Context) error {
		if _, err := client.Auth().Bot(ctx, cfg.BotToken); err != nil {
			return fmt.Errorf("Telegram bot login: %w", err)
		}
		api := client.API()
		sender := message.NewSender(api)
		peer := &tg.InputPeerUser{UserID: cfg.ChatID}

		if cfg.UpdatesState.Pts == 0 {
			state, err := api.UpdatesGetState(ctx)
			if err != nil {
				return fmt.Errorf("get initial Telegram state: %w", err)
			}
			cfg.UpdatesState = config.TelegramUpdatesState{Pts: state.Pts, Date: state.Date, Qts: state.Qts}
			if err := cfg.Save(config.ConfigPath()); err != nil {
				return fmt.Errorf("save initial Telegram state: %w", err)
			}
			initialized = true
			if cfg.SendNotifications {
				_, _ = sender.To(peer).Text(ctx, fmt.Sprintf("KindleTeleSync initialized for chat %d. New files will be downloaded on the next sync.", cfg.ChatID))
			}
			return nil
		}

		for {
			diff, err := api.UpdatesGetDifference(ctx, &tg.UpdatesGetDifferenceRequest{
				Pts:  cfg.UpdatesState.Pts,
				Date: cfg.UpdatesState.Date,
				Qts:  cfg.UpdatesState.Qts,
			})
			if err != nil {
				return fmt.Errorf("get Telegram updates: %w", err)
			}

			done := true
			switch d := diff.(type) {
			case *tg.UpdatesDifferenceEmpty:
				cfg.UpdatesState.Date = d.Date
			case *tg.UpdatesDifference:
				processMessages(ctx, client, d.NewMessages, peer, cfg, stats)
				cfg.UpdatesState = config.TelegramUpdatesState{Pts: d.State.Pts, Date: d.State.Date, Qts: d.State.Qts}
			case *tg.UpdatesDifferenceSlice:
				processMessages(ctx, client, d.NewMessages, peer, cfg, stats)
				cfg.UpdatesState = config.TelegramUpdatesState{Pts: d.IntermediateState.Pts, Date: d.IntermediateState.Date, Qts: d.IntermediateState.Qts}
				done = false
			case *tg.UpdatesDifferenceTooLong:
				appendCapped(&stats.skipped, fmt.Sprintf("Telegram history gap was too long; state advanced to PTS %d", d.Pts))
				cfg.UpdatesState.Pts = d.Pts
			default:
				appendCapped(&stats.errors, fmt.Sprintf("Unexpected Telegram update type %T", diff))
			}
			if err := cfg.Save(config.ConfigPath()); err != nil {
				return fmt.Errorf("save Telegram state: %w", err)
			}
			if done {
				break
			}
		}

		if cfg.SendNotifications && (len(stats.downloaded) > 0 || len(stats.errors) > 0) {
			text := notificationText(stats)
			if _, err := sender.To(peer).Text(ctx, text); err != nil {
				log.Printf("Failed to send Telegram summary: %v", err)
			}
		}
		return nil
	})
	if err != nil {
		return Result{
			OK:         false,
			Action:     "sync",
			Message:    "Synchronization failed: " + err.Error(),
			Downloaded: stats.downloaded,
			Skipped:    stats.skipped,
			Errors:     append(stats.errors, err.Error()),
			Details: map[string]any{
				"bytes_downloaded": stats.totalBytes,
			},
		}
	}
	if initialized {
		return Result{OK: true, Action: "sync", Message: "KindleTeleSync initialized. Run sync again after sending a new file to the bot."}
	}

	messageText := "Sync completed. No new matching files."
	if len(stats.downloaded) > 0 {
		messageText = fmt.Sprintf("Sync completed: downloaded %d file(s), %s.", len(stats.downloaded), humanBytes(stats.totalBytes))
	}
	if len(stats.errors) > 0 {
		messageText = fmt.Sprintf("Sync completed with %d error(s).", len(stats.errors))
	}
	return Result{
		OK:         len(stats.errors) == 0,
		Action:     "sync",
		Message:    messageText,
		Downloaded: stats.downloaded,
		Skipped:    stats.skipped,
		Errors:     stats.errors,
		Details: map[string]any{
			"bytes_downloaded": stats.totalBytes,
		},
	}
}

func runTelegramTest() Result {
	cfg, err := config.LoadOrCreate(config.ConfigPath())
	if err != nil {
		return failure("test", "Failed to load configuration", err)
	}
	if err := cfg.ValidateForSync(); err != nil {
		return failure("test", "Invalid configuration", err)
	}
	resolver, err := setupResolver(cfg)
	if err != nil {
		return failure("test", "Proxy configuration error", err)
	}
	if server, err := syncClock(); err != nil {
		log.Printf("Clock synchronization warning before Telegram test: %v", err)
	} else {
		log.Printf("Clock synchronized via %s before Telegram test", server)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := telegram.NewClient(appID, appHash, telegram.Options{Resolver: resolver, DialTimeout: 90 * time.Second, NoUpdates: true})
	err = client.Run(ctx, func(ctx context.Context) error {
		if _, err := client.Auth().Bot(ctx, cfg.BotToken); err != nil {
			return fmt.Errorf("Telegram bot login: %w", err)
		}
		if err := client.Ping(ctx); err != nil {
			return fmt.Errorf("Telegram ping: %w", err)
		}
		peer := &tg.InputPeerUser{UserID: cfg.ChatID}
		if _, err := message.NewSender(client.API()).To(peer).Text(ctx, "KindleTeleSync connection test: OK."); err != nil {
			return fmt.Errorf("send test message to chat %d: %w", cfg.ChatID, err)
		}
		return nil
	})
	if err != nil {
		return failure("test", "Telegram test failed", err)
	}
	return Result{OK: true, Action: "test", Message: fmt.Sprintf("Telegram connection is working. A test message was sent to chat %d.", cfg.ChatID)}
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
			if cd, ok := dialer.(proxy.ContextDialer); ok {
				return cd.DialContext(ctx, network, addr)
			}
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
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		defer conn.SetDeadline(time.Time{})

		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: make(http.Header)}
		if user != "" || pass != "" {
			auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
			req.Header.Set("Proxy-Authorization", "Basic "+auth)
		}
		if err := req.Write(conn); err != nil {
			return nil, err
		}
		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, req)
		if err != nil {
			return nil, fmt.Errorf("read HTTP proxy response: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			return nil, fmt.Errorf("HTTP proxy CONNECT failed: %s", resp.Status)
		}
		ok = true
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
}

func processMessages(ctx context.Context, client *telegram.Client, messages []tg.MessageClass, peer *tg.InputPeerUser, cfg *config.Config, stats *syncStats) {
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
		if filename == "" {
			continue
		}
		if !isAllowedExt(filename, cfg.AllowedExtensions) {
			appendCapped(&stats.skipped, filename+" (extension not allowed)")
			continue
		}
		if len(stats.downloaded) >= cfg.MaxFilesPerSync {
			appendCapped(&stats.skipped, filename+" (per-sync file limit reached)")
			continue
		}
		if doc.Size > cfg.MaxFileBytes {
			appendCapped(&stats.skipped, fmt.Sprintf("%s (%s exceeds %s file limit)", filename, humanBytes(doc.Size), humanBytes(cfg.MaxFileBytes)))
			continue
		}
		if doc.Size > 0 && stats.totalBytes+doc.Size > cfg.MaxTotalBytes {
			appendCapped(&stats.skipped, fmt.Sprintf("%s (would exceed %s total sync limit)", filename, humanBytes(cfg.MaxTotalBytes)))
			continue
		}

		outPath := getUniqueFilename(cfg.DownloadPath, filename)
		if _, err := client.Download(doc.AsInputDocumentFileLocation()).ToPath(ctx, outPath); err != nil {
			_ = os.Remove(outPath)
			log.Printf("Failed to download %s: %v", filename, err)
			appendCapped(&stats.errors, fmt.Sprintf("%s: %v", filename, err))
			continue
		}
		stats.downloaded = append(stats.downloaded, outPath)
		if doc.Size > 0 {
			stats.totalBytes += doc.Size
		} else if info, err := os.Stat(outPath); err == nil {
			stats.totalBytes += info.Size()
		}
		log.Printf("Downloaded: %s", outPath)
	}
}

func getUniqueFilename(baseDir, filename string) string {
	filename = filepath.Base(filename)
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

func getPeerID(peer tg.PeerClass) int64 {
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

func appendCapped(dst *[]string, value string) {
	const maxItems = 50
	if len(*dst) < maxItems {
		*dst = append(*dst, value)
	}
}

func notificationText(stats *syncStats) string {
	var b strings.Builder
	if len(stats.downloaded) > 0 {
		fmt.Fprintf(&b, "KindleTeleSync downloaded %d file(s):\n", len(stats.downloaded))
		for i, path := range stats.downloaded {
			if i >= 12 {
				fmt.Fprintf(&b, "…and %d more\n", len(stats.downloaded)-i)
				break
			}
			fmt.Fprintf(&b, "• %s\n", filepath.Base(path))
		}
	}
	if len(stats.errors) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Errors: %d. See sync.log on the Kindle for details.", len(stats.errors))
	}
	return strings.TrimSpace(b.String())
}

func failure(action, message string, err error) Result {
	text := message
	errorsList := []string{}
	if err != nil {
		text += ": " + err.Error()
		errorsList = append(errorsList, err.Error())
	}
	return Result{OK: false, Action: action, Message: text, Errors: errorsList}
}
