package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestNotifyContextCancelsOnHangup(t *testing.T) {
	ctx, stop := notifyContext(context.Background())
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("SIGHUP did not cancel the CLI context")
	}
}
