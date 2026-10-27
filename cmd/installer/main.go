package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	releaseBaseURL = "https://github.com/Nomadcxx/sysc-launch/releases"
	maxBinarySize  = 64 << 20
	maxSumsSize    = 1 << 20
)

type options struct {
	prefix    string
	version   string
	uninstall bool
}

func parseArgs(args []string, stderr io.Writer) (options, error) {
	var opts options
	fs := flag.NewFlagSet("sysc-launch-installer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.prefix, "prefix", "", "install prefix (default $HOME/.local)")
	fs.StringVar(&opts.version, "version", "latest", "release tag to install (default latest)")
	fs.BoolVar(&opts.uninstall, "uninstall", false, "remove sysc-launch from the prefix")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: sysc-launch-installer [--prefix DIR] [--version latest|TAG] [--uninstall]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return opts, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return opts, nil
}

func run(args []string, stdout, stderr io.Writer, client *http.Client) int {
	opts, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if os.Geteuid() == 0 {
		fmt.Fprintln(stderr, "sysc-launch-installer: run as your desktop user; sudo is not needed")
		return 2
	}
	if opts.prefix == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, "sysc-launch-installer:", err)
			return 1
		}
		opts.prefix = filepath.Join(home, ".local")
	}
	if err := validatePrefix(opts.prefix); err != nil {
		fmt.Fprintln(stderr, "sysc-launch-installer:", err)
		return 2
	}
	if opts.uninstall {
		if err := uninstall(opts.prefix); err != nil {
			fmt.Fprintln(stderr, "sysc-launch-installer:", err)
			return 1
		}
		fmt.Fprintf(stdout, "sysc-launch removed from %s\n", filepath.Join(opts.prefix, "bin"))
		return 0
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := installRelease(ctx, client, releaseBaseURL, opts.version, opts.prefix, runtime.GOOS, runtime.GOARCH); err != nil {
		fmt.Fprintln(stderr, "sysc-launch-installer:", err)
		return 1
	}
	fmt.Fprintf(stdout, "sysc-launch installed to %s\n", filepath.Join(opts.prefix, "bin", "sysc-launch"))
	return 0
}

func installRelease(ctx context.Context, client *http.Client, baseURL, version, prefix, goos, goarch string) error {
	if err := validatePrefix(prefix); err != nil {
		return err
	}
	if goos != "linux" || (goarch != "amd64" && goarch != "arm64") {
		return fmt.Errorf("unsupported target %s/%s (supported: linux/amd64, linux/arm64)", goos, goarch)
	}
	asset := fmt.Sprintf("sysc-launch_%s_%s", goos, goarch)
	sumsURL, err := releaseAssetURL(baseURL, version, "SHA256SUMS")
	if err != nil {
		return err
	}
	binaryURL, err := releaseAssetURL(baseURL, version, asset)
	if err != nil {
		return err
	}
	sums, err := readAsset(ctx, client, sumsURL, maxSumsSize)
	if err != nil {
		return fmt.Errorf("download SHA256SUMS: %w", err)
	}
	expected, err := checksumFor(sums, asset)
	if err != nil {
		return err
	}
	binary, err := readAsset(ctx, client, binaryURL, maxBinarySize)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	if len(binary) == 0 {
		return fmt.Errorf("release asset %s is empty", asset)
	}
	actual := sha256.Sum256(binary)
	if !bytes.Equal(actual[:], expected) {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	// ponytail: releases ship one executable; add safe archive extraction if the install needs more files.
	return installBinary(prefix, binary)
}

func releaseAssetURL(baseURL, version, asset string) (string, error) {
	var suffix string
	if version == "latest" {
		suffix = "/latest/download/"
	} else {
		if !validReleaseTag(version) {
			return "", fmt.Errorf("invalid release tag %q", version)
		}
		suffix = "/download/" + url.PathEscape(version) + "/"
	}
	return strings.TrimRight(baseURL, "/") + suffix + asset, nil
}

func validReleaseTag(tag string) bool {
	if len(tag) < 2 || tag[0] != 'v' {
		return false
	}
	for _, r := range tag[1:] {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			continue
		}
		return false
	}
	return true
}

func readAsset(ctx context.Context, client *http.Client, assetURL string, maxSize int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected response: %s", resp.Status)
	}
	if resp.ContentLength > maxSize {
		return nil, fmt.Errorf("asset exceeds %d bytes", maxSize)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("asset exceeds %d bytes", maxSize)
	}
	return data, nil
}

func checksumFor(sums []byte, asset string) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	var found []byte
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != asset {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("duplicate checksum for %s", asset)
		}
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("invalid SHA-256 checksum for %s", asset)
		}
		found = decoded
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("SHA256SUMS has no checksum for %s", asset)
	}
	return found, nil
}

func installBinary(prefix string, binary []byte) error {
	if err := validatePrefix(prefix); err != nil {
		return err
	}
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(binDir, ".sysc-launch-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(binary); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o755); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, filepath.Join(binDir, "sysc-launch"))
}

func uninstall(prefix string) error {
	if err := validatePrefix(prefix); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(prefix, "bin", "sysc-launch"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func validatePrefix(prefix string) error {
	if prefix == "" || !filepath.IsAbs(prefix) {
		return errors.New("prefix must be an absolute directory")
	}
	if filepath.Clean(prefix) == string(filepath.Separator) {
		return errors.New("prefix cannot be the filesystem root")
	}
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))
}
