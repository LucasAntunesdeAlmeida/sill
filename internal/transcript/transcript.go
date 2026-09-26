// Package transcript reads session activity out of Claude Code's JSONL transcript.
package transcript

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"regexp"
	"time"
)

// Activity is what the line learns from the transcript.
type Activity struct {
	Agents      int       // background agents started but not yet reported finished
	Compactions int       // compact boundaries written so far
	Start       time.Time // timestamp of the first record, zero when none has one
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
	asyncAck     = []byte(`Async agent launched`)
	// Every record Claude Code writes carries a timestamp at its top level.
	timestampMarker = []byte(`"timestamp":"`)
)

// maxRecord bounds a single transcript line; tool results can make one several MB long.
const maxRecord = 64 << 20

// headRecords is how far Start looks for a first timestamp.
const headRecords = 50

// Scan reads the transcript once. Missing or unreadable files count as no activity.
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
	sc := newScanner(r)
	for n := 0; n < headRecords && sc.Scan(); n++ {
		if t, ok := timestamp(sc.Bytes()); ok {
			return t
		}
	}
	return time.Time{}
}

// ScanReader looks for byte markers and never decodes a record, so a 20 MB transcript
// costs a few tens of milliseconds. A record too long to buffer ends the scan early
// with what was found so far.
func ScanReader(r io.Reader) Activity {
	var act Activity
	started := map[string]bool{}
	finished := map[string]bool{}

	sc := newScanner(r)
	for sc.Scan() {
		line := sc.Bytes()
		if act.Start.IsZero() {
			if t, ok := timestamp(line); ok {
				act.Start = t
			}
		}
		if bytes.Contains(line, compactMarker) {
			act.Compactions++
		}
		if bytes.Contains(line, agentHint) || bytes.Contains(line, taskHint) {
			for _, m := range agentToolUse.FindAllSubmatch(line, -1) {
				started[string(m[1])] = true
			}
		}
		if bytes.Contains(line, notifyOpen) {
			for _, id := range between(line, notifyOpen, notifyClose) {
				finished[id] = true
			}
		}
		if bytes.Contains(line, resultMarker) {
			for id := range started {
				if syncResult(line, id) {
					finished[id] = true
				}
			}
		}
	}
	_ = sc.Err() // a truncated or oversized record is not worth failing the render over
	for id := range started {
		if !finished[id] {
			act.Agents++
		}
	}
	return act
}

func newScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxRecord)
	return sc
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

// syncResult reports whether line carries a real result for the tool use, meaning the agent
// ran in the foreground and is done. An async launch only acknowledges and is still running.
func syncResult(line []byte, id string) bool {
	i := bytes.Index(line, []byte(`"tool_use_id":"`+id+`"`))
	if i < 0 {
		return false
	}
	window := line[i:]
	if len(window) > 600 {
		window = window[:600]
	}
	return !bytes.Contains(window, asyncAck)
}
