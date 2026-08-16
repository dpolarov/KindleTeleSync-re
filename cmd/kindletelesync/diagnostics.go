package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dpolarov/KindleTeleSync-re/internal/config"
)

func runDiagnostics() Result {
	result := Result{
		OK:      true,
		Action:  "diagnostics",
		Message: "Diagnostics completed successfully.",
		Details: map[string]any{
			"version":        Version,
			"goarm":          effectiveGOARM(),
			"kernel":         kernelRelease(),
			"min_go_kernel":  "2.6.32",
			"root":           config.KindleRoot(),
			"app_dir":        config.AppDir(),
			"local_ip":       localIP(),
		},
	}

	cfg, err := config.LoadOrCreate(config.ConfigPath())
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, "Configuration: "+err.Error())
	} else {
		result.Details["config_path"] = config.ConfigPath()
		result.Details["download_path"] = cfg.DownloadPath
		result.Details["max_file_bytes"] = cfg.MaxFileBytes
		result.Details["max_total_bytes"] = cfg.MaxTotalBytes
		result.Details["max_files_per_sync"] = cfg.MaxFilesPerSync
		result.Details["proxy_enabled"] = cfg.Proxy.Enabled
		result.Details["proxy_type"] = cfg.Proxy.Type
		if err := cfg.ValidateForSync(); err != nil {
			result.OK = false
			result.Errors = append(result.Errors, "Configuration: "+err.Error())
		}
		if err := ensureWritableDirectory(cfg.DownloadPath); err != nil {
			result.OK = false
			result.Errors = append(result.Errors, "Download directory: "+err.Error())
		}
	}

	if skew, server, err := clockSkew(); err != nil {
		result.Skipped = append(result.Skipped, "NTP clock check unavailable: "+err.Error())
	} else {
		if skew < 0 {
			skew = -skew
		}
		result.Details["ntp_server"] = server
		result.Details["clock_skew_seconds"] = int64(skew.Seconds())
		if skew > 5*time.Minute {
			result.OK = false
			result.Errors = append(result.Errors, fmt.Sprintf("Kindle clock differs from NTP by %s", skew.Round(time.Second)))
		} else if skew > 30*time.Second {
			result.Skipped = append(result.Skipped, fmt.Sprintf("Clock warning: NTP difference is %s", skew.Round(time.Second)))
		}
	}

	if _, err := net.LookupHost("api.github.com"); err != nil {
		result.Skipped = append(result.Skipped, "DNS warning: api.github.com cannot be resolved: "+err.Error())
	} else {
		result.Details["dns"] = "ok"
	}

	if info, err := os.Stat(config.ConfigPath()); err == nil {
		result.Details["config_mode"] = info.Mode().Perm().String()
		if info.Mode().Perm()&0077 != 0 {
			result.Skipped = append(result.Skipped, "Security warning: config.json is readable by other local users; it should be mode 0600")
		}
	}

	if free, err := freeBytes(config.KindleRoot()); err == nil {
		result.Details["free_bytes"] = free
		result.Details["free_space"] = humanBytes(free)
		if free < 100*1024*1024 {
			result.Skipped = append(result.Skipped, "Storage warning: less than 100 MiB is free")
		}
	}

	if b, err := os.ReadFile(lockPath()); err == nil {
		result.Details["operation_lock"] = string(b)
	}
	if exe, err := os.Executable(); err == nil {
		result.Details["binary"] = filepath.Clean(exe)
		if info, err := os.Stat(exe); err == nil {
			result.Details["binary_bytes"] = info.Size()
		}
	}

	if !result.OK {
		result.Message = fmt.Sprintf("Diagnostics found %d problem(s).", len(result.Errors))
	} else if len(result.Skipped) > 0 {
		result.Message = fmt.Sprintf("Diagnostics passed with %d warning(s).", len(result.Skipped))
	}
	return result
}

func freeBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
