package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dpolarov/KindleTeleSync-re/internal/config"
)

var (
	Version      = "dev"
	GoArmVersion = "unknown"
	RepoPath     = "dpolarov/KindleTeleSync-re"
)

type Result struct {
	OK         bool           `json:"ok"`
	Action     string         `json:"action"`
	Message    string         `json:"message"`
	Downloaded []string       `json:"downloaded,omitempty"`
	Skipped    []string       `json:"skipped,omitempty"`
	Errors     []string       `json:"errors,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

func main() {
	closeLog := setupLogging()
	defer closeLog()

	if len(os.Args) < 2 {
		printHelp()
		return
	}

	command := os.Args[1]
	args := os.Args[2:]
	jsonOutput := hasFlag(args, "--json")
	kualOutput := hasFlag(args, "--kual")
	var result Result

	switch command {
	case "sync":
		result = withLock("sync", func() Result { return runSync() })
	case "test":
		result = withLock("test", func() Result { return runTelegramTest() })
	case "diagnostics", "doctor":
		result = runDiagnostics()
	case "update":
		result = withLock("update", func() Result { return runUpdate() })
	case "web":
		kindleUI := hasFlag(args, "--kindle-ui")
		result = withLock("web", func() Result { return runWebServer(kindleUI) })
	case "web-url":
		token, err := webToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(webURL(token))
		return
	case "web-stop":
		result = stopWebServer()
	case "version":
		result = versionResult()
	case "help", "--help", "-h":
		printHelp()
		return
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printHelp()
		os.Exit(2)
	}

	emitResult(result, jsonOutput)
	if kualOutput {
		renderKUALResult(result)
	}
	if !result.OK {
		os.Exit(1)
	}
}

func withLock(action string, fn func() Result) Result {
	unlock, err := acquireAppLock(action)
	if err != nil {
		return Result{OK: false, Action: action, Message: err.Error(), Errors: []string{err.Error()}}
	}
	defer unlock()
	return fn()
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func emitResult(result Result, asJSON bool) {
	_ = writeLastResult(result)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(result)
		return
	}

	fmt.Println(result.Message)
	if len(result.Downloaded) > 0 {
		fmt.Printf("Downloaded (%d):\n", len(result.Downloaded))
		for _, item := range result.Downloaded {
			fmt.Printf("  + %s\n", item)
		}
	}
	if len(result.Skipped) > 0 {
		fmt.Printf("Skipped (%d):\n", len(result.Skipped))
		for _, item := range result.Skipped {
			fmt.Printf("  - %s\n", item)
		}
	}
	if len(result.Errors) > 0 {
		fmt.Printf("Errors (%d):\n", len(result.Errors))
		for _, item := range result.Errors {
			fmt.Printf("  ! %s\n", item)
		}
	}
}

func writeLastResult(result Result) error {
	if err := os.MkdirAll(config.AppDir(), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	target := filepath.Join(config.AppDir(), "last_result.json")
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func setupLogging() func() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	if err := os.MkdirAll(config.AppDir(), 0755); err != nil {
		return func() {}
	}
	path := filepath.Join(config.AppDir(), "sync.log")
	if info, err := os.Stat(path); err == nil && info.Size() > 5*1024*1024 {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return func() {}
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	return func() { _ = f.Close() }
}

func versionResult() Result {
	arm := effectiveGOARM()
	return Result{
		OK:      true,
		Action:  "version",
		Message: fmt.Sprintf("KindleTeleSync %s (linux/arm%s)", Version, arm),
		Details: map[string]any{
			"version": Version,
			"goarm":   arm,
			"repo":    RepoPath,
		},
	}
}

func printHelp() {
	text := `KindleTeleSync

Usage:
  kindletelesync sync [--json] [--kual]         Download new Telegram files
  kindletelesync test [--json] [--kual]         Test Telegram login and chat delivery
  kindletelesync diagnostics [--json] [--kual]  Check config, storage, clock and network
  kindletelesync update [--json] [--kual]       Verify and install the latest release
  kindletelesync web [--kindle-ui]              Start protected web settings
  kindletelesync web-url                         Print the protected settings URL
  kindletelesync web-stop [--json]               Stop the settings server
  kindletelesync version [--json] [--kual]      Show version and detected ARM target
`
	fmt.Print(strings.TrimSpace(text) + "\n")
}

func timeoutDuration(seconds int, fallback int) time.Duration {
	if seconds <= 0 {
		seconds = fallback
	}
	return time.Duration(seconds) * time.Second
}
