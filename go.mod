module github.com/Zen1th53/marshal

go 1.26.11

require (
	github.com/google/uuid v1.6.0
	github.com/rivo/uniseg v0.4.7
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/sys v0.49.0
	golang.org/x/term v0.45.0
	modernc.org/libc v1.80.1
	modernc.org/sqlite v1.62.0
)

require (
	github.com/Zen1th53/code-evidence v0.0.0
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/Zen1th53/code-evidence => ./third_party/code-evidence
