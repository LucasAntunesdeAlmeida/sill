# sill

Status line for the Claude Code CLI, written in Go with no dependencies outside the standard library.

## Commands

```
go test ./...            # all packages; gitinfo tests need git on PATH
gofmt -l . && go vet ./...
go build ./cmd/sill      # local binary
go install ./cmd/sill    # into $GOPATH/bin
./sill demo              # preview the line with the current settings
./sill doctor            # check the setup against the real ~/.claude
make bench               # benchmarks; make fuzz runs every fuzz target for FUZZTIME
```

## Layout

- `cmd/sill` is the CLI: render from stdin, `install`, `uninstall`, `settings`, `set`, `unset`, `demo`, `doctor` (in `doctor.go`), `hook` and the sweep of unrecorded sessions (`hook.go`), `cost` (`cost.go`).
- `internal/payload` decodes Claude Code's stdin JSON. Schema: search the Claude Code binary for `Pre-calculated: % of context used`.
- `internal/config` is the options table, the layout presets and the settings file `~/.claude/sill.json`. `Check` reports what `Parse` ignores.
- `internal/render` builds the line, including width fitting in terminal columns. All formatting lives here.
- `internal/gitinfo` reads the branch from `.git/HEAD` and falls back to one git call; `git status` only when `dirty` is on.
- `internal/transcript` scans the session JSONL for agents, compactions, the start time and each response's usage, incrementally, with its state in the user cache dir. Subagents in `<session>/subagents/` count for cost, in the same state file. `Inspect` is the format-drift check for `doctor`.
- `internal/cost` prices tokens from `prices.json`, a dated table of list prices per million tokens. `pricegen` refreshes it from LiteLLM; the `prices` workflow runs it weekly and opens a PR.
- `internal/ledger` is the cost ledger, `~/.claude/sill-costs/YYYY-MM.jsonl`, one line per session, latest line wins.
- `internal/term` reads the console width without relying on stdout.
- `internal/install` edits Claude Code's `settings.json`: the `statusLine` and the `SessionEnd` hook, in one edit with one backup.
- `internal/atomicfile` writes a file through a temp file and a rename. Every file sill writes goes through it, except the ledger, which is appended to so sessions ending together do not lose a line.

## Rules

- Output is plain ASCII plus ANSI colors. No Unicode glyphs in code or tests; test non-ASCII input with `\u` escapes.
- Every string from the payload or the repository goes through `render.Clean` before it is printed.
- No git process per render in the common case, at most one by default, and never `git status` unless `dirty` is on.
- The transcript is read once, incrementally: no JSON decoding of records, only byte markers. Change `cacheVersion` in `transcript/cache.go` whenever what the scan counts changes. A new price table invalidates the state on its own (`cost.TableVersion`).
- A response counts once per session: its records repeat the usage and forks copy their parent's history. Check a change to the usage scan against a full decode of real transcripts, not only the fixtures.
- Price rows are never edited or removed; a change is a new row with the date it took effect. A model without a price shows tokens, never `$0`. Costs are API list prices and the output says so.
- A render stays within `budget` (cmd/sill/main.go): anything slow takes a context and gives up when it ends.
- A render never fails silently: errors become the line `sill: ...` and `last-error.txt`, exit 0. The hook never fails Claude Code either: exit 0, errors to `last-error.txt`.
- A segment with nothing to show renders as an empty string. Nothing prints a zero.
- Every option lives in `config.Options`. Adding a segment means: an option there, a case in `render.segment`, a place in both presets, a slot in `render.degradeSteps`, a test. A boolean option that is not a segment goes in `config.modifiers`. `TestPresetsNameRealSegments` catches a preset that names a non-segment.
- Keep it dependency free. Tests use `t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())` and, when a render may touch the cache, `t.Setenv("SILL_CACHE_DIR", t.TempDir())`, so they never touch the real config; set `width` explicitly when the output length matters.
- CI actions are pinned to commit SHAs with the version as a comment.
