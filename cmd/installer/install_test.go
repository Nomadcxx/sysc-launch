package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallReleaseVerifiesAndInstallsBinary(t *testing.T) {
	binary := []byte("launch binary")
	digest := fmt.Sprintf("%x  sysc-launch_linux_amd64\n", sha256.Sum256(binary))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest/download/SHA256SUMS":
			fmt.Fprint(w, digest)
		case "/releases/latest/download/sysc-launch_linux_amd64":
			_, _ = w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	prefix := filepath.Join(t.TempDir(), ".local")
	if err := installRelease(t.Context(), server.Client(), server.URL+"/releases", "latest", prefix, "linux", "amd64"); err != nil {
		t.Fatalf("installRelease: %v", err)
	}

	installed := filepath.Join(prefix, "bin", "sysc-launch")
	got, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binary) {
		t.Fatalf("installed binary = %q, want %q", got, binary)
	}
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %04o, want 0755", info.Mode().Perm())
	}
}

func TestInstallReleaseChecksumFailurePreservesExistingBinary(t *testing.T) {
	binary := []byte("new binary")
	digest := fmt.Sprintf("%x  sysc-launch_linux_amd64\n", sha256.Sum256([]byte("different binary")))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest/download/SHA256SUMS" {
			fmt.Fprint(w, digest)
			return
		}
		if r.URL.Path == "/releases/latest/download/sysc-launch_linux_amd64" {
			_, _ = w.Write(binary)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	prefix := filepath.Join(t.TempDir(), ".local")
	installed := filepath.Join(prefix, "bin", "sysc-launch")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := installRelease(t.Context(), server.Client(), server.URL+"/releases", "latest", prefix, "linux", "amd64")
	if err == nil {
		t.Fatal("checksum mismatch returned nil")
	}
	got, readErr := os.ReadFile(installed)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old binary" {
		t.Fatalf("existing binary changed to %q", got)
	}
}

func TestInstallReleaseRejectsEmptyBinary(t *testing.T) {
	emptyDigest := fmt.Sprintf("%x  sysc-launch_linux_amd64\n", sha256.Sum256(nil))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest/download/SHA256SUMS" {
			fmt.Fprint(w, emptyDigest)
			return
		}
		if r.URL.Path == "/releases/latest/download/sysc-launch_linux_amd64" {
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	prefix := filepath.Join(t.TempDir(), ".local")
	err := installRelease(t.Context(), server.Client(), server.URL+"/releases", "latest", prefix, "linux", "amd64")
	if err == nil {
		t.Fatal("empty release binary returned nil")
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "sysc-launch")); !os.IsNotExist(err) {
		t.Fatalf("installed binary stat error = %v, want not-exist", err)
	}
}

func TestReleaseAssetURLAllowsTagsAndRejectsPathInput(t *testing.T) {
	got, err := releaseAssetURL("https://example.test/releases", "v1.2.3", "SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://example.test/releases/download/v1.2.3/SHA256SUMS"; got != want {
		t.Fatalf("releaseAssetURL = %q, want %q", got, want)
	}
	if _, err := releaseAssetURL("https://example.test/releases", "v1/../latest", "SHA256SUMS"); err == nil {
		t.Fatal("path-like release tag returned nil")
	}
}

func TestUninstallRemovesOnlyInstalledBinary(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), ".local")
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(binDir, "sysc-launch")
	other := filepath.Join(binDir, "other")
	for path, contents := range map[string]string{installed: "launch", other: "keep"} {
		if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := uninstall(prefix); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatalf("installed binary stat error = %v, want not-exist", err)
	}
	got, err := os.ReadFile(other)
	if err != nil || string(got) != "keep" {
		t.Fatalf("other file = %q, %v; want keep", got, err)
	}
	if err := uninstall(prefix); err != nil {
		t.Fatalf("repeat uninstall: %v", err)
	}
}

func TestValidatePrefixRequiresSafeAbsoluteDirectory(t *testing.T) {
	for _, prefix := range []string{"", "relative", "/"} {
		if err := validatePrefix(prefix); err == nil {
			t.Errorf("validatePrefix(%q) returned nil", prefix)
		}
	}
	if err := validatePrefix(filepath.Join(t.TempDir(), ".local")); err != nil {
		t.Fatalf("valid prefix: %v", err)
	}
}
