package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dpolarov/KindleTeleSync-re/internal/config"
	"golang.org/x/mod/semver"
)

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	Prerelease bool          `json:"prerelease"`
	Draft      bool          `json:"draft"`
	Assets     []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func runUpdate(includePrerelease bool) Result {
	currentVer := installedVersion()
	client := &http.Client{Timeout: 5 * time.Minute}
	release, err := fetchLatestRelease(client, includePrerelease)
	if err != nil {
		return failure("update", "Failed to check GitHub releases", err)
	}
	latestVer := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	if latestVer == "" {
		return failure("update", "Latest release has no version tag", fmt.Errorf("empty tag_name"))
	}
	channel := "stable"
	if includePrerelease {
		channel = "prerelease"
	}
	if currentVer != "" {
		switch compareVersions(currentVer, latestVer) {
		case 0:
			return Result{OK: true, Action: "update", Message: fmt.Sprintf("Already up to date (%s).", currentVer), Details: map[string]any{"current": currentVer, "latest": latestVer, "goarm": effectiveGOARM(), "channel": channel}}
		case 1:
			return Result{OK: true, Action: "update", Message: fmt.Sprintf("Installed version %s is newer than the latest %s release (%s); refusing to downgrade.", currentVer, channel, latestVer), Details: map[string]any{"current": currentVer, "latest": latestVer, "goarm": effectiveGOARM(), "channel": channel}}
		}
	}

	arch := effectiveGOARM()
	archiveName := "KindleTeleSync-arm" + arch + ".tar.gz"
	archiveAsset, ok := findAsset(release.Assets, archiveName)
	if !ok {
		archiveName = "KindleTeleSync-universal.tar.gz"
		archiveAsset, ok = findAsset(release.Assets, archiveName)
	}
	if !ok {
		return failure("update", "No compatible release archive found", fmt.Errorf("GOARM %s", arch))
	}
	checksumAsset, ok := findAsset(release.Assets, "SHA256SUMS")
	if !ok {
		return failure("update", "Release is missing SHA256SUMS", fmt.Errorf("refusing unverified update"))
	}

	expected, err := fetchExpectedChecksum(client, checksumAsset.BrowserDownloadURL, archiveName)
	if err != nil {
		return failure("update", "Failed to read release checksum", err)
	}

	tmpRoot, err := os.MkdirTemp("", "kindletelesync-update-")
	if err != nil {
		return failure("update", "Failed to create update directory", err)
	}
	defer os.RemoveAll(tmpRoot)
	archivePath := filepath.Join(tmpRoot, archiveName)
	actual, err := downloadFileSHA256(client, archiveAsset.BrowserDownloadURL, archivePath, 200*1024*1024)
	if err != nil {
		return failure("update", "Failed to download release archive", err)
	}
	if !strings.EqualFold(expected, actual) {
		return failure("update", "Release checksum verification failed", fmt.Errorf("expected %s, got %s", expected, actual))
	}

	extractDir := filepath.Join(tmpRoot, "extract")
	if err := extractArchive(archivePath, extractDir); err != nil {
		return failure("update", "Failed to extract verified release", err)
	}
	if err := applyUpdate(extractDir, config.KindleRoot()); err != nil {
		return failure("update", "Failed to install update", err)
	}
	cleanupWarnings := cleanupLegacyFiles(config.AppDir())
	if err := os.WriteFile(filepath.Join(config.AppDir(), "version.txt"), []byte(latestVer+"\n"), 0644); err != nil {
		return failure("update", "Update installed but version file could not be written", err)
	}

	return Result{
		OK:      true,
		Action:  "update",
		Message: fmt.Sprintf("Updated KindleTeleSync from %s to %s. Restart KOReader to reload the plugin.", valueOrUnknown(currentVer), latestVer),
		Skipped: cleanupWarnings,
		Details: map[string]any{"current": currentVer, "latest": latestVer, "goarm": arch, "asset": archiveName, "sha256": actual, "channel": channel},
	}
}

