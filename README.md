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
put it somewhere on your PATH, and run `sill install`. Each binary carries a build provenance
attestation, so you can check it was built by this repository's release workflow:

```
gh attestation verify sill-linux-amd64 --repo LucasAntunesdeAlmeida/sill
```

The macOS binaries are not signed. If one was downloaded through a browser, macOS quarantines it;
clear that with `xattr -d com.apple.quarantine sill-darwin-arm64`.

`sill install` writes the `statusLine` entry into `~/.claude/settings.json` (backed up first to `settings.json.sill.bak`, other
settings untouched) pointing at the binary where it is, and a `SessionEnd` hook that records what each
session cost (see Costs). Restart Claude Code. The binary needs to be on
your PATH only so that `sill set` is easy to type; `go install` puts it in `$GOPATH/bin`, which usually is.
It honors `CLAUDE_CONFIG_DIR` if you use one. `sill uninstall` removes both entries again.

Requirements: none for most repositories, since the branch is read from `.git/HEAD`. `git` on PATH
is used for `dirty` and for the repositories whose HEAD file cannot answer (see How it works). On
Windows, Claude Code runs status line commands through Git Bash, so Git for Windows is there anyway.

If the line is blank, wrong or says `sill: ...`, run `sill doctor`.

## What it shows

Segments only appear when they have something to say, so a fresh session in a plain folder shows just
the path and the model. Labels and separators are gray; only budgets change color.

| Key | Default | Shows | Source |
|---|---|---|---|
| `ctx` | on | `ctx 61%`, yellow at 60, red at 80 | payload |
| `limits` | on | `5h 94% (1h20)`, `7d`, `spend`; yellow at 70, red at 90. From yellow on, the reset time follows | payload |
| `cache` | off | `cache 42m` while the prompt cache is warm, `cache cold` after | payload |
| `cost` | off | `cost $4.12`, the session at API list prices, subagents included; `+` when a model had no price, `tok 1.2m` when none had | transcript |
| `agents` | on | `agents 2`, background agents still running | transcript |
| `compactions` | on | `compact 1`, how many times the context was compacted | transcript |
| `duration` | off | `up 2h15`, time since the session started | transcript |
| `path` | on | `~/.../utils/sill`: home as `~`, anchor plus the last two folders | payload |
| `git` | on | branch, or short sha when detached, then `MERGING`, `REBASING`, `CHERRY-PICKING`, `REVERTING` or `BISECTING` in red | `.git/HEAD` |
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
one by one: `version`, `duration`, `session`, `compactions`, `agents`, `cache`, `cost`, `effort`,
`worktree`, `pr`, `git`, `path`. Budgets and the model go last. Widths are counted in terminal columns, so a
folder or session name in Chinese, Japanese or Korean, or with an emoji, counts double.
`sill settings` prints the detected width; if it says unknown, set `width` yourself.

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
```

```
sill set layout full
sill set cache on version off
sill set reset both
sill set width 120
sill unset width
```

`sill doctor` prints one line per check: whether `settings.json` runs this binary and has the cost
hook, problems in `sill.json` (unknown keys, bad values, names in `lines` that are not segments), how
the branch is read and how long it takes, the terminal width, the newest transcript with its scan
times, the cost ledger with any model it has no price for, and the last render that failed. It also
compares the transcript markers sill relies on with a looser search, so if a Claude Code update
changes the transcript format it says so rather than showing no agents or a low cost:

```
ok    sill.json is valid, 1 option(s) changed
ok    settings.json runs this binary
ok    a SessionEnd hook records session costs
ok    git: branch main read from .git/HEAD in 0.5 ms, no git process
ok    terminal width 120 columns
ok    latest transcript: 1804 records, 0 agent(s) running, 0 compaction(s), 612 response(s)
      C:/Users/me/.claude/projects/.../5f2c.jsonl (24.4 MB)
      full scan 76.0 ms, a render with nothing new 1.5 ms
ok    cost ledger: 132 session(s), $1430 at list prices, 1 file(s), 61.8 KB
      C:/Users/me/.claude/sill-costs
