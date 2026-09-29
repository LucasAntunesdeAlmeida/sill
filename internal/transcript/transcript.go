// Package transcript reads session activity out of Claude Code's JSONL transcript.
package transcript

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
)

// Activity is what the line learns from the transcript.
type Activity struct {
	Agents      int         // background agents started but not yet reported finished
	Compactions int         // compact boundaries written so far
	Start       time.Time   // timestamp of the first record, zero when none has one
	Last        time.Time   // timestamp of the latest response
	Cost        cost.Totals // tokens and dollars of every response, subagents included
	Cwd         string      // working directory of the latest response
}

// Empty reports whether the transcript said nothing at all.
func (a Activity) Empty() bool {
	return a.Agents == 0 && a.Compactions == 0 && a.Start.IsZero() && a.Last.IsZero() &&
		len(a.Cost) == 0 && a.Cwd == ""
}

var (
	// A system record Claude Code writes each time the context is compacted.
	compactMarker = []byte(`"subtype":"compact_boundary"`)
	// An assistant tool_use that launches a subagent (Agent today, Task in older versions).
	agentToolUse = regexp.MustCompile(`"type":"tool_use","id":"([^"]+)","name":"(?:Agent|Task)"`)
	agentHint    = []byte(`"name":"Agent"`)
	taskHint     = []byte(`"name":"Task"`)
	// The task notification that reports a background agent finished names its tool use.
	notifyOpen  = []byte(`<tool-use-id>`)
	notifyClose = []byte(`</tool-use-id>`)
	// A tool_result for the launch: async launches acknowledge, sync ones carry the output.
	resultMarker = []byte(`"type":"tool_result"`)
	resultID     = []byte(`"tool_use_id":"`)
	asyncAck     = []byte(`Async agent launched`)
	// Every record Claude Code writes carries a timestamp at its top level.
	timestampMarker = []byte(`"timestamp":"`)
)

// maxRecord bounds a single transcript line; tool results can make one several MB long.
const maxRecord = 64 << 20

// headRecords is how far Start looks for a first timestamp.
const headRecords = 50

// Scan reads the whole transcript. Missing or unreadable files count as no activity.
func Scan(path string) Activity {
	if path == "" {
		return Activity{}
	}
	f, err := os.Open(path)
	if err != nil {
		return Activity{}
	}
	defer f.Close()
	return ScanReader(f)
}

// Start reads only the head of the transcript for the session's first timestamp, for
// when the full scan is not wanted.
func Start(path string) time.Time {
	if path == "" {
		return time.Time{}
	}
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	return StartReader(f)
}

// StartReader returns the first timestamp within the first records, or zero.
func StartReader(r io.Reader) time.Time {
	var start time.Time
	n := 0
	_, _ = readLines(r, true, func(line []byte) bool {
		if t, ok := timestamp(line); ok {
			start = t
			return false
		}
		n++
		return n < headRecords
	})
	return start
}

// ScanReader looks for byte markers and never decodes a record, so a 20 MB transcript
// costs a few tens of milliseconds.
func ScanReader(r io.Reader) Activity {
	var s scanner
	// A read error is not worth failing the render over; what was read still counts.
	_, _ = readLines(r, true, func(line []byte) bool {
		s.record(line)
		return true
	})
	return s.activity()
}

// scanner accumulates what the records say, one record at a time.
type scanner struct {
	compactions int
	start       time.Time
	open        map[string]bool // agent tool uses started and not yet reported finished
	cost        cost.Totals     // responses that are complete
	pending     response        // the response whose records are being written
	last        time.Time
	cwd         string
}

