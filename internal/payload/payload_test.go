package payload

import "testing"

func TestFallbacks(t *testing.T) {
	p, err := Parse([]byte(`{"model":{"id":"claude-x"},"cwd":"/tmp/x","workspace":{"git_worktree":"wt1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.ModelName() != "claude-x" || p.CurrentDir() != "/tmp/x" || p.WorktreeName() != "wt1" {
		t.Errorf("fallbacks: %q %q %q", p.ModelName(), p.CurrentDir(), p.WorktreeName())
	}

	p, _ = Parse([]byte(`{"model":{"id":"claude-x","display_name":"X"},"cwd":"/tmp/x","workspace":{"current_dir":"/w","git_worktree":"a"},"worktree":{"name":"b"}}`))
	if p.ModelName() != "X" || p.CurrentDir() != "/w" || p.WorktreeName() != "b" {
		t.Errorf("preferred fields: %q %q %q", p.ModelName(), p.CurrentDir(), p.WorktreeName())
	}
}

func TestContextPercent(t *testing.T) {
	p, _ := Parse([]byte(`{"context_window":{"total_input_tokens":50000,"context_window_size":200000}}`))
	if v := p.ContextPercent(); v == nil || *v != 25 {
		t.Errorf("computed = %v", v)
	}
	p, _ = Parse([]byte(`{"context_window":{"used_percentage":61.2,"total_input_tokens":1,"context_window_size":2}}`))
	if v := p.ContextPercent(); v == nil || *v != 61.2 {
		t.Errorf("pre-calculated = %v", v)
	}
	p, _ = Parse([]byte(`{"context_window":{"used_percentage":null}}`))
	if v := p.ContextPercent(); v != nil {
		t.Errorf("null should stay nil, got %v", *v)
	}
	p, _ = Parse([]byte(`{"effort":null,"rate_limits":null,"prompt_cache":null}`))
	if p.Effort.Level != "" || p.RateLimits.FiveHour != nil || p.PromptCache != nil {
		t.Error("nulls should decode to zero values")
	}
}

func TestParseError(t *testing.T) {
	if _, err := Parse([]byte(`{`)); err == nil {
		t.Error("truncated JSON accepted")
	}
}
