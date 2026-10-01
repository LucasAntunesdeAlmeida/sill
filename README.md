# sill

sill is a status line for the [Claude Code](https://claude.com/claude-code) CLI: a single binary with
no runtime to install, plain ASCII output, a render time of a few milliseconds, and a line that never
wraps.

```
ctx 61%  5h 94% (1h20)  7d 72% (2d5h)  agents 2 | ~/.../utils/sill  feature/billing MERGING  wt  #42 + | Fable 5.1 / high / billing-fix
```

The name comes from the window sill, the ledge under a window. This one sits under the context window.

## Getting started

You need Go 1.24 or later:

```
go install github.com/LucasAntunesdeAlmeida/sill/cmd/sill@latest
sill install
```

If you would rather not build it, grab a binary from the
[releases page](https://github.com/LucasAntunesdeAlmeida/sill/releases), place it on your PATH and run
`sill install`. Every release binary comes with a build provenance attestation, which proves it was
produced by this repository's release workflow:

```
gh attestation verify sill-linux-amd64 --repo LucasAntunesdeAlmeida/sill
```

The macOS builds are unsigned, so a copy downloaded with a browser gets quarantined. Lift that with
`xattr -d com.apple.quarantine sill-darwin-arm64`.

### What `sill install` does

It adds two entries to `~/.claude/settings.json`, after saving a backup as `settings.json.sill.bak`:
a `statusLine` that runs the binary from wherever it lives, and a `SessionEnd` hook that records the
cost of each session (see [Costs](#costs)). Nothing else in the file changes. Restart Claude Code to
see the line. `CLAUDE_CONFIG_DIR` is respected, and `sill uninstall` takes both entries out again.

Keeping the binary on your PATH only matters for typing `sill set` comfortably; `go install` puts it
in `$GOPATH/bin`, which normally is on the PATH already.

Most repositories need nothing else, because the branch comes straight from `.git/HEAD`. `git` on the
PATH is only used by the `dirty` option and for the few repositories whose HEAD file is not enough (see
[Internals](#internals)). On Windows, Claude Code runs status line commands through Git Bash, so Git for
Windows is installed anyway.

Something blank, wrong, or starting with `sill: ...`? Run `sill doctor`.

## Segments

A segment is shown only when it has something to report: a new session in a folder that is not a
repository shows nothing but the path and the model. Labels and separators stay gray, and color is
reserved for budgets.

| Key | Default | Example and meaning | From |
|---|---|---|---|
| `ctx` | on | `ctx 61%`; yellow from 60, red from 80 | payload |
| `limits` | on | `5h 94% (1h20)`, `7d`, `spend`; yellow from 70, red from 90, and the reset time appears once yellow | payload |
| `cache` | off | `cache 42m` while the prompt cache is warm, then `cache cold` | payload |
| `cost` | off | `cost $4.12` for the session at API list prices, subagents included; `+` if some model had no price, `tok 1.2m` if no model had one | transcript |
| `agents` | on | `agents 2`, background agents that have not finished | transcript |
| `compactions` | on | `compact 1`, times the context was compacted | transcript |
| `duration` | off | `up 2h15` since the session started | transcript |
| `repo` | off | `acme/storefront`, the owner and name of the origin remote; `sill set repo on path off` shows it in place of the folder | payload |
| `path` | on | `~/.../utils/sill`: home becomes `~`, then the anchor and the last two folders | payload |
| `git` | on | the branch, or a short sha when detached, followed in red by `MERGING`, `REBASING`, `CHERRY-PICKING`, `REVERTING` or `BISECTING` | `.git/HEAD` |
| `dirty` | off | `*` after the branch if tracked files have uncommitted changes; costs one more `git` call | `git status` |
| `worktree` | on | `wt`, or `wt:name` if the worktree and the branch are named differently | payload |
| `pr` | on | `#42 +` approved, `#42 x` changes requested, `#42 draft`; a GitLab MR shows as `!42` | payload |
| `model` | on | `Fable 5.1` | payload |
| `effort` | on | `high`, as set by `/effort` | payload |
| `session` | on | the session name given with `/rename` | payload |
| `style` | on | `style Learning`, the output style from `/output-style`, when it is not the default | payload |
| `version` | off | `v2.1.282`, the Claude Code version | payload |

## Options

Four settings change how the line is drawn instead of what goes on it:

| Key | Values | Effect |
|---|---|---|
| `layout` | `compact` (default), `full`, `custom` | one line, three lines, or lines you define |
| `reset` | `relative` (default), `absolute`, `both` | `(1h20)`, `(@ 14:30)` or `(1h20 @ 14:30)` |
| `color` | `on` (default), `off` | ANSI colors; setting `NO_COLOR` in the environment disables them too |
| `width` | `0` (default) or a number | the columns to fit in; `0` means ask the terminal |

### Layouts

`compact` fits everything on one line. Budgets come first so they are never cut off, the path sits in
the middle and is the only part that stretches or shrinks, and the model closes the line on the right.

```
ctx 61%  5h 94% (1h20)  7d 72% (2d5h)  agents 2  compact 1 | ~/.../utils/sill  feature/billing MERGING  wt  #42 + | Fable 5.1 / high / billing-fix
```

`full` spreads over three lines and spends the extra room on detail: the whole path, token counts next
to `ctx`, and a reset time on every limit. The example also has `cache`, `version` and `duration` on.

```
~/source/repos/utils/sill  feature/billing MERGING  wt  #42 +
ctx 61% (122k/200k)  5h 94% (1h20 @ 14:30)  7d 72% (2d5h @ Sun 09:00)  cache 42m
Fable 5.1 / high / billing-fix  agents 2  compact 1  v2.1.282
```

`custom` reads a `lines` list from the settings file. Each entry lists segment keys in order; a `|`
draws a separator bar and a `/` joins the segments on either side with a slash. As soon as there is
more than one line, the layout is treated like `full`: long path, token counts, every reset time.

```json
{
  "layout": "custom",
  "lines": [
    "path git worktree pr | model / effort / session",
    "ctx limits cache agents compactions duration"
  ]
}
```

### Fitting the width

stdout is a pipe to Claude Code, so sill asks the console directly for its width, through the Windows
console API or `/dev/tty`. When a line is too long it drops detail in a fixed order rather than wrap.
First the path shortens to `~/.../sill`, then to `sill`; then limits lose their reset times; then
whole segments go, in this order: `version`, `duration`, `style`, `session`, `compactions`, `agents`, `cache`,
`cost`, `effort`, `worktree`, `pr`, `repo`, `git`, `path`. The budgets and the model are the last to go.

Width is measured in terminal columns, so Chinese, Japanese or Korean characters and emoji in a folder
or session name take two columns each. `sill settings` shows the width sill detected; if it reports
unknown, set `width` by hand.

## Commands

```
sill                    render; Claude Code pipes its JSON on stdin, you never run this by hand
sill install            point settings.json at this binary, with the cost hook
sill uninstall          remove the statusLine entry and the hook again
sill settings           list every option with its current value and the detected width
sill set <key> <value>  change options, several pairs at once
sill unset <key>...     back to the default; `sill unset lines` drops custom lines
sill demo               render a sample payload with the current settings
sill cost [folder]      what sessions cost, per repository or for one repository
sill doctor             check the setup and what a render sees
sill hook               record session costs; Claude Code runs it as a session ends
sill completion <shell> print Tab completion for bash, zsh, fish or powershell
```

### Changing settings

```
sill set layout full
sill set cache on version off
sill set reset both
sill set width 120
sill unset width
```

From inside a Claude Code session, put `!` in front to run the command without spending a model turn:

```
! sill set layout full
```

The next refresh of the line picks the change up. Settings are stored in `~/.claude/sill.json`, which
holds only the values you changed; `sill settings` shows the complete picture.

### Diagnosing with `sill doctor`

Each check gets one line. It covers: whether `settings.json` runs this binary and has the cost hook;
problems in `sill.json`, such as unknown keys, invalid values or names in `lines` that are not
segments; how the branch is read and how long that takes; the terminal width; the newest transcript and
how long it takes to scan; the cost ledger, including any model without a price; and the last render
that failed. It also compares the transcript markers sill depends on against a looser search, so a
Claude Code update that changes the transcript format is reported instead of silently showing no agents
or a cost that is too low.

```
ok    sill.json is valid, 2 option(s) changed
ok    settings.json runs this binary
ok    a SessionEnd hook records session costs
ok    git: branch main read from .git/HEAD in 0.4 ms, no git process
ok    terminal width 140 columns
ok    latest transcript: 1532 records, 0 agent(s) running, 1 compaction(s), 498 response(s)
      C:/Users/me/.claude/projects/.../9a1e.jsonl (19.8 MB)
      full scan 64.2 ms, a render with nothing new 1.3 ms
ok    cost ledger: 57 session(s), $412 at list prices, 1 file(s), 27.5 KB
      C:/Users/me/.claude/sill-costs
ok    no failed render recorded
```

### Tab completion

`sill completion` prints a script that completes commands, option names and option values: `sill set
la<Tab>` becomes `layout`, and `sill set layout <Tab>` offers `compact custom full`. Load it from your
shell's startup file:

```
# PowerShell, in $PROFILE
sill completion powershell | Out-String | Invoke-Expression

# bash, in ~/.bashrc
eval "$(sill completion bash)"

# zsh, in ~/.zshrc after compinit
source <(sill completion zsh)

# fish
sill completion fish > ~/.config/fish/completions/sill.fish
```

The candidates come from the binary itself, so new options complete without regenerating the script.
Completion works in a terminal, not in the `!` prompt inside Claude Code.

## Costs

sill prices a session from the token counts in its transcript, using a table of Anthropic's list
prices per million tokens. Input, output, cache reads and both kinds of cache writes (5 minutes and 1
hour) each have their own rate, fast mode is priced separately, and subagents are added to the session
that started them. The result is what the API would bill. On a Pro or Max plan, that is not what you
pay.

`sill set cost on` shows the running total on the line. Transcripts are deleted by Claude Code after
30 days, so the `SessionEnd` hook from `sill install` writes one line per session to
`~/.claude/sill-costs/YYYY-MM.jsonl` with the repository, the start and end times, and dollars and
tokens per model, about 500 bytes each. A response is counted toward the repository of the folder it
was made in: a session that moved between repositories is split across them, and a session in a linked
worktree counts toward the repository the worktree belongs to. Whenever a session ends, the hook also
picks up sessions the ledger is missing, such as one whose terminal was killed or one that was resumed
and kept growing.

```
$ sill cost
repo                         sessions     in   out     cost        last
~/code/storefront                  38   312m  1.2m  $281.40  2026-09-29
~/.../utils/sill                    5    54m  410k   $41.75  2026-09-29
total                              43   366m  1.6m  $323.15

$ sill cost .
~/source/repos/utils/sill

         started     in   out    cost  models        session
2026-09-24 14:05  11.2m  172k  $19.30  fable-5-1     a41c7e02
2026-09-28 09:12  17.6m  118k   $8.05  opus-5-5      7e93b5d1
```

Every row of the price table carries the date it took effect, and each response is priced at the rate
in force on the day it was made, so a price change never alters the cost of an old session. A model
missing from the table shows its token count rather than an invented price, and `sill doctor` lists
it. The table lives in `internal/cost/prices.json` and is built from
[LiteLLM's price list](https://github.com/BerriAI/litellm); a weekly workflow opens a pull request when
that list changes.

Claude Code writes a response into the transcript several times, once for each content block, and a
forked subagent's transcript begins with a copy of its parent's history. sill counts each response once,
from its most complete record. The totals were checked against a full decode of real transcripts.

## Internals

Claude Code runs the command on every update and sends a JSON payload on stdin, which is where most
segments come from. For the branch, sill walks up to the `.git` directory, following the `.git` file
of a worktree or submodule, and reads `HEAD` along with the marker files git leaves while an operation
is in progress. That takes about half a millisecond, compared with 20 ms or more just to start git on
Windows. git runs only when those files cannot answer: `GIT_DIR` or `GIT_WORK_TREE` set, a reftable
repository, or an unusual HEAD. `git status` runs only with `dirty` on, so large repositories stay fast.

Agents, compactions, cost and the session start come from the session transcript, whose path the
payload provides. sill never decodes a record; it searches for byte markers: the compaction boundary,
`Agent` tool calls, the task notification that names a finished agent, the usage of each response, and
the timestamp of the first record. Subagent transcripts beside it are read the same way for their cost.

The scan is incremental. Where it stopped and what it has counted are saved per session in the user
cache directory (`~/.cache/sill`, `~/Library/Caches/sill` or `%LocalAppData%\sill`, or
`SILL_CACHE_DIR`), so each render reads only what was appended since the previous one: around 1 ms,
against 45 ms for a full pass over a 25 MB transcript. A transcript that got shorter or was rewritten is
scanned again from the beginning, and state for sessions untouched for 30 days is deleted. With
`agents`, `compactions` and `cost` all off, only the start of the file is read, for the start time.

Each render has one second in total for git and the transcript. When that runs out, git is no longer
waited for and the scan resumes on the next render. Control characters in payload text (session,
folder and model names) are replaced with `?`, so no name can clear the screen or break the line. A
render that fails shows `sill: <error>` and writes the details (the error, the payload, and a stack
trace for a crash) to `last-error.txt` in the cache directory, where `sill doctor` finds it.

The complete payload schema is embedded in the Claude Code binary; search it for
`Pre-calculated: % of context used`.

## Development

```
go test ./...
gofmt -l . && go vet ./...
go build ./cmd/sill && ./sill demo
make bench              # render, ReadHead and transcript scan benchmarks
make fuzz               # each fuzz target for FUZZTIME (30s)
```

Only the standard library is used. The CLI is in `cmd/sill`, and `internal/` has one package per
concern: `payload`, `config`, `render`, `gitinfo`, `transcript`, `cost`, `ledger`, `term`, `install`
and `atomicfile`. To refresh the price table by hand, run `go run ./internal/cost/pricegen`.

CI runs the tests on Linux, macOS and Windows with both the latest Go and the minimum version in
`go.mod`, plus `govulncheck` and a short fuzzing pass. Actions are pinned to commit SHAs and kept up to
date by Dependabot. Pushing a `v*` tag builds release binaries for six targets with `-trimpath`, along
with checksums and provenance attestations.

## License

MIT
