package launcher

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestActivateSpawnsOverIPC(t *testing.T) {
	for _, tc := range []struct{ name, workDir, want string }{
		{"direct", "", `{"Action":{"Spawn":{"command":["app"]}}}`},
		{"workdir", "/w", `{"Action":{"Spawn":{"command":["sh","-c","cd \"$1\" && shift && exec \"$@\"","sh","/w","app"]}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := spawnTestSocket(t)
			ln, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			got := make(chan string, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				scanner := bufio.NewScanner(conn)
				if scanner.Scan() {
					got <- scanner.Text()
				}
				conn.Write([]byte("{\"Ok\":\"Handled\"}\n"))
			}()
			svc := NewService(ServiceConfig{
				Scan: func() []Entry { return []Entry{{ID: "app", Argv: []string{"app"}, WorkDir: tc.workDir}} },
				Getenv: func(key string) string {
					if key == "NIRI_SOCKET" {
						return socket
					}
					return ""
				},
			})
			defer svc.Close()
			recvResults(t, svc)
			if err := svc.Activate("app", ""); err != nil {
				t.Fatal(err)
			}
			select {
			case request := <-got:
				if request != tc.want {
					t.Fatalf("request = %s, want %s", request, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("no IPC request")
			}
		})
	}
}

func TestSpawnIPCRejectsFailuresAndCancellation(t *testing.T) {
	for _, reply := range []string{`{"Err":"refused"}`, `{"Ok":"unexpected"}`, `{}`, `garbage`, strings.Repeat("x", 65537)} {
		t.Run(reply[:min(len(reply), 16)], func(t *testing.T) {
			socket := spawnTestSocket(t)
			ln, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				bufio.NewReader(conn).ReadString('\n')
				conn.Write([]byte(reply + "\n"))
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := spawnIPC(ctx, socket, []string{"app"}); err == nil {
				t.Fatal("invalid reply accepted")
			}
		})
	}
	if err := spawnIPC(context.Background(), filepath.Join(t.TempDir(), "missing"), []string{"app"}); err == nil {
		t.Fatal("dial failure accepted")
	}
	socket := spawnTestSocket(t)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		bufio.NewReader(conn).ReadString('\n')
		bufio.NewReader(conn).ReadByte()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- spawnIPC(ctx, socket, []string{"app"}) }()
	<-accepted
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close socket")
	}
}

func spawnTestSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

func TestActivateCustomRunnerOverridesIPC(t *testing.T) {
	runner := &recordRunner{}
	svc := NewService(ServiceConfig{
		Scan: func() []Entry { return []Entry{{ID: "app", Argv: []string{"app"}}} },
		Getenv: func(key string) string {
			if key == "NIRI_SOCKET" {
				return "/nonexistent/test-only.sock"
			}
			return ""
		},
		Run: runner.run,
	})
	defer svc.Close()
	recvResults(t, svc)
	if err := svc.Activate("app", ""); err != nil {
		t.Fatal(err)
	}
	if got := runner.lastArgv(); !slices.Equal(got, []string{"niri", "msg", "action", "spawn", "--", "app"}) {
		t.Fatalf("custom runner argv = %v", got)
	}
}
