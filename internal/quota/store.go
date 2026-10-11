// Package quota keeps the latest 5-hour and weekly rate-limit windows that
// Claude and Codex report in their response headers, per proxy account, and
// remembers which account last served each client key.
//
// Nothing here calls upstream: CLIProxyAPI hands every completed request's
// upstream headers to usage plugins, so a reading is only as fresh as the
// last request that account served.
package quota

import (
	"crypto/sha256"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Window is one rate-limit window: percent used and when it resets.
type Window struct {
	UsedPercent float64 `json:"used_percent"`
	// ResetsAt is Unix seconds; zero when upstream did not report it.
	ResetsAt int64 `json:"resets_at,omitempty"`
}

// Reading is the latest set of windows observed for one account.
type Reading struct {
	// ObservedAt is Unix seconds of the request that carried these headers.
	ObservedAt int64             `json:"observed_at"`
	Windows    map[string]Window `json:"windows"`
}

// Observation is the subset of a CLIProxyAPI usage record this package reads.
type Observation struct {
	Provider  string
	AuthIndex string
	// ClientKey is the client API key that made the request, as the proxy
	// recorded it. Only its hash is kept.
	ClientKey string
	Headers   http.Header
	At        time.Time
}

type accountKey struct {
	provider  string
	authIndex string
}

type keyHash [sha256.Size]byte

// Store is safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	accounts map[accountKey]Reading
	// clients maps a client key hash to the account that last served it, per provider.
	clients map[keyHash]map[string]accountKey
	// latest is the account that last served any client, per provider.
	latest map[string]accountKey
}

func NewStore() *Store {
	return &Store{
		accounts: make(map[accountKey]Reading),
		clients:  make(map[keyHash]map[string]accountKey),
		latest:   make(map[string]accountKey),
	}
}

// Observe records the windows carried by one request's upstream headers.
// Requests without quota headers (other providers, failures before upstream)
// change nothing, so they cannot repoint a client at an account with no data.
func (s *Store) Observe(o Observation) {
	provider := strings.ToLower(strings.TrimSpace(o.Provider))
	var windows map[string]Window
	switch provider {
	case "claude":
		windows = parseClaude(o.Headers)
	case "codex":
		windows = parseCodex(o.Headers, o.At)
	}
	if len(windows) == 0 {
		return
	}
	account := accountKey{provider: provider, authIndex: o.AuthIndex}

	s.mu.Lock()
	defer s.mu.Unlock()
	previous, seen := s.accounts[account]
	if seen && previous.ObservedAt > o.At.Unix() {
		// An older request finishing late must not roll the windows back.
		return
	}
	s.accounts[account] = Reading{ObservedAt: o.At.Unix(), Windows: windows}
	s.latest[provider] = account
	if o.ClientKey != "" {
		hash := sha256.Sum256([]byte(o.ClientKey))
		served := s.clients[hash]
		if served == nil {
			served = make(map[string]accountKey)
			s.clients[hash] = served
		}
		served[provider] = account
	}
}

// Snapshot returns, per provider, the windows of the account that last served
// clientKey. Providers that client has not used yet fall back to the account
// that last served any client; an empty clientKey always gets that fallback.
func (s *Store) Snapshot(clientKey string) map[string]Reading {
	s.mu.Lock()
	defer s.mu.Unlock()
	var served map[string]accountKey
	if clientKey != "" {
		served = s.clients[sha256.Sum256([]byte(clientKey))]
	}
	out := make(map[string]Reading, len(s.latest))
	for provider, account := range s.latest {
		if own, ok := served[provider]; ok {
			account = own
		}
		if reading, ok := s.accounts[account]; ok {
			out[provider] = reading
		}
	}
	return out
}

// lowerHeaders flattens headers to lower-case names with their first value;
// upstream and Codex event headers do not agree on canonical casing.
func lowerHeaders(headers http.Header) map[string]string {
	out := make(map[string]string, len(headers))
	for name, values := range headers {
		if len(values) > 0 {
			out[strings.ToLower(name)] = strings.TrimSpace(values[0])
		}
	}
	return out
}

// parseClaude reads anthropic-ratelimit-unified-{5h,7d}-utilization, a 0..1
// fraction, and the matching -reset (Unix seconds or a timestamp).
func parseClaude(headers http.Header) map[string]Window {
	h := lowerHeaders(headers)
	windows := make(map[string]Window, 2)
	for _, id := range []string{"5h", "7d"} {
		prefix := "anthropic-ratelimit-unified-" + id + "-"
		fraction, ok := parseNumber(h[prefix+"utilization"])
		if !ok {
			continue
		}
		// Rounded to 0.01 so 0.52 reads as 52, not 52.00000000000001.
		percent := math.Round(fraction*10000) / 100
		windows[id] = Window{UsedPercent: clampPercent(percent), ResetsAt: parseInstant(h[prefix+"reset"])}
	}
	return windows
}

// Codex window lengths in minutes; any other length (e.g. a monthly team
// window) is not a 5h or 7d bar and is skipped.
const (
	fiveHourMinutes = 300
	weekMinutes     = 7 * 24 * 60
)

// parseCodex reads x-codex-{primary,secondary}-used-percent (0..100) and
// classifies each window by -window-minutes. Without a window length, primary
// is the 5-hour window and secondary the weekly one, as Codex orders them.
func parseCodex(headers http.Header, at time.Time) map[string]Window {
	h := lowerHeaders(headers)
	windows := make(map[string]Window, 2)
	for _, slot := range []struct{ name, fallback string }{{"primary", "5h"}, {"secondary", "7d"}} {
		prefix := "x-codex-" + slot.name + "-"
		used, ok := parseNumber(h[prefix+"used-percent"])
		if !ok {
			continue
		}
		id := slot.fallback
		if minutes, ok := parseNumber(h[prefix+"window-minutes"]); ok {
			switch int64(minutes) {
			case fiveHourMinutes:
				id = "5h"
			case weekMinutes:
				id = "7d"
			default:
				continue
			}
		}
		if _, taken := windows[id]; taken {
			continue
		}
		resetsAt := parseInstant(h[prefix+"reset-at"])
		if resetsAt == 0 {
			if after, ok := parseNumber(h[prefix+"reset-after-seconds"]); ok && after >= 0 {
				resetsAt = at.Unix() + int64(after)
			}
		}
		windows[id] = Window{UsedPercent: clampPercent(used), ResetsAt: resetsAt}
	}
	return windows
}

func parseNumber(raw string) (float64, bool) {
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

// parseInstant accepts Unix seconds or an RFC 3339 / HTTP date; zero if neither.
func parseInstant(raw string) int64 {
	if seconds, ok := parseNumber(raw); ok && seconds > 0 {
		return int64(seconds)
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Unix()
	}
	if t, err := http.ParseTime(raw); err == nil {
		return t.Unix()
	}
	return 0
}

func clampPercent(value float64) float64 {
	return math.Max(0, math.Min(100, value))
}
