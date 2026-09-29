// Package cost prices the tokens a session used. Prices are Anthropic's list prices per
// million tokens from a dated table, so a response is priced at the rate in effect when it
// was made, and a later price change leaves old sessions as they were billed.
package cost

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Tokens is what one or more responses consumed, by how each kind is billed.
type Tokens struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"` // thinking included
	CacheRead    int64 `json:"cache_read"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
}

// Total is every token, whatever its kind.
func (t Tokens) Total() int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheWrite5m + t.CacheWrite1h
}

// In is every token sent to the model: new, read from the cache and written to it.
func (t Tokens) In() int64 { return t.Input + t.CacheRead + t.CacheWrite5m + t.CacheWrite1h }

func (t *Tokens) add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite5m += o.CacheWrite5m
	t.CacheWrite1h += o.CacheWrite1h
}

// Usage is one model's share of a session.
type Usage struct {
	Tokens
	USD      float64 `json:"usd"`
	Unpriced int64   `json:"unpriced,omitempty"` // tokens of responses no price covered
}

// Totals is a session's usage by model. Responses in fast mode count under the model id
// with "/fast" appended, since they are billed at a different rate.
type Totals map[string]Usage

// Add prices the tokens of one response made at the given time and adds them. A model or
// a mode the table has no price for adds its tokens as unpriced.
func (t Totals) Add(model string, fast bool, at time.Time, tok Tokens) {
	model = Normalize(model)
	key := model
	if fast {
		key += "/fast"
	}
	u := t[key]
	u.Tokens.add(tok)
	if usd, ok := Price(model, fast, at, tok); ok {
		u.USD += usd
	} else {
		u.Unpriced += tok.Total()
	}
	t[key] = u
}

// Merge adds every model of o.
func (t Totals) Merge(o Totals) {
	for k, v := range o {
		u := t[k]
		u.Tokens.add(v.Tokens)
		u.USD += v.USD
		u.Unpriced += v.Unpriced
		t[k] = u
	}
}

// USD is what the priced responses cost.
func (t Totals) USD() float64 {
	var sum float64
	for _, u := range t {
		sum += u.USD
	}
	return sum
}

// Tokens is every model's tokens together.
func (t Totals) Tokens() Tokens {
	var sum Tokens
	for _, u := range t {
		sum.add(u.Tokens)
	}
	return sum
}

// Unpriced is the number of tokens no price covered.
func (t Totals) Unpriced() int64 {
	var sum int64
	for _, u := range t {
		sum += u.Unpriced
	}
	return sum
}

// Models lists the model keys, most expensive first, then by name.
func (t Totals) Models() []string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if t[a].USD != t[b].USD {
			if t[a].USD > t[b].USD {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	return keys
}

// Row is one price in the table: dollars per million tokens for a model, from a date on.
type Row struct {
	Model        string  `json:"model"`
	From         string  `json:"from,omitempty"` // YYYY-MM-DD, empty for since the model existed
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	CacheRead    float64 `json:"cache_read"`
	Fast         float64 `json:"fast,omitempty"` // multiplier in fast mode, 0 when unknown
}

// cost is what tok costs at this row's prices.
func (r Row) cost(tok Tokens) float64 {
	return (float64(tok.Input)*r.Input +
		float64(tok.Output)*r.Output +
		float64(tok.CacheRead)*r.CacheRead +
		float64(tok.CacheWrite5m)*r.CacheWrite5m +
		float64(tok.CacheWrite1h)*r.CacheWrite1h) / 1e6
}

//go:embed prices.json
var pricesJSON []byte

// table holds the rows sorted by model, then date.
var table = mustParse(pricesJSON)

// TableVersion identifies the price table this sill was built with. Anything that stored
// dollars computed from it, such as the transcript scan state, is recomputed when it
// changes. It hashes the parsed table, so line endings of a checkout do not matter.
var TableVersion = func() string {
	sum := sha256.Sum256(FormatTable(table))
	return hex.EncodeToString(sum[:6])
}()

func mustParse(data []byte) []Row {
	rows, err := ParseTable(data)
	if err != nil {
		panic("cost: prices.json: " + err.Error())
	}
	return rows
}

// ParseTable decodes and checks a price table, and sorts it by model and date.
func ParseTable(data []byte) ([]Row, error) {
	var rows []Row
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Model == "" || r.Model != Normalize(r.Model) {
			return nil, fmt.Errorf("model %q is not a normalized id", r.Model)
		}
		if r.From != "" {
			if _, err := time.Parse(time.DateOnly, r.From); err != nil {
				return nil, fmt.Errorf("%s: from %q is not a YYYY-MM-DD date", r.Model, r.From)
			}
		}
		if r.Input <= 0 || r.Output <= 0 || r.CacheRead < 0 || r.CacheWrite5m < 0 || r.CacheWrite1h < 0 || r.Fast < 0 {
			return nil, fmt.Errorf("%s from %q: a price is missing or negative", r.Model, r.From)
		}
	}
	sortRows(rows)
	for i := 1; i < len(rows); i++ {
		if rows[i].Model == rows[i-1].Model && rows[i].From == rows[i-1].From {
			return nil, fmt.Errorf("%s has two prices from %q", rows[i].Model, rows[i].From)
		}
	}
	return rows, nil
}

func sortRows(rows []Row) {
	slices.SortFunc(rows, func(a, b Row) int {
		if c := strings.Compare(a.Model, b.Model); c != 0 {
			return c
		}
		return strings.Compare(a.From, b.From)
	})
}

// Lookup returns the row in effect for a model at a time: the latest one dated on or
// before it. A zero time takes the current price.
func Lookup(model string, at time.Time) (Row, bool) {
	model = Normalize(model)
	day := ""
	if !at.IsZero() {
		day = at.UTC().Format(time.DateOnly)
	}
	var found Row
	ok := false
	for _, r := range table {
		if r.Model != model {
			continue
		}
		if day == "" || r.From <= day {
			found, ok = r, true
		}
	}
	return found, ok
}

// Price is what tok cost for a model at a time, or false when the table has no price for
// it: an unknown model, a date before its first price, or fast mode without a known rate.
func Price(model string, fast bool, at time.Time, tok Tokens) (float64, bool) {
	r, ok := Lookup(model, at)
	if !ok {
		return 0, false
	}
	usd := r.cost(tok)
	if fast {
		if r.Fast == 0 {
			return 0, false
		}
		usd *= r.Fast
	}
	return usd, true
}

// Known reports whether the table has any price for a model.
func Known(model string) bool {
	_, ok := Lookup(model, time.Time{})
	return ok
}

var (
	cloudSuffix = regexp.MustCompile(`-v\d+(:\d+)?$`) // Bedrock: ...-v1:0
	dateSuffix  = regexp.MustCompile(`-\d{8}$`)       // a dated snapshot: ...-20251001
)

// Normalize reduces a model id to the base id the table uses: lower case, without a
// context tag such as [1m], a cloud provider's prefix or version, or a snapshot date.
func Normalize(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	m, _, _ = strings.Cut(m, "[")
	m, _, _ = strings.Cut(m, "@") // Vertex: claude-opus-4-5@20251101
	if i := strings.Index(m, "claude-"); i > 0 {
		m = m[i:] // Bedrock: us.anthropic.claude-...
	}
	m = cloudSuffix.ReplaceAllString(m, "")
	return dateSuffix.ReplaceAllString(m, "")
}
