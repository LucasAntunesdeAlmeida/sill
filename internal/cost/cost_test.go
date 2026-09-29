package cost

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t.Add(12 * time.Hour)
}

// withTable swaps in a price table for one test.
func withTable(t *testing.T, doc string) {
	t.Helper()
	rows, err := ParseTable([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	old := table
	table = rows
	t.Cleanup(func() { table = old })
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5-5":                            "claude-opus-5-5",
		"claude-haiku-4-5-20251001":                  "claude-haiku-4-5",
		"claude-fable-5-1[1m]":                       "claude-fable-5-1",
		" Claude-Opus-5-5 ":                          "claude-opus-5-5",
		"us.anthropic.claude-opus-4-8-20260101-v1:0": "claude-opus-4-8",
		"anthropic.claude-opus-5-5":                  "claude-opus-5-5",
		"claude-opus-4-5@20251101":                   "claude-opus-4-5",
		"<synthetic>":                                "<synthetic>",
		"":                                           "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// The response from the design discussion: one Opus 5.5 turn, mostly cache reads.
func TestPriceOfARealResponse(t *testing.T) {
	tok := Tokens{Input: 2, Output: 453, CacheRead: 70433, CacheWrite1h: 381}
	usd, ok := Price("claude-opus-5-5", false, day("2026-09-29"), tok)
	want := (2*4 + 453*20 + 70433*0.2 + 381*8) / 1e6
	if !ok || !near(usd, want) {
		t.Errorf("price = %v, %v, want %v", usd, ok, want)
	}
	if fast, ok := Price("claude-opus-5-5", true, day("2026-09-29"), tok); !ok || !near(fast, 2*want) {
		t.Errorf("fast = %v, %v", fast, ok)
	}
	if _, ok := Price("claude-opus-4-8", true, time.Time{}, tok); ok {
		t.Error("fast mode without a known multiplier should be unpriced")
	}
	if _, ok := Price("claude-unknown-9", false, time.Time{}, tok); ok {
		t.Error("an unknown model should be unpriced")
	}
}

// A price change applies from its date on; responses before it keep the old rate.
func TestDatedPrices(t *testing.T) {
	withTable(t, `[
		{"model":"claude-x","input":4,"output":20,"cache_write_5m":5,"cache_write_1h":8,"cache_read":0.2},
		{"model":"claude-x","from":"2027-01-15","input":2,"output":10,"cache_write_5m":2.5,"cache_write_1h":4,"cache_read":0.1},
		{"model":"claude-y","from":"2027-03-01","input":1,"output":5,"cache_write_5m":1.25,"cache_write_1h":2,"cache_read":0.1}
	]`)
	out := Tokens{Output: 1_000_000}
	for _, c := range []struct {
		model string
		at    time.Time
		want  float64
		ok    bool
	}{
		{"claude-x", day("2026-12-31"), 20, true},
		{"claude-x", day("2027-01-15"), 10, true},
		{"claude-x", day("2027-06-01"), 10, true},
		{"claude-x", time.Time{}, 10, true}, // no timestamp: the current price
		{"claude-y", day("2027-02-01"), 0, false},
		{"claude-y", day("2027-03-01"), 5, true},
	} {
		got, ok := Price(c.model, false, c.at, out)
		if ok != c.ok || !near(got, c.want) {
			t.Errorf("%s at %v = %v, %v; want %v, %v", c.model, c.at, got, ok, c.want, c.ok)
		}
	}
}

func TestTotals(t *testing.T) {
	withTable(t, `[{"model":"claude-x","input":1,"output":2,"cache_write_5m":3,"cache_write_1h":4,"cache_read":5,"fast":2}]`)
	tot := Totals{}
	million := Tokens{Input: 1e6, Output: 1e6, CacheRead: 1e6, CacheWrite5m: 1e6, CacheWrite1h: 1e6}
	tot.Add("claude-x-20260101", false, time.Time{}, million)
	tot.Add("claude-x", true, time.Time{}, Tokens{Output: 1e6})
	tot.Add("claude-z", false, time.Time{}, Tokens{Input: 10, Output: 5})
	if got := tot.USD(); !near(got, 15+4) {
		t.Errorf("USD = %v, want 19", got)
	}
	if got := tot.Unpriced(); got != 15 {
		t.Errorf("unpriced = %d", got)
	}
	if got := tot.Tokens(); got.Output != 2e6+5 || got.In() != 4e6+10 || got.Total() != 6e6+15 {
		t.Errorf("tokens = %+v", got)
	}
	if got := tot.Models(); strings.Join(got, ",") != "claude-x,claude-x/fast,claude-z" {
		t.Errorf("models = %v", got)
	}
	sum := Totals{}
	sum.Merge(tot)
	sum.Merge(tot)
	if !near(sum.USD(), 2*tot.USD()) || sum.Unpriced() != 30 || sum["claude-x"].Input != 2e6 {
		t.Errorf("merged = %+v", sum)
	}
}

func TestParseTableRejects(t *testing.T) {
	for name, doc := range map[string]string{
		"not json":    `{`,
		"dated id":    `[{"model":"claude-x-20260101","input":1,"output":1}]`,
		"bad date":    `[{"model":"claude-x","from":"soon","input":1,"output":1}]`,
		"no price":    `[{"model":"claude-x","output":1}]`,
		"negative":    `[{"model":"claude-x","input":1,"output":1,"cache_read":-1}]`,
		"duplicate":   `[{"model":"claude-x","input":1,"output":1},{"model":"claude-x","input":2,"output":2}]`,
		"empty model": `[{"input":1,"output":1}]`,
	} {
		if _, err := ParseTable([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The table sill ships parses, is formatted the way pricegen writes it, and prices the
// models Claude Code uses today.
func TestEmbeddedTable(t *testing.T) {
	rows, err := ParseTable(pricesJSON)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(FormatTable(rows)); got != strings.ReplaceAll(string(pricesJSON), "\r\n", "\n") {
		t.Errorf("prices.json is not in FormatTable's layout; run pricegen or reformat it")
	}
	for _, m := range []string{"claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5"} {
		if !Known(m) {
			t.Errorf("%s has no price", m)
		}
	}
	if len(TableVersion) != 12 {
		t.Errorf("TableVersion = %q", TableVersion)
	}
}

const litellmSample = `{
  "sample_spec": {"mode": "chat"},
  "claude-x": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 4e-06, "output_cost_per_token": 2e-05,
    "cache_creation_input_token_cost": 5e-06, "cache_creation_input_token_cost_above_1hr": 8e-06, "cache_read_input_token_cost": 2e-07},
  "claude-x-20260101": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 4e-06, "output_cost_per_token": 2e-05,
    "cache_creation_input_token_cost": 5e-06, "cache_creation_input_token_cost_above_1hr": 8e-06, "cache_read_input_token_cost": 2e-07},
  "claude-new": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 1e-05, "output_cost_per_token": 5e-05,
    "cache_creation_input_token_cost": 1.25e-05, "cache_creation_input_token_cost_above_1hr": 2e-05, "cache_read_input_token_cost": 2.5e-07},
  "claude-tiered": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 3e-06, "output_cost_per_token": 1.5e-05,
    "input_cost_per_token_above_200k_tokens": 6e-06,
    "cache_creation_input_token_cost": 3.75e-06, "cache_creation_input_token_cost_above_1hr": 6e-06, "cache_read_input_token_cost": 3e-07},
  "claude-old": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 3e-06, "output_cost_per_token": 1.5e-05},
  "bedrock/claude-x": {"litellm_provider": "bedrock", "mode": "chat", "input_cost_per_token": 9e-06, "output_cost_per_token": 9e-05,
    "cache_creation_input_token_cost": 1e-05, "cache_creation_input_token_cost_above_1hr": 1e-05, "cache_read_input_token_cost": 1e-06},
  "gpt-x": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 1e-06}
}`

func TestUpdate(t *testing.T) {
	rows, err := ParseTable([]byte(`[
		{"model":"claude-x","input":5,"output":25,"cache_write_5m":6.25,"cache_write_1h":10,"cache_read":0.5,"fast":2},
		{"model":"claude-gone","input":1,"output":1,"cache_write_5m":1,"cache_write_1h":1,"cache_read":1}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	updated, changes, err := Update(rows, []byte(litellmSample), "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	want := `[
  {"model":"claude-gone","input":1,"output":1,"cache_write_5m":1,"cache_write_1h":1,"cache_read":1},
  {"model":"claude-new","input":10,"output":50,"cache_write_5m":12.5,"cache_write_1h":20,"cache_read":0.25},
  {"model":"claude-x","input":5,"output":25,"cache_write_5m":6.25,"cache_write_1h":10,"cache_read":0.5,"fast":2},
  {"model":"claude-x","from":"2026-10-05","input":4,"output":20,"cache_write_5m":5,"cache_write_1h":8,"cache_read":0.2,"fast":2}
]
`
	if got := string(FormatTable(updated)); got != want {
		t.Errorf("updated table:\n%s\nwant:\n%s", got, want)
	}
	joined := strings.Join(changes, "\n")
	for _, s := range []string{"added claude-new", "changed claude-x from 2026-10-05", "skipped claude-tiered"} {
		if !strings.Contains(joined, s) {
			t.Errorf("changes lack %q:\n%s", s, joined)
		}
	}
	if strings.Contains(joined, "gpt") || strings.Contains(joined, "claude-old") {
		t.Errorf("changes mention models that are not priced:\n%s", joined)
	}

	// Running it again the same day changes nothing.
	again, changes, err := Update(updated, []byte(litellmSample), "2026-10-05")
	if err != nil || len(again) != len(updated) || strings.Contains(strings.Join(changes, "\n"), "changed") {
		t.Errorf("second run: %d rows, %v, %v", len(again), changes, err)
	}
	if _, _, err := Update(rows, []byte(`[`), "2026-10-05"); err == nil {
		t.Error("broken LiteLLM document accepted")
	}
}

// pricegen keeps the file in FormatTable's layout; a checkout with CRLF endings still parses.
func TestPricesFileParsesWithCRLF(t *testing.T) {
	data, err := os.ReadFile("prices.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseTable([]byte(strings.ReplaceAll(string(data), "\n", "\r\n"))); err != nil {
		t.Error(err)
	}
}
