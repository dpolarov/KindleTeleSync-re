package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dpolarov/KindleTeleSync-re/internal/config"
)

var RepoPath = "dpolarov/KindleTeleSync-re"
var GoArmVersion = "unknown"

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func init() {
	log.SetFlags(log.Ldate | log.Ltime)
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

func main() {
	rootPath := config.KindleRoot()
	workingDirPath := config.AppDir()
	versionFile := filepath.Join(workingDirPath, "version.txt")
	currentVerBytes, _ := os.ReadFile(versionFile)
	currentVer := strings.TrimPrefix(strings.TrimSpace(string(currentVerBytes)), "v")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", RepoPath))
	if err != nil {
		log.Fatalf("Failed to check for updates: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("GitHub API returned %s", resp.Status)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		log.Fatalf("Failed to parse GitHub response: %v", err)
	}
	latestVer := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	if latestVer == "" {
		log.Fatal("Latest release does not have a version tag")
	}
	if currentVer == latestVer {
		log.Printf("Already up to date (%s).", currentVer)
		return
	}

	targetArch := "arm" + GoArmVersion
	var assetURL string
	for _, asset := range release.Assets {
		if strings.Contains(asset.Name, targetArch) && strings.HasSuffix(asset.Name, ".tar.gz") {
			assetURL = asset.BrowserDownloadURL
			break
		}
	}
	if assetURL == "" {
		log.Fatalf("No release archive found for %s", targetArch)
	}

	log.Printf("Updating %s -> %s", currentVer, latestVer)
	dlResp, err := client.Get(assetURL)
	if err != nil {
		log.Fatalf("Failed to download release archive: %v", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		log.Fatalf("Release download returned %s", dlResp.Status)
	}

	gzr, err := gzip.NewReader(dlResp.Body)
	if err != nil {
		log.Fatalf("Failed to open release archive: %v", err)
	}
	defer gzr.Close()

	tmpUpdateDir := filepath.Join(os.TempDir(), "kindletelesync-update")
	if err := os.RemoveAll(tmpUpdateDir); err != nil {
		log.Fatalf("Failed to clear update directory: %v", err)
	}
	if err := os.MkdirAll(tmpUpdateDir, 0755); err != nil {
		log.Fatalf("Failed to create update directory: %v", err)
	}
	defer os.RemoveAll(tmpUpdateDir)

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Archive read error: %v", err)
		}
		target, err := safeJoin(tmpUpdateDir, header.Name)
		if err != nil {
			log.Fatal(err)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				log.Fatalf("Failed to create %s: %v", target, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				log.Fatalf("Failed to create parent directory: %v", err)
			}
			mode := os.FileMode(header.Mode) & 0777
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				log.Fatalf("Failed to create %s: %v", target, err)
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil || closeErr != nil {
				log.Fatalf("Failed to extract %s: %v %v", target, copyErr, closeErr)
			}
		default:
			log.Printf("Skipping unsupported archive entry %s", header.Name)
		}
	}

	err = filepath.Walk(tmpUpdateDir, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if srcPath == tmpUpdateDir {
			return nil
		}
		rel, err := filepath.Rel(tmpUpdateDir, srcPath)
		if err != nil {
			return err
		}
		dst, err := safeJoin(rootPath, rel)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		if filepath.Base(rel) == "config.json" {
			if _, err := os.Stat(dst); err == nil {
				log.Printf("Keeping existing user configuration: %s", rel)
				return nil
			}
		}
		if err := copyFileAtomic(srcPath, dst, info.Mode().Perm()); err != nil {
			return fmt.Errorf("update %s: %w", rel, err)
		}
		log.Printf("Updated %s", rel)
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to apply update: %v", err)
	}

	if err := os.WriteFile(versionFile, []byte(latestVer+"\n"), 0644); err != nil {
		log.Fatalf("Failed to write version file: %v", err)
	}
	log.Println("Update completed successfully. Restart KOReader if the plugin files changed.")
}
