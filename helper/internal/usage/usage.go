// Package usage reports how much of each AI subscription's rate limits is
// used, for the side panel of network displays:
//
//   - Claude: GET https://api.anthropic.com/api/oauth/usage with the OAuth
//     token Claude Code keeps in ~/.claude/.credentials.json (the numbers
//     Claude Code's /usage shows). The token is read on every poll, never
//     refreshed or stored here: Claude Code refreshes it.
//   - Codex: the rate_limits Codex writes into its newest session log, so no
//     network call.
//
// Muse Code keeps its numbers in the running process only, so it is absent.
package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tonylook/managents/helper/internal/detect"
)

// Window is one rate-limit window.
type Window struct {
	Label    string `json:"label"`    // "5h", "Week", ...
	Used     int    `json:"used"`     // percent, may exceed 100
	ResetsAt int64  `json:"resetsAt"` // UNIX seconds, 0 if unknown
}

// Provider is one subscription.
type Provider struct {
	Kind    string   `json:"kind"` // claude, codex
	Plan    string   `json:"plan,omitempty"`
	Windows []Window `json:"windows"`
	AsOf    int64    `json:"asOf"` // UNIX seconds the numbers were observed
}

// Poller refreshes every provider in the background.
type Poller struct {
	Home   string
	Client *http.Client
	// Interval between Claude API calls; Codex logs are read every poll.
	Interval time.Duration

	mu     sync.Mutex
	claude *Provider
	last   time.Time
}

// NewPoller returns a Poller for the user whose home directory is home.
func NewPoller(home string) *Poller {
	return &Poller{Home: home, Client: &http.Client{Timeout: 10 * time.Second}, Interval: time.Minute}
}

// Snapshot returns the latest numbers of every provider that has any.
func (p *Poller) Snapshot(ctx context.Context, now time.Time) []Provider {
	var out []Provider
	if c := p.claudeUsage(ctx, now); c != nil {
		out = append(out, *c)
	}
	if c := p.codexUsage(); c != nil {
		out = append(out, *c)
	}
	return out
}

// --- Claude ------------------------------------------------------------------

func (p *Poller) claudeUsage(ctx context.Context, now time.Time) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.last.IsZero() && now.Sub(p.last) < p.Interval {
		return p.claude
	}
	p.last = now
	fresh, err := p.fetchClaude(ctx, now)
	if err == nil {
		p.claude = fresh
	}
	return p.claude
}

type claudeWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

func (p *Poller) fetchClaude(ctx context.Context, now time.Time) (*Provider, error) {
	data, err := os.ReadFile(filepath.Join(p.Home, ".claude", ".credentials.json"))
	if err != nil {
		return nil, err
	}
	var creds struct {
		OAuth struct {
			AccessToken      string `json:"accessToken"`
			SubscriptionType string `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil || creds.OAuth.AccessToken == "" {
		return nil, errors.New("no Claude login")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.OAuth.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	res, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New(res.Status)
	}
	var body struct {
		FiveHour *claudeWindow `json:"five_hour"`
		SevenDay *claudeWindow `json:"seven_day"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := &Provider{Kind: "claude", Plan: creds.OAuth.SubscriptionType, AsOf: now.Unix()}
	for _, w := range []struct {
		label string
		win   *claudeWindow
	}{{"5h", body.FiveHour}, {"Week", body.SevenDay}} {
		if w.win == nil || w.win.Utilization == nil {
			continue
		}
		win := Window{Label: w.label, Used: int(*w.win.Utilization + 0.5)}
		if t, err := time.Parse(time.RFC3339Nano, w.win.ResetsAt); err == nil {
			win.ResetsAt = t.Unix()
		}
		out.Windows = append(out.Windows, win)
	}
	return out, nil
}

// --- Codex -------------------------------------------------------------------

type codexLimit struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

func (p *Poller) codexUsage() *Provider {
	for _, path := range newestCodexLogs(filepath.Join(p.Home, ".codex", "sessions"), 3) {
		data, _, err := detect.ReadTail(path, 1<<20)
		if err != nil {
			continue
		}
		i := bytes.LastIndex(data, []byte(`"rate_limits":{`))
		if i < 0 {
			continue
		}
		info, _ := os.Stat(path)
		var limits struct {
			Primary   *codexLimit `json:"primary"`
			Secondary *codexLimit `json:"secondary"`
			PlanType  string      `json:"plan_type"`
		}
		dec := json.NewDecoder(bytes.NewReader(data[i+len(`"rate_limits":`):]))
		if dec.Decode(&limits) != nil {
			continue
		}
		out := &Provider{Kind: "codex", Plan: limits.PlanType}
		if info != nil {
			out.AsOf = info.ModTime().Unix()
		}
		for _, l := range []*codexLimit{limits.Primary, limits.Secondary} {
			if l == nil || l.WindowMinutes <= 0 {
				continue
			}
			out.Windows = append(out.Windows, Window{Label: windowLabel(l.WindowMinutes), Used: int(l.UsedPercent + 0.5), ResetsAt: l.ResetsAt})
		}
		// Shortest window first, like Claude's.
		sort.SliceStable(out.Windows, func(a, b int) bool { return out.Windows[a].Label == "5h" && out.Windows[b].Label != "5h" })
		if len(out.Windows) > 0 {
			return out
		}
	}
	return nil
}

func windowLabel(minutes int) string {
	switch {
	case minutes == 10080:
		return "Week"
	case minutes%1440 == 0:
		return itoa(minutes/1440) + "d"
	case minutes%60 == 0:
		return itoa(minutes/60) + "h"
	}
	return itoa(minutes) + "m"
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// newestCodexLogs returns up to n rollout files under dir, newest first.
func newestCodexLogs(dir string, n int) []string {
	type entry struct {
		path string
		mod  time.Time
	}
	var files []entry
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files = append(files, entry{path, info.ModTime()})
		}
		return nil
	})
	sort.Slice(files, func(a, b int) bool { return files[a].mod.After(files[b].mod) })
	var out []string
	for i := 0; i < len(files) && i < n; i++ {
		out = append(out, files[i].path)
	}
	return out
}
