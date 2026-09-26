# sill

Status line for the Claude Code CLI, written in Go with no dependencies outside the standard library.

## Commands

```
go test ./...            # all packages; gitinfo tests need git on PATH
gofmt -l . && go vet ./...
go build ./cmd/sill      # local binary
go install ./cmd/sill    # into $GOPATH/bin
./sill demo              # preview the line with the current settings
```

## Layout

- `cmd/sill` is the CLI: render from stdin, `install`, `uninstall`, `settings`, `set`, `demo`.
- `internal/payload` decodes Claude Code's stdin JSON. Schema: search the Claude Code binary for `Pre-calculated: % of context used`.
- `internal/config` is the options table, the layout presets and the settings file `~/.claude/sill.json`.
- `internal/render` builds the line, including width fitting. All formatting lives here.
- `internal/gitinfo` runs the single git call (plus `git status` only when `dirty` is on).
- `internal/transcript` scans the session JSONL for agents, compactions and the start time.
- `internal/term` reads the console width without relying on stdout.
- `internal/install` edits Claude Code's `settings.json`.

## Rules

- Output is plain ASCII plus ANSI colors. No Unicode glyphs, in code or in tests.
- One `git` process per render by default and never `git status` unless `dirty` is on. One sequential pass over the transcript, no JSON decoding of records.
- A segment with nothing to show renders as an empty string. Nothing prints a zero.
- Every option lives in `config.Options`. Adding a segment means: an option there, a case in `render.segment`, a place in both presets, a slot in `render.degradeSteps`, a test. `TestPresetsNameRealSegments` catches a preset that names a non-segment.
- Keep it dependency free. Tests use `t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())` so they never touch the real config, and set `width` explicitly when the output length matters.
