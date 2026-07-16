//go:build darwin || linux

package sandbox

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func runCanaryChecks(roots []string) error {
	var directory string
	for _, root := range roots {
		info, err := os.Stat(root)
		if err == nil && info.IsDir() {
			directory = root
			break
		}
	}
	if directory == "" {
		return fmt.Errorf("sandbox canary requires an existing directory root")
	}
	if _, err := os.ReadDir(directory); err != nil {
		return fmt.Errorf("sandbox denied allowed read: %w", err)
	}
	probe := filepath.Join(directory, fmt.Sprintf(".tmh-sandbox-canary-%d", time.Now().UnixNano()))
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		_ = file.Close()
		_ = os.Remove(probe)
		return fmt.Errorf("sandbox allowed a filesystem write")
	}
	if !sandboxPermissionDenied(err) {
		return fmt.Errorf("sandbox filesystem write probe was inconclusive: %w", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err == nil {
		_ = listener.Close()
		return fmt.Errorf("sandbox allowed a network socket")
	}
	if !sandboxPermissionDenied(err) {
		return fmt.Errorf("sandbox network probe was inconclusive: %w", err)
	}
	return nil
}

func sandboxPermissionDenied(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}
