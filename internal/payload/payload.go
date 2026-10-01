// Package payload decodes the JSON Claude Code pipes to a status line command.
package payload

import "encoding/json"

// Payload is the status line input. Only the fields sill uses are declared; the full
// schema is embedded in the Claude Code binary (search it for "Pre-calculated: % of
// context used").
type Payload struct {
	SessionName    string `json:"session_name"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	Version        string `json:"version"`
	Model          struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Workspace struct {
		CurrentDir  string `json:"current_dir"`
		GitWorktree string `json:"git_worktree"`
		Repo        struct {
			Owner string `json:"owner"`
			Name  string `json:"name"`
		} `json:"repo"`
	} `json:"workspace"`
	ContextWindow struct {
		TotalInputTokens  int      `json:"total_input_tokens"`
		ContextWindowSize int      `json:"context_window_size"`
		UsedPercentage    *float64 `json:"used_percentage"`
	} `json:"context_window"`
	Effort struct {
		Level string `json:"level"`
	} `json:"effort"`
	OutputStyle struct {
		Name string `json:"name"`
	} `json:"output_style"`
	RateLimits struct {
		FiveHour   *LimitWindow `json:"five_hour"`
		SevenDay   *LimitWindow `json:"seven_day"`
		SpendLimit *LimitWindow `json:"spend_limit"`
	} `json:"rate_limits"`
	PromptCache *PromptCache `json:"prompt_cache"`
	PR          struct {
		Number      int    `json:"number"`
		ReviewState string `json:"review_state"`
		Kind        string `json:"kind"`
	} `json:"pr"`
	Worktree struct {
		Name string `json:"name"`
	} `json:"worktree"`
}

// LimitWindow is one subscription or gateway budget window.
type LimitWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       float64  `json:"resets_at"`
}

// PromptCache is the prompt cache health Claude Code reports after the first response.
type PromptCache struct {
	Warm            bool     `json:"warm"`
	CachingObserved bool     `json:"caching_observed"`
	ExpiresAt       *float64 `json:"expires_at"`
}

// Parse decodes a payload.
func Parse(data []byte) (*Payload, error) {
	var p Payload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ModelName is the display name, or the model id when there is none.
func (p *Payload) ModelName() string {
	if p.Model.DisplayName != "" {
		return p.Model.DisplayName
	}
	return p.Model.ID
}

// CurrentDir is the workspace directory, or the legacy cwd field.
func (p *Payload) CurrentDir() string {
	if p.Workspace.CurrentDir != "" {
		return p.Workspace.CurrentDir
	}
	return p.Cwd
}

// WorktreeName is the worktree slug from either place Claude Code reports it.
func (p *Payload) WorktreeName() string {
	if p.Worktree.Name != "" {
		return p.Worktree.Name
	}
	return p.Workspace.GitWorktree
}

// ContextPercent is the context window used, computed from token counts when the
// pre-calculated value is absent. It is nil before the first message.
func (p *Payload) ContextPercent() *float64 {
	cw := p.ContextWindow
	if cw.UsedPercentage != nil {
		return cw.UsedPercentage
	}
	if cw.TotalInputTokens > 0 && cw.ContextWindowSize > 0 {
		v := 100 * float64(cw.TotalInputTokens) / float64(cw.ContextWindowSize)
		return &v
	}
	return nil
}
