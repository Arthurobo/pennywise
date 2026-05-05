package cli

import (
	"path/filepath"
	"testing"
)

func TestUpdatedBinaryPath_GOBIN(t *testing.T) {
	t.Setenv("GOBIN", "/custom/gobin")
	t.Setenv("GOPATH", "/should/be/ignored")
	got, err := updatedBinaryPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/custom/gobin", "pennywise")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUpdatedBinaryPath_GOPATH(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/custom/gopath")
	got, err := updatedBinaryPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/custom/gopath", "bin", "pennywise")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUpdatedBinaryPath_DefaultHome(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	t.Setenv("HOME", "/test/home")
	got, err := updatedBinaryPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/test/home", "go", "bin", "pennywise")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
