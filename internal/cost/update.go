package cost

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

// LiteLLMURL is the community-maintained price list the table is refreshed from.
const LiteLLMURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// litellmModel is the part of a LiteLLM entry the table uses, in dollars per token.
type litellmModel struct {
	Provider     string   `json:"litellm_provider"`
	Mode         string   `json:"mode"`
	Input        *float64 `json:"input_cost_per_token"`
	Output       *float64 `json:"output_cost_per_token"`
	CacheWrite5m *float64 `json:"cache_creation_input_token_cost"`
	CacheWrite1h *float64 `json:"cache_creation_input_token_cost_above_1hr"`
	CacheRead    *float64 `json:"cache_read_input_token_cost"`
}

// Update brings a table in line with LiteLLM's list. Rows are only ever added: a new model
// gets a row without a date, a changed price a row dated today, which the reviewer
// corrects to the day the change took effect. Models LiteLLM prices by request size (a
// premium past 200k tokens) are left out, so their tokens show unpriced instead of wrong.
// The fast multiplier is not in LiteLLM and carries over from the previous row. Changes
// lists what happened, one line each, for the pull request.
func Update(rows []Row, litellm []byte, today string) (updated []Row, changes []string, err error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(litellm, &raw); err != nil {
		return nil, nil, err
	}
	fresh := map[string]Row{}
	conflict := map[string]bool{}
	for key, doc := range raw {
		var m litellmModel
		if json.Unmarshal(doc, &m) != nil || m.Provider != "anthropic" || m.Mode != "chat" {
			continue
		}
		model := Normalize(key)
		if !strings.HasPrefix(model, "claude-") || m.Input == nil || m.Output == nil || m.CacheRead == nil ||
			m.CacheWrite5m == nil || m.CacheWrite1h == nil {
			continue
		}
		if strings.Contains(string(doc), "above_200k") {
			changes = append(changes, "skipped "+key+": priced by request size")
			conflict[model] = true
			continue
		}
		r := Row{
			Model:        model,
			Input:        perMillion(*m.Input),
			Output:       perMillion(*m.Output),
			CacheWrite5m: perMillion(*m.CacheWrite5m),
			CacheWrite1h: perMillion(*m.CacheWrite1h),
			CacheRead:    perMillion(*m.CacheRead),
		}
		// Dated snapshots normalize to the same id; they must agree.
		if prev, seen := fresh[model]; seen && !samePrices(prev, r) {
			conflict[model] = true
			changes = append(changes, "skipped "+model+": its snapshots have different prices")
		}
		fresh[model] = r
	}

	updated = slices.Clone(rows)
	models := make([]string, 0, len(fresh))
	for m := range fresh {
		models = append(models, m)
	}
	slices.Sort(models)
	for _, model := range models {
		if conflict[model] {
			continue
		}
		r := fresh[model]
		last, ok := latest(rows, model)
		switch {
		case !ok:
			updated = append(updated, r)
			changes = append(changes, fmt.Sprintf("added %s: %s", model, describe(r)))
		case !samePrices(last, r):
			r.From, r.Fast = today, last.Fast
			if last.From == today {
				return nil, nil, fmt.Errorf("%s already has a row from %s", model, today)
			}
			updated = append(updated, r)
			changes = append(changes, fmt.Sprintf("changed %s from %s: was %s, now %s", model, today, describe(last), describe(r)))
		}
	}
	sortRows(updated)
	slices.Sort(changes)
	return updated, slices.Compact(changes), nil
}

func latest(rows []Row, model string) (Row, bool) {
	var found Row
	ok := false
	for _, r := range rows {
		if r.Model == model && (!ok || r.From > found.From) {
			found, ok = r, true
		}
	}
	return found, ok
}

func samePrices(a, b Row) bool {
	return a.Input == b.Input && a.Output == b.Output && a.CacheRead == b.CacheRead &&
		a.CacheWrite5m == b.CacheWrite5m && a.CacheWrite1h == b.CacheWrite1h
}

func describe(r Row) string {
	return fmt.Sprintf("in %g, out %g, cache write %g (5m) %g (1h), cache read %g",
		r.Input, r.Output, r.CacheWrite5m, r.CacheWrite1h, r.CacheRead)
}

// perMillion turns dollars per token into dollars per million, without the float noise
// (1e-05 * 1e6 is 10.000000000000002).
func perMillion(perToken float64) float64 {
	return math.Round(perToken*1e6*1e6) / 1e6
}

// FormatTable writes the table one row per line, so a price change is a one-line diff.
func FormatTable(rows []Row) []byte {
	var b strings.Builder
	b.WriteString("[\n")
	for i, r := range rows {
		line, _ := json.Marshal(r)
		b.WriteString("  ")
		b.Write(line)
		if i < len(rows)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("]\n")
	return []byte(b.String())
}
