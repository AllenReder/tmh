package safety

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxCommandBytes = 8 * 1024

type Risk struct {
	Category string
	Message  string
}

func Validate(ctx context.Context, command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("command is empty")
	}
	if len(command) > MaxCommandBytes {
		return "", fmt.Errorf("command exceeds %d bytes", MaxCommandBytes)
	}
	if !utf8.ValidString(command) {
		return "", fmt.Errorf("command is not valid UTF-8")
	}
	for _, r := range command {
		if r == '\n' || r == '\r' {
			return "", fmt.Errorf("command must be a single line")
		}
		if unicode.IsControl(r) {
			return "", fmt.Errorf("command contains a control character")
		}
	}

	zsh, err := exec.LookPath("zsh")
	if err != nil {
		return "", fmt.Errorf("zsh is required to validate generated commands")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(checkCtx, zsh, "-n", "-c", command).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("invalid zsh syntax: %s", message)
	}
	return command, nil
}

var riskRules = []struct {
	category string
	message  string
	pattern  *regexp.Regexp
}{
	{"remote-execution", "downloads or streams remote content into a shell", regexp.MustCompile(`(?i)\b(curl|wget)\b[^|;&]*(\||\|&)\s*(sh|bash|zsh)\b`)},
	{"destructive", "may delete files or irreversibly remove data", regexp.MustCompile(`(?i)\b(rm|rmdir|unlink|shred|truncate)\b|\bfind\b[^;&|]*\s-delete\b`)},
	{"privilege", "uses privilege escalation or changes ownership/permissions", regexp.MustCompile(`(?i)(^|[;&|()]\s*)\b(sudo|doas|su|chmod|chown|chgrp)\b`)},
	{"process", "may terminate or signal running processes", regexp.MustCompile(`(?i)(^|[;&|()]\s*)\b(kill|killall|pkill)\b`)},
	{"installation", "installs or changes system/user software", regexp.MustCompile(`(?i)\b(apt|apt-get|dnf|yum|pacman|apk|brew|pip|pip3|npm|pnpm|yarn|cargo|gem)\b[^;&|]*(\sinstall\b|\sadd\b)`)},
	{"disk", "performs a low-level disk, filesystem, or partition operation", regexp.MustCompile(`(?i)(^|[;&|()]\s*)\b(dd|mkfs(?:\.[a-z0-9]+)?|fdisk|parted)\b|\bdiskutil\b[^;&|]*\b(erase|partition)`)},
	{"network-upload", "may upload data or open a raw network connection", regexp.MustCompile(`(?i)(^|[;&|()]\s*)\b(scp|sftp|ftp|nc|netcat)\b|\bcurl\b[^;&|]*(--upload-file|-T\s|--data|-d\s)`)},
}

func Classify(command string) []Risk {
	risks := make([]Risk, 0, len(riskRules))
	for _, rule := range riskRules {
		if rule.pattern.MatchString(command) {
			risks = append(risks, Risk{Category: rule.category, Message: rule.message})
		}
	}
	if hasOverwriteRedirection(command) {
		risks = append(risks, Risk{Category: "overwrite", Message: "contains output redirection that may overwrite a file"})
	}
	return risks
}

var overwriteRedirection = regexp.MustCompile(`(^|[\s;&|()])(?:1>|>)\s*([^\s;&|]+)`)

func hasOverwriteRedirection(command string) bool {
	for _, match := range overwriteRedirection.FindAllStringSubmatch(command, -1) {
		if len(match) < 3 {
			continue
		}
		target := strings.Trim(match[2], `"'`)
		if target == "/dev/null" || target == "/dev/stderr" || target == "/dev/stdout" {
			continue
		}
		return true
	}
	return false
}
