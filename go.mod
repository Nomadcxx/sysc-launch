module github.com/Nomadcxx/sysc-launch

go 1.26.10

require (
	// hold: v0.2.0 declares go 1.27.1, and the toolchain floor is an
	// organisation-wide decision (see sysc-2015)
	github.com/go-freedesktop/desktopentry v0.1.0
	github.com/junegunn/fzf v0.74.5
	github.com/junegunn/go-shellwords v0.0.0-20250127100254-2aa3b3277741
	github.com/rkoesters/xdg v0.0.1
)

require (
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/sys v0.49.0 // indirect
)
