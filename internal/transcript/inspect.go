package transcript

import (
	"fmt"
	"os"
	"regexp"
)

// The scan matches exact byte patterns, which hold only while Claude Code serializes
// records the way it does today. These looser patterns tolerate spacing and order, so a
// transcript where they find more than the scan is one whose format moved.
var (
	looseToolUse = regexp.MustCompile(`"type"\s*:\s*"tool_use"`)
	looseAgent   = regexp.MustCompile(`"name"\s*:\s*"(?:Agent|Task)"`)
	looseCompact = regexp.MustCompile(`"subtype"\s*:\s*"compact_boundary"`)
	looseUsage   = regexp.MustCompile(`"usage"\s*:\s*\{`)
	looseModel   = regexp.MustCompile(`"model"\s*:\s*"claude-`)
)

// Report is what sill doctor shows about a transcript.
type Report struct {
	Records          int
	Activity         Activity
	Launches         int // records with an agent launch the scan recognised
	LooseLaunches    int // records that look like one to the loose patterns
	LooseCompactions int
	Responses        int // records with usage the scan recognised
	LooseResponses   int // records that look like one to the loose patterns
}

// Inspect scans a whole transcript the way a render does and counts, next to it, what the
// loose patterns find.
func Inspect(path string) (Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	var (
		rep Report
		s   scanner
	)
	_, err = readLines(f, true, func(line []byte) bool {
		rep.Records++
		s.record(line)
		if agentToolUse.Match(line) {
			rep.Launches++
		}
		if looseToolUse.Match(line) && looseAgent.Match(line) {
			rep.LooseLaunches++
		}
		if looseCompact.Match(line) {
			rep.LooseCompactions++
		}
		if _, ok := parseResponse(line); ok {
			rep.Responses++
		}
		if looseUsage.Match(line) && looseModel.Match(line) {
			rep.LooseResponses++
		}
		return true
	})
	rep.Activity = s.activity()
	return rep, err
}

// Drift lists the signs that the scan no longer understands the transcript.
func (r Report) Drift() []string {
	var out []string
	if r.LooseLaunches > r.Launches {
		out = append(out, fmt.Sprintf("%d of %d agent launches not recognised", r.LooseLaunches-r.Launches, r.LooseLaunches))
	}
	if r.LooseCompactions > r.Activity.Compactions {
		out = append(out, fmt.Sprintf("%d of %d compactions not recognised", r.LooseCompactions-r.Activity.Compactions, r.LooseCompactions))
	}
	if r.LooseResponses > r.Responses {
		out = append(out, fmt.Sprintf("%d of %d responses not recognised", r.LooseResponses-r.Responses, r.LooseResponses))
	}
	if r.Records > 0 && r.Activity.Start.IsZero() {
		out = append(out, "no record has a timestamp sill can read")
	}
	return out
}
