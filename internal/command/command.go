package command

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxCommandBytes = 8 * 1024

// Risk is a high-signal warning about a generated command. It is advisory;
// generated commands are always returned for user review rather than run.
type Risk struct {
	Category string
	Message  string
}

// Validate enforces the shell-independent output contract and then parses the
// command with the resolved target shell without executing it.
func (target Target) Validate(ctx context.Context, value string) (string, error) {
	value, err := validateText(value)
	if err != nil {
		return "", err
	}
	adapter, err := adapterFor(target.Name)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(target.Executable) == "" {
		return "", fmt.Errorf("%s executable is not resolved", target.Name)
	}
	if err := adapter.check(ctx, target, value); err != nil {
		return "", err
	}
	return value, nil
}

func validateText(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("command is empty")
	}
	if len(value) > MaxCommandBytes {
		return "", fmt.Errorf("command exceeds %d bytes", MaxCommandBytes)
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("command is not valid UTF-8")
	}
	for _, r := range value {
		if r == '\n' || r == '\r' {
			return "", fmt.Errorf("command must be a single line")
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return "", fmt.Errorf("command contains a control or formatting character")
		}
	}
	return value, nil
}

var riskRules = []struct {
	category string
	message  string
	pattern  *regexp.Regexp
}{
	{"remote-execution", "downloads or streams remote content into a shell", regexp.MustCompile("(?i)\\b(curl|wget)\\b[^|;&]*(\\||\\|&)\\s*(sh|bash|zsh|fish)\\b")},
	{"destructive", "may delete files or irreversibly remove data", regexp.MustCompile("(?i)\\b(rm|rmdir|unlink|shred|truncate)\\b|\\bfind\\b[^;&|]*\\s-delete\\b|\\bgit\\s+clean\\b|\\bdocker\\s+system\\s+prune\\b|\\bkubectl\\s+delete\\b")},
	{"privilege", "uses privilege escalation or changes ownership or permissions", regexp.MustCompile("(?i)(^|[;&|()]\\s*)\\b(sudo|doas|su|chmod|chown|chgrp)\\b")},
	{"process", "may terminate or signal running processes", regexp.MustCompile("(?i)(^|[;&|()]\\s*)\\b(kill|killall|pkill)\\b")},
	{"installation", "installs or changes system or user software", regexp.MustCompile("(?i)\\b(apt|apt-get|dnf|yum|pacman|apk|brew|pip|pip3|npm|pnpm|yarn|cargo|gem)\\b[^;&|]*(\\sinstall\\b|\\sadd\\b)")},
	{"disk", "performs a low-level disk, filesystem, or partition operation", regexp.MustCompile("(?i)(^|[;&|()]\\s*)\\b(dd|mkfs(?:\\.[a-z0-9]+)?|fdisk|parted)\\b|\\bdiskutil\\b[^;&|]*\\b(erase|partition)")},
	{"network-upload", "may upload data or open a raw network connection", regexp.MustCompile("(?i)(^|[;&|()]\\s*)\\b(scp|sftp|ftp|nc|netcat)\\b|\\bcurl\\b[^;&|]*(--upload-file|-T\\s|--data|-d\\s)")},
	{"in-place-write", "may modify a file in place", regexp.MustCompile("(?i)(^|[;&|()]\\s*)\\b(sed\\b[^;&|]*\\s-i(?:[^\\s;&|]*)?(?:\\s|$)|tee\\b)")},
}

// Classify returns advisory, high-signal risks in stable rule order.
func Classify(value string) []Risk {
	risks := make([]Risk, 0, len(riskRules)+1)
	for _, rule := range riskRules {
		if rule.pattern.MatchString(value) {
			risks = append(risks, Risk{Category: rule.category, Message: rule.message})
		}
	}
	if hasOverwriteRedirection(value) {
		risks = append(risks, Risk{Category: "overwrite", Message: "contains output redirection that may overwrite a file"})
	}
	return risks
}

var overwriteRedirection = regexp.MustCompile("(^|[\\s;&|()])(?:1>|>)\\s*([^\\s;&|]+)")

func hasOverwriteRedirection(value string) bool {
	for _, match := range overwriteRedirection.FindAllStringSubmatch(value, -1) {
		if len(match) < 3 {
			continue
		}
		target := strings.Trim(match[2], "\"'")
		if target == "/dev/null" || target == "/dev/stderr" || target == "/dev/stdout" {
			continue
		}
		return true
	}
	return false
}
