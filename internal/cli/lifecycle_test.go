//go:build !windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPIDFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pennywise.pid")

	want := pidFileState{
		PID:       12345,
		StartTime: time.Unix(1700000000, 0).UTC(),
	}
	if err := writePIDFile(path, want); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}

	got, err := readPIDFile(path)
	if err != nil {
		t.Fatalf("readPIDFile: %v", err)
	}
	if got.PID != want.PID {
		t.Fatalf("PID: got %d, want %d", got.PID, want.PID)
	}
	if !got.StartTime.Equal(want.StartTime) {
		t.Fatalf("StartTime: got %v, want %v", got.StartTime, want.StartTime)
	}
}

func TestReadPIDFile_Missing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.pid")
	_, err := readPIDFile(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestReadPIDFile_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.pid")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := readPIDFile(path)
	if err == nil {
		t.Fatalf("expected error on empty file")
	}
}

func TestReadPIDFile_BogusPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bogus.pid")
	if err := os.WriteFile(path, []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := readPIDFile(path)
	if err == nil {
		t.Fatalf("expected error on bogus PID")
	}
}

func TestProcessAlive_Self(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatalf("processAlive(self) should be true")
	}
}

func TestProcessAlive_NonExistent(t *testing.T) {
	// Find a PID that's almost certainly not in use. PID 0 is a kernel
	// scheduler placeholder on Linux — kill(0,0) is the broadcast variant
	// rather than a process probe. We use a deliberately huge PID instead;
	// if the kernel happens to assign it, the test author owes you a beer.
	const fakePID = 999999999
	if processAlive(fakePID) {
		t.Fatalf("processAlive(%d) should be false", fakePID)
	}
}

func TestProcessAlive_ZeroAndNegative(t *testing.T) {
	if processAlive(0) {
		t.Fatalf("processAlive(0) should be false (broadcast variant)")
	}
	if processAlive(-1) {
		t.Fatalf("processAlive(-1) should be false")
	}
}

func TestReadPIDFileAlive_StaleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stale.pid")
	// Write a clearly-dead PID.
	if err := os.WriteFile(path, []byte("999999999\n0\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := readPIDFileAlive(path)
	if !errors.Is(err, errNotRunning) {
		t.Fatalf("expected errNotRunning, got %v", err)
	}
}

func TestReadPIDFileAlive_LiveProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live.pid")
	body := []byte(fmt.Sprintf("%d\n%d\n", os.Getpid(), time.Now().Unix()))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	state, err := readPIDFileAlive(path)
	if err != nil {
		t.Fatalf("readPIDFileAlive: %v", err)
	}
	if state.PID != os.Getpid() {
		t.Fatalf("PID: got %d, want %d", state.PID, os.Getpid())
	}
}
