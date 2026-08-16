package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/beevik/ntp"
	"github.com/dpolarov/KindleTeleSync-re/internal/config"
)

type lockInfo struct {
	PID       int    `json:"pid"`
	Action    string `json:"action"`
	StartedAt string `json:"started_at"`
}

func lockPath() string { return filepath.Join(config.AppDir(), ".kindletelesync.lock") }

func acquireAppLock(action string) (func(), error) {
	if err := os.MkdirAll(config.AppDir(), 0755); err != nil {
		return nil, err
	}
	path := lockPath()
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			info := lockInfo{PID: os.Getpid(), Action: action, StartedAt: time.Now().UTC().Format(time.RFC3339)}
			_ = json.NewEncoder(f).Encode(info)
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}

		data, readErr := os.ReadFile(path)
		var existing lockInfo
		if readErr == nil {
			_ = json.Unmarshal(data, &existing)
		}
		if existing.PID > 0 && processExists(existing.PID) {
			label := existing.Action
			if label == "" {
				label = "another operation"
			}
			return nil, fmt.Errorf("KindleTeleSync is busy: %s is already running (PID %d)", label, existing.PID)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove stale lock: %w", err)
		}
	}
	return nil, errors.New("cannot acquire KindleTeleSync operation lock")
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func effectiveGOARM() string {
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		text := strings.ToLower(string(b))
		if strings.Contains(text, "armv7") || strings.Contains(text, "vfpv3") || strings.Contains(text, " neon ") {
			return "7"
		}
		if strings.Contains(text, "armv6") || strings.Contains(text, "armv5") {
			return "6"
		}
	}
	if GoArmVersion == "7" {
		return "7"
	}
	return "6"
}

func kernelRelease() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return "unknown"
	}
	buf := make([]byte, 0, len(u.Release))
	for _, c := range u.Release {
		if c == 0 {
			break
		}
		buf = append(buf, byte(c))
	}
	if len(buf) == 0 {
		return "unknown"
	}
	return string(buf)
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

func syncClock() (string, error) {
	servers := []string{"pool.ntp.org", "time.cloudflare.com", "time.google.com"}
	var lastErr error
	for _, server := range servers {
		t, err := ntp.Time(server)
		if err != nil {
			lastErr = err
			continue
		}
		tv := syscall.NsecToTimeval(t.UnixNano())
		if err := syscall.Settimeofday(&tv); err != nil {
			return server, fmt.Errorf("NTP succeeded but setting the Kindle clock failed: %w", err)
		}
		return server, nil
	}
	if lastErr == nil {
		lastErr = errors.New("all NTP servers failed")
	}
	return "", lastErr
}

func clockSkew() (time.Duration, string, error) {
	servers := []string{"pool.ntp.org", "time.cloudflare.com", "time.google.com"}
	var lastErr error
	for _, server := range servers {
		t, err := ntp.Time(server)
		if err != nil {
			lastErr = err
			continue
		}
		return time.Until(t), server, nil
	}
	return 0, "", lastErr
}

func ensureWritableDirectory(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}

	rootReal, err := filepath.EvalSymlinks(config.KindleRoot())
	if err != nil {
		return fmt.Errorf("resolve Kindle storage root: %w", err)
	}
	pathReal, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve download directory: %w", err)
	}
	rel, err := filepath.Rel(rootReal, pathReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("resolved download path %q is outside Kindle storage %q", pathReal, rootReal)
	}

	name := filepath.Join(path, ".kindletelesync-write-test-"+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(name, []byte("ok\n"), 0600); err != nil {
		return err
	}
	return os.Remove(name)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for q := n / unit; q >= unit && exp < 3; q /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
