package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeJoin(t *testing.T) {
	base := t.TempDir()
	ok, err := safeJoin(base, "extensions/KindleTeleSync/file")
	if err != nil {
		t.Fatalf("safeJoin valid path: %v", err)
	}
	if !strings.HasPrefix(ok, base+string(filepath.Separator)) {
		t.Fatalf("safe path escaped base: %q", ok)
	}

	for _, name := range []string{"../outside", "extensions/../../outside"} {
		if _, err := safeJoin(base, name); err == nil {
			t.Fatalf("safeJoin accepted traversal %q", name)
		}
	}
}

func TestParseMB(t *testing.T) {
	const fallback = int64(123)
	if got := parseMB("25", fallback); got != 25*1024*1024 {
		t.Fatalf("parseMB: got %d", got)
	}
	if got := parseMB("0", fallback); got != fallback {
		t.Fatalf("parseMB invalid: got %d", got)
	}
	if got := parseMB("not-a-number", fallback); got != fallback {
		t.Fatalf("parseMB invalid string: got %d", got)
	}
}

func TestOperationLockRejectsSecondProcess(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KINDLE_ROOT", root)

	unlock, err := acquireAppLock("first")
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer unlock()

	if _, err := acquireAppLock("second"); err == nil {
		t.Fatal("second lock unexpectedly succeeded")
	}
}

func TestEffectiveGOARMBuildValue(t *testing.T) {
	old := GoArmVersion
	defer func() { GoArmVersion = old }()
	for _, want := range []string{"6", "7"} {
		GoArmVersion = want
		if got := effectiveGOARM(); got != want {
			t.Fatalf("effectiveGOARM = %q, want %q", got, want)
		}
	}
}

func TestEnsureWritableDirectoryRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	t.Setenv("KINDLE_ROOT", root)

	link := filepath.Join(root, "books-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := ensureWritableDirectory(link); err == nil {
		t.Fatal("symlink escaping Kindle root was accepted")
	}
}

func TestWriteLastResultPermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KINDLE_ROOT", root)
	if err := writeLastResult(Result{OK: true, Action: "test", Message: "ok"}); err != nil {
		t.Fatalf("writeLastResult: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "extensions", "KindleTeleSync", "last_result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}
