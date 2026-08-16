package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func renderKUALResult(result Result) {
	if _, err := exec.LookPath("eips"); err != nil {
		return
	}

	lines := []string{"KindleTeleSync"}
	for _, line := range wrapKindleLine(result.Message, 48) {
		lines = append(lines, line)
	}
	if len(result.Downloaded) > 0 {
		lines = append(lines, fmt.Sprintf("Downloaded: %d", len(result.Downloaded)))
		for i := 0; i < len(result.Downloaded) && i < 4; i++ {
			lines = append(lines, "+ "+filepath.Base(result.Downloaded[i]))
		}
	}
	if len(result.Errors) > 0 {
		lines = append(lines, fmt.Sprintf("Errors: %d", len(result.Errors)))
		for i := 0; i < len(result.Errors) && i < 3; i++ {
			for _, line := range wrapKindleLine("! "+result.Errors[i], 48) {
				lines = append(lines, line)
			}
		}
	}
	if len(lines) > 11 {
		lines = lines[:11]
	}

	_ = exec.Command("eips", "-c").Run()
	for i, line := range lines {
		_ = exec.Command("eips", "2", fmt.Sprintf("%d", i+2), sanitizeEIPSText(line)).Run()
	}
	time.Sleep(6 * time.Second)
}

func wrapKindleLine(text string, width int) []string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" || width <= 0 {
		return nil
	}
	var lines []string
	for len(text) > width {
		cut := strings.LastIndex(text[:width+1], " ")
		if cut <= 0 {
			cut = width
		}
		lines = append(lines, text[:cut])
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		lines = append(lines, text)
	}
	return lines
}

func sanitizeEIPSText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r >= 32 && r <= 126 {
			b.WriteRune(r)
		} else if r == '\t' {
			b.WriteByte(' ')
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}
