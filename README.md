# sysc-launch

`sysc-launch` is a Go launcher engine and diagnostic CLI for Niri. It is a
presentation-neutral library consumed by
[sysc-shell](https://github.com/Nomadcxx/sysc-shell).

The module discovers desktop applications, ranks queries, and activates entries
through Niri. It does not provide a standalone Wayland UI or daemon; sysc-shell
owns the launcher panel and all presentation.

## Requirements

- Go 1.26.4
- Niri in `PATH` for activation

Desktop scanning and `query` work without Niri.

## Build and install

From a clone of this repository:

```sh
go build -o ./bin/sysc-launch ./cmd/sysc-launch
go install ./cmd/sysc-launch
```

`go install` writes the command to `GOBIN`, or to Go's default binary directory
when `GOBIN` is unset.

## CLI

```text
sysc-launch query [QUERY]
sysc-launch launch DESKTOP_ID [ACTION_ID]
```

`query` writes one JSON array of results to standard output. Each result has an
`Entry` containing the desktop ID, display metadata, expanded argument vector,
terminal flag, and desktop actions, plus its numeric `Score`. With no query it
returns the current application list. A query beginning with `/` uses the
provider-prefix routing; `/apps` selects installed applications.

`launch` activates the identified application or optional desktop action via:

```text
niri msg action spawn -- PROGRAM [ARG...]
```

Arguments come from the desktop entry and are passed without a shell.

## Library

Import the module as:

```go
import launcher "github.com/Nomadcxx/sysc-launch"
```

A service scans immediately. Wait for that initial result before submitting a
query so the response is based on the discovered entries:

```go
package main

import (
	"fmt"
	"log"
	"time"

	launcher "github.com/Nomadcxx/sysc-launch"
)

func main() {
	service := launcher.NewService(launcher.ServiceConfig{
		History: launcher.DefaultHistory(log.Printf),
		Logf:    log.Printf,
	})
	defer service.Close()

	wait := func() ([]launcher.Result, error) {
		select {
		case results := <-service.Results():
			return results, nil
		case <-time.After(5 * time.Second):
			return nil, fmt.Errorf("timed out waiting for launcher results")
		}
	}

	if _, err := wait(); err != nil { // initial XDG scan
		log.Print(err)
		return
	}
	service.Query("terminal")
	results, err := wait()
	if err != nil {
		log.Print(err)
		return
	}
	fmt.Println(results)
}
```

`Query` and `Open` are asynchronous. `Activate` is synchronous and records
usage only after Niri reports a successful spawn. Call `Close` when the service
is no longer needed.

## Discovery and ranking

The scanner reads `.desktop` files from `$XDG_DATA_HOME/applications` and each
`applications` directory under `$XDG_DATA_DIRS`; the standard HOME and system
fallbacks apply when those variables are unset. User entries take precedence.
It excludes entries marked `Hidden` or `NoDisplay`, entries rejected by
`OnlyShowIn` or `NotShowIn`, and entries whose `TryExec` is unavailable.

`Terminal=true` entries use `$TERMINAL` when configured, then try `kitty`,
`foot`, `alacritty`, `wezterm`, and `ghostty`. Such an entry is excluded when
no terminal can be resolved. Valid desktop actions are exposed separately and
can be selected by action ID.

Application names, generic names, keywords, commands, and comments are fuzzy
matched with fzf's ranking algorithm. Recent and repeated successful launches
add a usage boost capped at 25 points, so history influences ranking without
overwhelming a clearly better text match.

## History and privacy

The default history file is:

```text
$XDG_STATE_HOME/sysc-launch/history.gob
```

If `XDG_STATE_HOME` is unset, it falls back to:

```text
$HOME/.local/state/sysc-launch/history.gob
```

History is local gob data written with owner-only file permissions. Successful
launches persist the associated query prefixes and desktop IDs, so the file can
reveal application-launch habits. Delete it to reset ranking history.

sysc-shell deliberately opens its legacy independent path,
`$XDG_STATE_HOME/sysc-shell/launcher/history.gob` (with the corresponding
`$HOME/.local/state` fallback), instead of this module's default. The two
applications therefore do not merge history.

## Checks

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build ./...
```

## License

GPL-3.0-only. See [LICENSE](LICENSE).