ok    no failed render recorded
```

Inside a Claude Code session, prefix the command with `!` to run it without a model turn:

```
! sill set layout full
```

The line picks up the change on its next refresh. Settings live in `~/.claude/sill.json` and the file
only contains what you changed, so `sill settings` is the place to see everything.

## Costs

What a session costs is worked out from the token counts in its transcript and a table of Anthropic's
list prices per million tokens. Input, output, cache reads and cache writes (5 minute and 1 hour) are
priced separately, fast mode at its own rate, and subagents count with their session. It is what the
API would charge; on a Pro or Max plan it is not what you pay.

`sill set cost on` puts the running total on the line. Claude Code deletes transcripts after 30 days,
so the `SessionEnd` hook that `sill install` adds keeps one line per session in
`~/.claude/sill-costs/YYYY-MM.jsonl`: the repository, start and end, dollars and tokens per model.
That is about 500 bytes a session. Each response counts toward the repository of the folder it was
made in, so a session that moved from one repository to another is split between them, and sessions in
a linked worktree count toward the repository it belongs to. When a session ends, the hook also records any session the ledger missed, such as a terminal that
was killed, or one that was resumed and grew.

```
$ sill cost
repo                         sessions     in   out     cost        last
~/source/repos/invoicing           43   388m  1.5m  $346.06  2026-09-29
~/.../utils/sill                    6    63m  533k   $50.49  2026-09-29
total                              49   451m  2.0m  $396.55

$ sill cost .
~/source/repos/utils/sill

         started     in   out    cost  models        session
2026-09-25 18:38  10.4m  189k  $22.16  fable-5-1     3b3d5c80
2026-09-29 18:26  22.5m  131k   $9.44  opus-5-5      d0f565cd
```

Each price row carries the date it took effect, and a response is priced at the rate of the day it was
made, so a price change never rewrites what an old session cost. A model the table does not know shows
its tokens instead of a made-up price, and `sill doctor` names it. The table in
`internal/cost/prices.json` comes from [LiteLLM's price list](https://github.com/BerriAI/litellm);
a weekly workflow proposes a pull request when that list changes.

Claude Code writes each response to the transcript several times, once per content block, and a forked
subagent's transcript starts with a copy of its parent's history. sill counts every response once, at
its most complete record, and was checked against a full decode of 132 real sessions.

## How it works

Claude Code runs the command on every update and pipes a JSON payload on stdin. sill reads the fields
above from it. For the branch it walks up to the `.git` directory (or follows the `.git` file of a
worktree or submodule), reads `HEAD` and git's marker files for the operation in progress: about half
a millisecond, where starting git costs 20 ms or more on Windows. git itself runs only when those files
cannot answer (`GIT_DIR` or `GIT_WORK_TREE` set, a reftable repository, an unusual HEAD), and
`git status` only when you turn `dirty` on, so large repos stay fast.

Agents, compactions, cost and the session start come from the session transcript, whose path is in the
payload. sill looks for byte markers and never decodes a record: the compaction boundary, `Agent` tool
calls, the task notification that names a finished agent, the usage of each response, and the first
record's timestamp. Subagent transcripts next to it are read the same way, for their cost. The scan
is incremental: where it stopped and what it counted are kept per session in the user cache directory
(`~/.cache/sill`, `~/Library/Caches/sill` or `%LocalAppData%\sill`, or `SILL_CACHE_DIR`), so a render
reads only what was appended since the last one, about 1 ms against 45 ms for a full pass over a 25 MB
transcript. A transcript that shrank or was rewritten is scanned again from the start, and the state
of sessions untouched for 30 days is removed. With `agents`, `compactions` and `cost` off, only the
head of the file is read for the start time.

A render gets one second in total for git and the transcript. Past it, git is not waited for and the
scan continues on the next render. Text from the payload (session, folder, model names) has control
characters replaced with `?`, so a name cannot clear the screen or split the line. If a render fails,
the line says `sill: <error>` and the details (the error, the payload, a stack for a crash) go to
`last-error.txt` in the cache directory, which `sill doctor` reports.

The full payload schema is embedded in the Claude Code binary; search it for
`Pre-calculated: % of context used`.

## Development

```
go test ./...
gofmt -l . && go vet ./...
go build ./cmd/sill && ./sill demo
make bench              # render, ReadHead and transcript scan benchmarks
make fuzz               # each fuzz target for FUZZTIME (30s)
```

Standard library only. Layout is `cmd/sill` for the CLI and `internal/` for one package per concern:
`payload`, `config`, `render`, `gitinfo`, `transcript`, `cost`, `ledger`, `term`, `install`,
`atomicfile`. `go run ./internal/cost/pricegen` refreshes the price table by hand. CI tests on
Linux, macOS and Windows with the latest Go and with the minimum in `go.mod`, runs `govulncheck` and a
short fuzzing pass. Actions are pinned to commit SHAs and kept current by Dependabot. Tags `v*` build
release binaries for six targets with `-trimpath`, with checksums and provenance attestations.

## License

MIT
