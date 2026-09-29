package transcript

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"github.com/LucasAntunesdeAlmeida/sill/internal/cost"
)

// Claude Code writes one record per content block of a response (thinking, text, each
// tool call), and every one of them repeats the response's usage. The records of one
// response come one after another, and the last carries the final usage, so the scan keeps
// the latest record of the current response and adds it to the totals once the next
// response starts.

var (
	// The usage of a response is the last one in its record: a tool call's input, earlier
	// in the record, could hold a "usage" object of its own.
	usageMarker = []byte(`"usage":{`)
	// An assistant record opens its message with the model and the response id.
	messageMarker = []byte(`"message":{"model":"`)
	idMarker      = []byte(`","id":"`)
	// iterations repeats the token counts per server-side step; the totals come before it.
	iterationsMarker = []byte(`"iterations":`)
	fastMarker       = []byte(`"speed":"fast"`)
	cwdMarker        = []byte(`"cwd":"`)

	inputTokens   = []byte(`"input_tokens":`)
	outputTokens  = []byte(`"output_tokens":`)
	cacheRead     = []byte(`"cache_read_input_tokens":`)
	cacheCreation = []byte(`"cache_creation_input_tokens":`)
	cacheWrite5m  = []byte(`"ephemeral_5m_input_tokens":`)
	cacheWrite1h  = []byte(`"ephemeral_1h_input_tokens":`)
)

// response is one API response as the latest of its records reports it.
type response struct {
	ID     string      `json:"id"`
	Model  string      `json:"model"`
	Fast   bool        `json:"fast,omitempty"`
	At     time.Time   `json:"at"`
	Tokens cost.Tokens `json:"tokens"`
}

// parseResponse reads the response in a record, if it is one.
func parseResponse(line []byte) (response, bool) {
	// The forward search rules out most records quickly; searching backwards is slower.
	_, rest, found := bytes.Cut(line, messageMarker)
	if !found {
		return response{}, false
	}
	u := bytes.LastIndex(line, usageMarker)
	if u < 0 {
		return response{}, false
	}
	model, rest, found := bytes.Cut(rest, []byte{'"'})
	if !found || !bytes.HasPrefix(rest, idMarker[1:]) {
		return response{}, false
	}
	id, _, found := bytes.Cut(rest[len(idMarker)-1:], []byte{'"'})
	if !found || len(id) == 0 {
		return response{}, false
	}
	usage := object(line[u+len(usageMarker)-1:])
	if usage == nil {
		return response{}, false
	}
	totals := usage
	if i := bytes.Index(totals, iterationsMarker); i >= 0 {
		totals = totals[:i]
	}
	tok := cost.Tokens{
		Input:     number(totals, inputTokens),
		Output:    number(totals, outputTokens),
		CacheRead: number(totals, cacheRead),
	}
	if bytes.Contains(totals, cacheWrite5m) || bytes.Contains(totals, cacheWrite1h) {
		tok.CacheWrite5m, tok.CacheWrite1h = number(totals, cacheWrite5m), number(totals, cacheWrite1h)
	} else {
		tok.CacheWrite5m = number(totals, cacheCreation) // no breakdown: the API's default TTL
	}
	r := response{ID: string(id), Model: string(model), Fast: bytes.Contains(usage, fastMarker), Tokens: tok}
	if i := bytes.LastIndex(line, timestampMarker); i >= 0 {
		r.At, _ = timestamp(line[i:])
	}
	return r, true
}

// number is the non-negative integer after the first key in b, or 0.
func number(b, key []byte) int64 {
	i := bytes.Index(b, key)
	if i < 0 {
		return 0
	}
	b = b[i+len(key):]
	end := 0
	for end < len(b) && b[end] >= '0' && b[end] <= '9' {
		end++
	}
	n, err := strconv.ParseInt(string(b[:end]), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// object returns the JSON object b starts with, through its closing brace, or nil when b
// does not start with one or it is cut short. Braces inside strings do not count.
func object(b []byte) []byte {
	if len(b) == 0 || b[0] != '{' {
		return nil
	}
	depth, inString := 0, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inString && c == '\\':
			i++
		case c == '"':
			inString = !inString
		case inString:
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			depth--
			if depth == 0 {
				return b[:i+1]
			}
		}
	}
	return nil
}

// lastString returns the JSON string value after the last key in line, unescaped, or "".
func lastString(line, key []byte) string {
	i := bytes.LastIndex(line, key)
	if i < 0 {
		return ""
	}
	rest := line[i+len(key)-1:] // from the opening quote
	for j := 1; j < len(rest); j++ {
		switch rest[j] {
		case '\\':
			j++
		case '"':
			var s string
			if json.Unmarshal(rest[:j+1], &s) != nil {
				return ""
			}
			return s
		}
	}
	return ""
}