func installedVersion() string {
	b, err := os.ReadFile(filepath.Join(config.AppDir(), "version.txt"))
	if err == nil {
		if v := strings.TrimPrefix(strings.TrimSpace(string(b)), "v"); v != "" {
			return v
		}
	}
	if Version != "" && Version != "dev" {
		return strings.TrimPrefix(Version, "v")
	}
	return ""
}

func compareVersions(current, latest string) int {
	currentSemver := "v" + strings.TrimPrefix(strings.TrimSpace(current), "v")
	latestSemver := "v" + strings.TrimPrefix(strings.TrimSpace(latest), "v")
	if semver.IsValid(currentSemver) && semver.IsValid(latestSemver) {
		return semver.Compare(currentSemver, latestSemver)
	}
	if current == latest {
		return 0
	}
	return -1
}

func fetchLatestRelease(client *http.Client, includePrerelease bool) (*githubRelease, error) {
	if !includePrerelease {
		return fetchReleaseEndpoint(client, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", RepoPath))
	}

	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=20", RepoPath))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}
	var releases []githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(&releases); err != nil {
		return nil, err
	}
	for i := range releases {
		if !releases[i].Draft {
			return &releases[i], nil
		}
	}
	return nil, fmt.Errorf("GitHub returned no published releases")
}

func fetchReleaseEndpoint(client *http.Client, endpoint string) (*githubRelease, error) {
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

func findAsset(assets []githubAsset, name string) (githubAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name && asset.BrowserDownloadURL != "" {
			return asset, true
		}
	}
	return githubAsset{}, false
}

func fetchExpectedChecksum(client *http.Client, url, filename string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum download returned %s", resp.Status)
	}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 1024*1024))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == filename {
			hash := strings.ToLower(fields[0])
			if len(hash) != sha256.Size*2 {
				return "", fmt.Errorf("invalid SHA-256 for %s", filename)
			}
			if _, err := hex.DecodeString(hash); err != nil {
				return "", fmt.Errorf("invalid SHA-256 for %s: %w", filename, err)
			}
			return hash, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("checksum for %s not found", filename)
}

func downloadFileSHA256(client *http.Client, url, path string, limit int64) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %s", resp.Status)
	}
	if resp.ContentLength > limit {
		return "", fmt.Errorf("release archive is too large: %s", humanBytes(resp.ContentLength))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written > limit {
		_ = os.Remove(path)
		return "", fmt.Errorf("release archive exceeded %s limit", humanBytes(limit))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func extractArchive(archivePath, dstDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}

	tr := tar.NewReader(gzr)
	var extracted int64
	const maxExtracted = int64(300 * 1024 * 1024)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dstDir, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || extracted+header.Size > maxExtracted {
				return fmt.Errorf("release archive exceeds extraction safety limit")
			}
			extracted += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			mode := os.FileMode(header.Mode).Perm()
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(out, tr, header.Size)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported archive entry %q", header.Name)
		}
	}
	return nil
}

func applyUpdate(srcRoot, kindleRoot string) error {
	return filepath.Walk(srcRoot, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if srcPath == srcRoot {
			return nil
		}
		rel, err := filepath.Rel(srcRoot, srcPath)
		if err != nil {
			return err
		}
		dst, err := safeJoin(kindleRoot, rel)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		if filepath.Base(rel) == "config.json" {
			if _, err := os.Stat(dst); err == nil {
				return nil
			}
		}
		return copyFileAtomic(srcPath, dst, info.Mode().Perm())
	})
}

func safeJoin(baseDir, name string) (string, error) {
	target := filepath.Join(baseDir, name)
	rel, err := filepath.Rel(baseDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return target, nil
}

func copyFileAtomic(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func cleanupLegacyFiles(appDir string) []string {
	legacy := []string{
		"kindle_sync_d",
		"updater",
		"webconfig",
		"KindleTeleSync.sh",
		"update.sh",
		"web.sh",
		"fbink",
		"qr.png",
	}
	var warnings []string
	for _, name := range legacy {
		path := filepath.Join(appDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			warnings = append(warnings, fmt.Sprintf("Could not remove legacy %s: %v", name, err))
		}
	}
	return warnings
}

func valueOrUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}
