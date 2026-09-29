package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
	"github.com/LucasAntunesdeAlmeida/sill/internal/gitinfo"
	"github.com/LucasAntunesdeAlmeida/sill/internal/ledger"
	"github.com/LucasAntunesdeAlmeida/sill/internal/render"
)

// costReport prints what sessions cost: per repository, or with a folder, per session of
// the repository around it. Sessions the ledger does not have yet are recorded first, the
// way the hook would.
func costReport(args []string, stdout io.Writer) error {
	if len(args) > 1 {
		return fmt.Errorf("cost takes at most one folder, e.g. sill cost .")
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
	defer cancel()
	swept, err := sweep(ctx, "", time.Now())
	if err != nil {
		return err
	}
	if err := ledger.Append(ledgerDir(), swept...); err != nil {
		return err
	}
	entries, bad, err := ledger.Read(ledgerDir())
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	if len(args) == 1 {
		abs, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		err = sessionTable(stdout, entries, gitinfo.Root(abs), home)
		if err != nil {
			return err
		}
	} else if err := repoTable(stdout, entries, home); err != nil {
		return err
	}
	if bad > 0 {
		fmt.Fprintf(stdout, "%d line(s) of the ledger could not be read and were skipped.\n", bad)
	}
	if ctx.Err() != nil {
		fmt.Fprintln(stdout, "Some recent sessions are still being read; run it again for the full total.")
	}
	return nil
}

func repoTable(w io.Writer, entries []ledger.Entry, home string) error {
	if len(entries) == 0 {
		_, err := fmt.Fprintf(w, "No session costs recorded yet in %s.\n", ledgerDir())
		return err
	}
	rows := [][]string{{"repo", "sessions", "in", "out", "cost", "last"}}
	total := cost.Totals{}
	for _, r := range ledger.ByRepo(entries) {
		rows = append(rows, []string{
			render.Clean(render.DisplayPath(r.Repo, home, 1)), fmt.Sprint(r.Sessions),
			render.Tokens(int(r.Cost.Tokens().In())), render.Tokens(int(r.Cost.Tokens().Output)),
			costCell(r.Cost), day(r.Last),
		})
		total.Merge(r.Cost)
	}
	rows = append(rows, []string{"total", fmt.Sprint(len(entries)),
		render.Tokens(int(total.Tokens().In())), render.Tokens(int(total.Tokens().Output)), costCell(total), ""})
	writeTable(w, rows, 1, 5)
	return footer(w, total)
}

func sessionTable(w io.Writer, entries []ledger.Entry, repo, home string) error {
	rows := [][]string{{"started", "in", "out", "cost", "models", "session"}}
	total := cost.Totals{}
	n := 0
	split := false
	for _, e := range entries {
		for _, p := range e.Shares() {
			if !samePath(p.Repo, repo) {
				continue
			}
			n++
			var models []string
			for _, m := range p.Models.Models() {
				models = append(models, strings.TrimPrefix(m, "claude-"))
			}
			id := short(e.Session)
			if len(e.Parts) > 1 {
				id += "*"
				split = true
			}
			rows = append(rows, []string{
				e.Start.Local().Format("2006-01-02 15:04"),
				render.Tokens(int(p.Models.Tokens().In())), render.Tokens(int(p.Models.Tokens().Output)),
				costCell(p.Models), render.Clean(strings.Join(models, ",")), render.Clean(id),
			})
			total.Merge(p.Models)
		}
	}
	shown := render.Clean(render.DisplayPath(repo, home, 0))
	if n == 0 {
		_, err := fmt.Fprintf(w, "No session costs recorded for %s.\n", shown)
		return err
	}
	fmt.Fprintf(w, "%s\n\n", shown)
	rows = append(rows, []string{"total", render.Tokens(int(total.Tokens().In())), render.Tokens(int(total.Tokens().Output)),
		costCell(total), fmt.Sprintf("%d session(s)", n), ""})
	writeTable(w, rows, 1, 3)
	if split {
		fmt.Fprintln(w, "\n* the session also worked in another repository; only its share here is shown.")
	}
	return footer(w, total)
}

// costCell is the dollars, with + when some tokens had no price, or "-" when none had one.
func costCell(t cost.Totals) string {
	usd, unpriced := t.USD(), t.Unpriced()
	switch {
	case usd > 0 && unpriced > 0:
		return render.Dollars(usd) + "+"
	case usd > 0:
		return render.Dollars(usd)
	case unpriced > 0:
		return "-"
	}
	return "$0.00"
}

func footer(w io.Writer, total cost.Totals) error {
	fmt.Fprintln(w)
	if total.Unpriced() > 0 {
		fmt.Fprintf(w, "+ %s token(s) came from models without a price; run `sill doctor` for which.\n", render.Tokens(int(total.Unpriced())))
	}
	_, err := fmt.Fprintln(w, "API list prices: what the tokens would cost on the API, not what a Pro or Max plan bills.")
	return err
}

// writeTable prints rows in columns separated by two spaces. Columns from numFrom to
// numTo, inclusive, are numbers and align right; the last row is a total.
func writeTable(w io.Writer, rows [][]string, numFrom, numTo int) {
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], len(cell))
		}
	}
	for _, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			if i > 0 {
				b.WriteString("  ")
			}
			pad := strings.Repeat(" ", widths[i]-len(cell))
			if i >= numFrom && i <= numTo {
				b.WriteString(pad + cell)
			} else {
				b.WriteString(cell + pad)
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

func day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02")
}

// short is the start of a session id, enough to tell sessions apart.
func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// samePath compares folder paths, ignoring case on Windows.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
