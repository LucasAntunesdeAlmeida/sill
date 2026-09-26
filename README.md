# sill

A status line for the [Claude Code](https://claude.com/claude-code) CLI. One binary, no runtime,
plain ASCII, renders in a few milliseconds, and never wraps.

```
ctx 61%  5h 94% (1h20)  7d 72% (2d5h)  agents 2 | ~/.../utils/sill  feature/billing MERGING  wt  #42 + | Fable 5.1 / high / billing-fix
```

A sill is the ledge at the bottom of a window. This one sits at the bottom of the context window.

## Install

With Go 1.24 or newer:

```
go install github.com/LucasAntunesdeAlmeida/sill/cmd/sill@latest
sill install
```

Or download a binary from the [releases page](https://github.com/LucasAntunesdeAlmeida/sill/releases),
put it somewhere on your PATH, and run `sill install`.

`sill install` writes the `statusLine` entry into `~/.claude/settings.json` (backed up first, other
settings untouched) pointing at the binary where it is. Restart Claude Code. The binary needs to be on
your PATH only so that `sill set` is easy to type; `go install` puts it in `$GOPATH/bin`, which usually is.
It honors `CLAUDE_CONFIG_DIR` if you use one. `sill uninstall` removes the entry again.

Requirements: `git` on PATH for the branch segment (everything else works without it). On Windows,
Claude Code runs status line commands through Git Bash, so Git for Windows is needed anyway.

## What it shows

Segments only appear when they have something to say, so a fresh session in a plain folder shows just
the path and the model. Labels and separators are gray; only budgets change color.

| Key | Default | Shows | Source |
|---|---|---|---|
| `ctx` | on | `ctx 61%`, yellow at 60, red at 80 | payload |
| `limits` | on | `5h 94% (1h20)`, `7d`, `spend`; yellow at 70, red at 90. From yellow on, the reset time follows | payload |
| `cache` | off | `cache 42m` while the prompt cache is warm, `cache cold` after | payload |
| `agents` | on | `agents 2`, background agents still running | transcript |
| `compactions` | on | `compact 1`, how many times the context was compacted | transcript |
| `duration` | off | `up 2h15`, time since the session started | transcript |
| `path` | on | `~/.../utils/sill`: home as `~`, anchor plus the last two folders | payload |
| `git` | on | branch, or short sha when detached, then `MERGING`, `REBASING`, `CHERRY-PICKING`, `REVERTING` or `BISECTING` in red | one `git` call |
| `dirty` | off | `*` after the branch when tracked files have uncommitted changes. Costs a second `git` call | `git status` |
| `worktree` | on | `wt`, or `wt:name` when the worktree is not named like the branch | payload |
| `pr` | on | `#42 +` approved, `#42 x` changes requested, `#42 draft`; `!42` for a GitLab MR | payload |
| `model` | on | `Fable 5.1` | payload |
| `effort` | on | `high`, from `/effort` | payload |
| `session` | on | the name from `/rename` | payload |
| `version` | off | `v2.1.282`, the Claude Code version | payload |

Four more options shape the line rather than add to it:

| Key | Values | Meaning |
|---|---|---|
| `layout` | `compact` (default), `full`, `custom` | one line, three lines, or your own |
| `reset` | `relative` (default), `absolute`, `both` | `(1h20)`, `(@ 14:30)`, `(1h20 @ 14:30)` |
| `color` | `on` (default), `off` | ANSI colors. `NO_COLOR` in the environment also turns them off |
| `width` | `0` (default), a number | columns to fit into. `0` asks the terminal |

## Layouts

**compact** is one line: budgets first so they never clip, the path in the middle as the only elastic
part, the model anchoring the right.

```
ctx 61%  5h 94% (1h20)  7d 72% (2d5h)  agents 2  compact 1 | ~/.../utils/sill  feature/billing MERGING  wt  #42 + | Fable 5.1 / high / billing-fix
```

**full** is three lines and uses the room: the path is not shortened, `ctx` shows token counts, and
every limit shows its reset time. Shown here with `cache`, `version` and `duration` turned on.

```
~/source/repos/utils/sill  feature/billing MERGING  wt  #42 +
ctx 61% (122k/200k)  5h 94% (1h20 @ 14:30)  7d 72% (2d5h @ Sun 09:00)  cache 42m
Fable 5.1 / high / billing-fix  agents 2  compact 1  v2.1.282
```

**custom** takes a `lines` list from the settings file. Each line is segment keys in order; `|` puts a
separator bar, `/` joins its neighbours with a slash. A layout with more than one line gets the full
treatment (long path, token counts, all reset times).

```json
{
  "layout": "custom",
  "lines": [
    "path git worktree pr | model / effort / session",
    "ctx limits cache agents compactions duration"
  ]
}
```

## Fitting the terminal

sill asks the console for its width (the Windows console API or `/dev/tty`, since stdout is a pipe to
Claude Code) and, when a line would overflow, gives up detail in a fixed order instead of wrapping:
the path shrinks to `~/.../sill` and then to `sill`, limits lose their reset times, then segments drop
one by one: `version`, `duration`, `session`, `compactions`, `agents`, `cache`, `effort`, `worktree`,
`pr`, `git`, `path`. Budgets and the model go last. `sill settings` prints the detected width; if it
says unknown, set `width` yourself.

## Commands

```
sill                    render; Claude Code pipes its JSON on stdin, you never run this by hand
sill install            point settings.json at this binary
sill uninstall          remove the statusLine entry again
sill settings           list every option with its current value and the detected width
sill set <key> <value>  change options, several pairs at once
sill demo               render a sample payload with the current settings
```

```
sill set layout full
sill set cache on version off
sill set reset both
sill set width 120
```

Inside a Claude Code session, prefix the command with `!` to run it without a model turn:

```
! sill set layout full
```

The line picks up the change on its next refresh. Settings live in `~/.claude/sill.json` and the file
only contains what you changed, so `sill settings` is the place to see everything.

## How it works

Claude Code runs the command on every update and pipes a JSON payload on stdin. sill reads the fields
above from it, makes one `git rev-parse` call for the branch and reads git's marker files for the
operation in progress. It never runs `git status` unless you turn `dirty` on, so large repos stay fast.

Agents, compactions and the session start come from the session transcript, whose path is in the
payload. sill scans it once per render looking for byte markers and never decodes a record: the
compaction boundary, `Agent` tool calls, the task notification that names a finished agent, and the
first record's timestamp. A 17 MB transcript adds about 30 ms. With `agents` and `compactions` off,
only the head of the file is read for the start time.

The full payload schema is embedded in the Claude Code binary; search it for
`Pre-calculated: % of context used`.

## Development

```
go test ./...
gofmt -l . && go vet ./...
go build ./cmd/sill && ./sill demo
```

Standard library only. Layout is `cmd/sill` for the CLI and `internal/` for one package per concern:
`payload`, `config`, `render`, `gitinfo`, `transcript`, `term`, `install`. CI tests on Linux, macOS and
Windows with the latest Go and with the minimum in `go.mod`. Tags `v*` build release binaries for six
targets through GitHub Actions.

## License

MIT
