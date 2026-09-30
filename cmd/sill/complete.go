package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/LucasAntunesdeAlmeida/sill/internal/config"
)

// Shell completion. The scripts only collect the words typed so far and ask the binary
// with "sill __complete", so completion follows config.Options without regenerating them.

// commands are what "sill <Tab>" offers. hook and __complete are left out: Claude Code and
// the scripts run them, nobody types them.
var commands = []string{"completion", "cost", "demo", "doctor", "help", "install", "set", "settings", "uninstall", "unset", "version"}

// dirsDirective tells a script to fall back to the shell's own directory completion.
const dirsDirective = ":dirs"

// completion prints the script for one shell.
func completion(args []string, stdout io.Writer) error {
	if len(args) == 1 {
		if script, ok := completionScripts[args[0]]; ok {
			_, err := io.WriteString(stdout, script)
			return err
		}
	}
	return fmt.Errorf("completion takes one of %s, e.g. sill completion bash", strings.Join(shellNames(), ", "))
}

func shellNames() []string {
	names := make([]string, 0, len(completionScripts))
	for name := range completionScripts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// complete answers "sill __complete <n> <word>...": the n words after "sill" that are
// finished, then the word being typed, one candidate per line. The word being typed may be
// missing, since Windows PowerShell drops an empty argument to a native command; it is
// then empty.
func complete(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("__complete takes a word count and the words")
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 0 || n > len(args)-1 {
		return fmt.Errorf("__complete: bad word count %q", args[0])
	}
	words, cur := args[1:1+n], ""
	if len(args) > n+1 {
		cur = strings.ToLower(args[n+1])
	}
	var b strings.Builder
	for _, c := range candidates(words) {
		if c == dirsDirective || strings.HasPrefix(c, cur) {
			b.WriteString(c)
			b.WriteByte('\n')
		}
	}
	_, err = io.WriteString(stdout, b.String())
	return err
}

// candidates is everything that fits after words, before filtering by what is typed.
func candidates(words []string) []string {
	if len(words) == 0 {
		return commands
	}
	switch words[0] {
	case "set":
		// set takes key value pairs: an odd count of words means a key comes next.
		if len(words)%2 == 1 {
			return optionNames(keys(words[1:]))
		}
		return optionValues(words[len(words)-1])
	case "unset":
		return optionNames(words[1:], "lines")
	case "cost":
		if len(words) == 1 {
			return []string{dirsDirective}
		}
	case "completion":
		if len(words) == 1 {
			return shellNames()
		}
	}
	return nil
}

// keys takes the keys out of key value pairs.
func keys(pairs []string) []string {
	var ks []string
	for i := 0; i < len(pairs); i += 2 {
		ks = append(ks, pairs[i])
	}
	return ks
}

// optionNames is every option plus extra, sorted, without the names already typed.
func optionNames(typed []string, extra ...string) []string {
	used := map[string]bool{}
	for _, t := range typed {
		used[strings.ToLower(t)] = true
	}
	var names []string
	for _, o := range config.Options {
		if !used[o.Name] {
			names = append(names, o.Name)
		}
	}
	for _, e := range extra {
		if !used[e] {
			names = append(names, e)
		}
	}
	sort.Strings(names)
	return names
}

// optionValues is what an option takes; nothing for a number or an unknown option.
func optionValues(name string) []string {
	o := config.Find(strings.ToLower(name))
	if o == nil {
		return nil
	}
	switch o.Kind {
	case config.Bool:
		return []string{"off", "on"}
	case config.Enum:
		values := append([]string(nil), o.Values...)
		sort.Strings(values)
		return values
	}
	return nil
}

// completionScripts are kept in Go strings rather than embedded files: a raw string drops
// carriage returns, so a Windows checkout with CRLF line endings still prints a script
// bash can run.
var completionScripts = map[string]string{
	"bash": `# sill completion for bash. Load it from ~/.bashrc:
#   eval "$(sill completion bash)"
_sill() {
    local cur=${COMP_WORDS[COMP_CWORD]} IFS=$'\n'
    local -a out
    out=($(sill __complete "$((COMP_CWORD - 1))" "${COMP_WORDS[@]:1:COMP_CWORD-1}" "$cur" 2>/dev/null))
    if [[ ${out[0]} == :dirs ]]; then
        compopt -o filenames 2>/dev/null
        out=($(compgen -d -- "$cur"))
    fi
    COMPREPLY=("${out[@]}")
}
complete -F _sill sill
`,
	"zsh": `# sill completion for zsh. Load it from ~/.zshrc, after compinit:
#   source <(sill completion zsh)
_sill() {
    local -a out
    out=("${(@f)$(sill __complete $((CURRENT - 2)) "${(@)words[2,CURRENT-1]}" "${words[CURRENT]}" 2>/dev/null)}")
    out=(${out:#})
    if [[ $out[1] == :dirs ]]; then
        _files -/
        return
    fi
    compadd -- $out
}
compdef _sill sill
`,
	"fish": `# sill completion for fish. Install it with:
#   sill completion fish > ~/.config/fish/completions/sill.fish
function __sill_complete
    set -l words (commandline -opc)
    set -e words[1]
    set -l cur (commandline -ct)
    set -l out (sill __complete (count $words) $words $cur 2>/dev/null)
    if test "$out[1]" = :dirs
        __fish_complete_directories $cur
        return
    end
    printf '%s\n' $out
end
complete -c sill -f -a '(__sill_complete)'
`,
	"powershell": `# sill completion for PowerShell. Load it from $PROFILE:
#   sill completion powershell | Out-String | Invoke-Expression
Register-ArgumentCompleter -Native -CommandName sill, sill.exe -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $words = @($commandAst.CommandElements |
        Where-Object { $_.Extent.EndOffset -lt $cursorPosition } |
        Select-Object -Skip 1 |
        ForEach-Object {
            if ($_ -is [System.Management.Automation.Language.StringConstantExpressionAst]) { $_.Value } else { $_.Extent.Text }
        })
    $out = @(sill __complete $words.Count @words $wordToComplete 2>$null)
    if ($out.Count -gt 0 -and $out[0] -eq ':dirs') {
        return
    }
    if ($out.Count -eq 0) {
        # With no answer PowerShell completes file names, so answer the word as typed.
        $w = if ($wordToComplete) { $wordToComplete } else { ' ' }
        return [System.Management.Automation.CompletionResult]::new($w, $w, 'ParameterValue', $w)
    }
    foreach ($c in $out) {
        [System.Management.Automation.CompletionResult]::new($c, $c, 'ParameterValue', $c)
    }
}
`,
}