func (s *scanner) record(line []byte) {
	if s.start.IsZero() {
		if t, ok := timestamp(line); ok {
			s.start = t
		}
	}
	if r, ok := parseResponse(line); ok {
		if r.ID != s.pending.ID {
			s.commit()
		}
		s.pending = r
		if r.At.After(s.last) {
			s.last = r.At
		}
		if cwd := lastString(line, cwdMarker); cwd != "" {
			s.cwd = cwd
		}
	}
	if bytes.Contains(line, compactMarker) {
		s.compactions++
	}
	if bytes.Contains(line, agentHint) || bytes.Contains(line, taskHint) {
		for _, m := range agentToolUse.FindAllSubmatch(line, -1) {
			if s.open == nil {
				s.open = map[string]bool{}
			}
			s.open[string(m[1])] = true
		}
	}
	if bytes.Contains(line, notifyOpen) {
		for _, id := range between(line, notifyOpen, notifyClose) {
			delete(s.open, id)
		}
	}
	if len(s.open) > 0 && bytes.Contains(line, resultMarker) {
		for rest := line; ; {
			i := bytes.Index(rest, resultID)
			if i < 0 {
				break
			}
			rest = rest[i+len(resultID):]
			end := bytes.IndexByte(rest, '"')
			if end < 0 {
				break
			}
			if id := string(rest[:end]); s.open[id] && syncResult(rest[end:]) {
				delete(s.open, id)
			}
			rest = rest[end:]
		}
	}
}

// commit adds the pending response to the totals.
func (s *scanner) commit() {
	if s.pending.ID == "" {
		return
	}
	if s.cost == nil {
		s.cost = cost.Totals{}
	}
	s.cost.Add(s.pending.Model, s.pending.Fast, s.pending.At, s.pending.Tokens)
	s.pending = response{}
}

// totals is the complete responses plus the pending one, without committing it: its
// records may not all be written yet.
func (s *scanner) totals() cost.Totals {
	t := cost.Totals{}
	t.Merge(s.cost)
	if p := s.pending; p.ID != "" {
		t.Add(p.Model, p.Fast, p.At, p.Tokens)
	}
	if len(t) == 0 {
		return nil
	}
	return t
}

func (s *scanner) activity() Activity {
	return Activity{
		Agents: len(s.open), Compactions: s.compactions, Start: s.start,
		Last: s.last, Cost: s.totals(), Cwd: s.cwd,
	}
}

// readLines calls fn with every line, without its newline, until fn returns false to say
// it stopped without taking that line, and returns the bytes consumed through the last
// complete line taken. A line longer
// than maxRecord is skipped whole and reading goes on after it: one huge tool result must
// not hide everything that follows. A last line without a newline goes to fn only when
// partial is set; it may still be being written.
func readLines(r io.Reader, partial bool, fn func(line []byte) bool) (int64, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var (
		consumed int64
		long     []byte // a line that outgrew the reader's buffer
		size     int    // its length so far, counted even while skipping
		skipping bool
	)
	for {
		chunk, err := br.ReadSlice('\n')
		size += len(chunk)
		switch err {
		case bufio.ErrBufferFull:
			if !skipping && len(long)+len(chunk) > maxRecord {
				skipping, long = true, long[:0]
			}
			if !skipping {
				long = append(long, chunk...)
			}
			continue
		case nil:
			line := chunk[:len(chunk)-1]
			if len(long) > 0 {
				long = append(long, line...)
				line = long
			}
			if !skipping && len(line) <= maxRecord && !fn(line) {
				return consumed, nil
			}
			consumed += int64(size)
			long, size, skipping = long[:0], 0, false
		case io.EOF:
			if partial && !skipping && size > 0 && size <= maxRecord {
				fn(append(long, chunk...))
			}
			return consumed, nil
		default:
			return consumed, err
		}
	}
}

// timestamp extracts the record's timestamp, an RFC 3339 string.
func timestamp(line []byte) (time.Time, bool) {
	_, rest, found := bytes.Cut(line, timestampMarker)
	if !found {
		return time.Time{}, false
	}
	value, _, found := bytes.Cut(rest, []byte{'"'})
	if !found {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, string(value))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// between returns every substring enclosed by open and close.
func between(line, open, close []byte) []string {
	var out []string
	for {
		i := bytes.Index(line, open)
		if i < 0 {
			return out
		}
		line = line[i+len(open):]
		j := bytes.Index(line, close)
		if j < 0 {
			return out
		}
		out = append(out, string(line[:j]))
		line = line[j+len(close):]
	}
}

// syncResult reports whether the tool result that starts at after (just past its
// tool_use_id) is a real one, meaning the agent ran in the foreground and is done. An async
// launch only acknowledges and is still running.
func syncResult(after []byte) bool {
	if len(after) > 600 {
		after = after[:600]
	}
	return !bytes.Contains(after, asyncAck)
}
