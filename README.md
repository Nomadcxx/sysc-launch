<p align="center">
  <img src="assets/header.webp" alt="sysc-launch" height="64">
</p>

<p align="center"><strong>Standalone Linux application launcher engine and JSON CLI in Go</strong></p>

`sysc-launch` reads XDG desktop entries, ranks application queries, and starts
the selected desktop command. It ships as a Go library and a standalone CLI,
with no dependency on sysc-shell or another sysc* component. Embedding
applications provide their own interface; sysc-shell is one optional consumer.

<details>
<summary>Preview</summary>

<p align="center"><img src="assets/launcher-tour.webp" alt="Animated preview of the sysc-shell launcher searching applications and browsing the full list" width="380" loading="lazy"></p>
<p align="center"><sub>Example interface from sysc-shell. sysc-launch provides the engine and CLI.</sub></p>
</details>

## Quick Links

- [Installation](#installation)
- [Usage](#usage)
- [Library](#library)
- [Providers](#providers)
- [Platform support](#platform-and-compositor-support)
- [Development](#development)
- [Contributing](CONTRIBUTING.md)
- [GitHub releases](https://github.com/Nomadcxx/sysc-launch/releases)
- [Go API](https://pkg.go.dev/github.com/Nomadcxx/sysc-launch)
- [Arch package](https://aur.archlinux.org/packages/sysc-launch)
- [Report an issue](https://github.com/Nomadcxx/sysc-launch/issues)

## Features

- Scan XDG application directories and honor desktop-entry visibility rules.
- Rank names, commands, keywords, comments, and categories with fuzzy search.
- Launch applications and desktop actions with parsed arguments and working directories.
- Add query providers in Go or adapt a command with `CommandProvider`.
- Keep local usage history to improve ranking across sessions.

## Platform and compositor support

`sysc-launch` uses desktop entries and does not connect to a compositor. Its
query and discovery paths need no graphical session. To start graphical
applications, run the CLI or embedding application from the target desktop
session so launched processes inherit its environment. This works across
Wayland and X11 sessions, including Niri, Sway, Hyprland, GNOME, and KDE Plasma.

| Install route | Current availability |
| --- | --- |
| GitHub release | Linux `amd64` and `arm64`; installs to a user-local prefix |
| AUR | Arch Linux and derivatives, `x86_64` and `aarch64` |
| Debian/Ubuntu, Fedora/openSUSE, NixOS | No project `.deb`, RPM, or NixOS package yet; use the release installer or Go install |

`sysc-launch` supplies an engine and CLI. It does not draw a panel or run a
background daemon; an embedding application supplies the interface.

## Requirements

- Linux; release binaries are provided for `amd64` and `arm64`
- Go 1.26.10 or newer to build from source
- XDG desktop entries to list applications
- A terminal emulator to launch entries marked `Terminal=true`; set
  `$TERMINAL` or install one of the supported terminals listed under
  [discovery](#discovery-and-ranking)

## Installation

### GitHub release installer

Download the installer for your architecture and run it as your desktop user.
It installs a user-local binary and does not need root access or a Go runtime.

The release installer assets start with `v0.5.0` and target Linux `amd64` and
`arm64`:

```sh
set -eu
machine="$(uname -m)"
case "$machine" in
  x86_64) goarch=amd64 ;;
  aarch64|arm64) goarch=arm64 ;;
  *) printf 'Unsupported architecture: %s\n' "$machine" >&2; exit 1 ;;
esac
curl -fsSL -o sysc-launch-installer \
  "https://github.com/Nomadcxx/sysc-launch/releases/latest/download/sysc-launch-installer_linux_${goarch}"
chmod +x ./sysc-launch-installer
./sysc-launch-installer
```

The installer downloads the launcher and `SHA256SUMS`, verifies the launcher's
SHA-256 digest, and installs it to `$HOME/.local/bin/sysc-launch`. Use
`--prefix DIR` to choose another absolute install prefix, `--version TAG` to
install a specific release, or `--uninstall` to remove the installed binary.

Add `$HOME/.local/bin` to `PATH` if it is not there already.

```sh
./sysc-launch-installer --version v0.5.0
./sysc-launch-installer --prefix "$HOME/.local" --uninstall
```

Each release includes the launcher and installer binaries for Linux `amd64`
and `arm64`, plus `SHA256SUMS`.

### Go

Build and install the CLI from a clone of this repository:

```sh
git clone https://github.com/Nomadcxx/sysc-launch.git
cd sysc-launch
go install ./cmd/sysc-launch
```

`go install` writes to `$GOBIN`, or Go's default binary directory when
`GOBIN` is unset. To install the latest tagged module release without cloning:

```sh
go install github.com/Nomadcxx/sysc-launch/cmd/sysc-launch@latest
```

The repository includes a Go installer for release binaries. From a clone, run
it to install the latest release or select a tag:

```sh
go run ./cmd/installer
go run ./cmd/installer --version v0.5.0
```

The installer downloads the selected release, verifies its checksum, and
places the CLI in the configured prefix. This route requires Go 1.26.10 or
newer; the release-binary installer does not require Go.

### AUR

On Arch Linux or an Arch-based distribution, install the `sysc-launch` package
for `x86_64` or `aarch64` with an AUR helper:

```sh
yay -S sysc-launch
# or
paru -S sysc-launch
```

The package installs the standalone CLI. Applications that import the Go
library do not need the AUR package.

## Usage

```text
sysc-launch query [QUERY]
sysc-launch launch DESKTOP_ID [ACTION_ID]
```

Search for applications and copy a desktop ID from the JSON output:

```sh
sysc-launch query terminal
sysc-launch query /apps
sysc-launch launch DESKTOP_ID
```

`query` writes one JSON array to standard output. Each result contains an
`Entry` with the desktop ID, display metadata, expanded argument vector,
terminal flag, and desktop actions, plus a numeric `Score` and an `Action` ID
when the row represents a desktop action. With no query, it returns the
application list. A query beginning with `/` selects a provider route; `/apps`
selects installed applications, and `/` lists the providers.

`launch` starts the identified application or desktop action with the expanded
desktop-entry argument vector. The launcher passes each argument to the
process and never evaluates a shell command. An entry with `Path=` starts in
that working directory. The application runs in its own session; `launch`
returns after the operating system starts the process, without waiting for its
window or initialization to finish.

## Library

Import the module as:

```go
import launcher "github.com/Nomadcxx/sysc-launch"
```

`NewService` starts an asynchronous XDG scan. Read its first result before
submitting a query:

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
		case batch, ok := <-service.Results():
			if !ok {
				return nil, fmt.Errorf("service closed")
			}
			return batch.Results, nil
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

`Query` and `Open` are asynchronous. `Activate` waits for the operating system
to start the application before it records usage. Call `Close` when your
application stops using the service.

`Query` returns a generation number. `ResultBatch.Gen` lets an asynchronous UI
discard a result from an older query. The default activation path starts the
desktop command. Set `ServiceConfig.Run` to supply a custom launch policy.

## Providers

Applications is the default provider, reached by bare text or `/apps`. Append
more through `ServiceConfig.Providers`:

```go
launcher.Provider{
	Name:        "Calculator",
	Prefix:      "/calc",
	Glyph:       "glyph:calculate",
	Description: "Arithmetic",
	Query:       func(query string) []launcher.Result { /* ... */ },
	Activate:    func(query, id, action string) error { /* ... */ },
	Inline:      true,
}
```

- `Prefix` must start with `/` and be unique; `/apps` is taken. A provider
  with a bad or repeated prefix, or no `Query`, is logged through `Logf` and
  skipped.
- `/<prefix> text` sends `text` to that provider. A bare `/` (or an unknown
  prefix) lists the registered providers, Applications first;
  `ApplicationsGlyph` sets its glyph.
- An `Inline` provider is also asked about bare text, and its rows are placed
  above the application rows.
- Each row belongs to the provider that produced it in the last published
  result set. `Activate(id, action)` hands the row to that provider's
  `Activate` with the routed query; a provider without one, or a row that is
  no longer published, takes the spawn path. `Result.Action` names a desktop
  action of `Result.Entry`. Successful activations are recorded in history
  whichever provider ran them.
- Provider functions run on the service goroutine and block queries and
  activation while they run. They must not call back into the `Service`.

`CommandProvider` runs a configured argv command for each query and parses
stdout as one result per line. A line contains `label` or `label<TAB>comment`.
The helper replaces `{query}` inside argv tokens and never evaluates a shell
command:

```go
launcher.CommandProvider(launcher.CommandProviderConfig{
	Name:    "Bookmarks",
	Prefix:  "/bookmarks",
	Command: []string{"bookmark-search", "--query={query}"},
	OnSelect: func(query, selection string) error {
		return activateBookmark(selection)
	},
})
```

Each command has a two-second deadline. The helper runs it in a process group
and kills that group on cancellation. After the main process exits, it waits up
to 150 ms for open pipes, then kills any remaining group processes. It caps
stdout at 256 KiB, rows at 4 KiB, and results at 200. It returns a visible error
result for process failures or malformed output. `OnSelect` receives the query
with the provider prefix removed and the selected full line. Leave `OnSelect`
nil when activation should report an error.

## Discovery and ranking

The scanner reads `.desktop` files from `$XDG_DATA_HOME/applications` and each
`applications` directory under `$XDG_DATA_DIRS`; the standard HOME and system
fallbacks apply when those variables are unset. User entries take precedence.
It excludes entries marked `Hidden` or `NoDisplay`, entries rejected by
`OnlyShowIn` or `NotShowIn`, and entries whose `TryExec` is unavailable.

`Terminal=true` entries use `$TERMINAL` when configured (the value may
include arguments), then try `kitty`, `foot`, `alacritty`, `wezterm`,
`ghostty`, `konsole`, and `gnome-terminal`. The terminal-specific execution
arguments stay intact. The scanner skips such entries when no terminal is
available. The scanner exposes valid desktop actions as separate rows that you
can select by action ID.

Application names, generic names, keywords, commands, comments, and categories
use fzf's ranking algorithm. Recent and repeated successful launches add a
usage boost capped at 25 points. This gives familiar apps a lift while text
matches still determine the result order.

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

Embedding applications can choose another history file with `OpenHistory`.
sysc-shell keeps its legacy path at
`$XDG_STATE_HOME/sysc-shell/launcher/history.gob` (or the matching
`$HOME/.local/state` path), so its history stays separate from the default.

## Development

```sh
test -z "$(gofmt -l .)"
go vet -p 2 ./...
go test -race -count=1 -p 2 ./...
go build -p 2 ./...
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the commit gate and development
workflow.

## License

BSD-3-Clause. See [LICENSE](LICENSE).

Releases up to v0.3.1 were published under GPL-3.0-only. The change to
BSD-3-Clause followed the removal of a file that had been derived from another
project's GPL-3 code; no such code remains in this module.
