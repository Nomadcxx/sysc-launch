package launcher

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

// spawnIPC sends one Spawn action to a Niri-compatible compositor socket.
func spawnIPC(ctx context.Context, socket string, command []string) error {
	var request bytes.Buffer
	encoder := json.NewEncoder(&request)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string]any{"Action": map[string]any{"Spawn": map[string]any{"command": command}}}); err != nil {
		return err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return fmt.Errorf("launcher: connect compositor: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	n, err := conn.Write(request.Bytes())
	if err != nil {
		return fmt.Errorf("launcher: send spawn: %w", err)
	}
	if n != request.Len() {
		return fmt.Errorf("launcher: send spawn: %w", io.ErrShortWrite)
	}
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("launcher: read spawn reply: %w", err)
		}
		return fmt.Errorf("launcher: compositor returned no spawn reply")
	}
	var reply struct {
		Ok  *string
		Err *string
	}
	if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
		return fmt.Errorf("launcher: decode spawn reply: %w", err)
	}
	if reply.Err != nil {
		return fmt.Errorf("launcher: compositor refused spawn: %s", *reply.Err)
	}
	if reply.Ok == nil || *reply.Ok != "Handled" {
		return fmt.Errorf("launcher: invalid spawn reply")
	}
	return nil
}
