package quota

import (
	"net/http"
	"testing"
	"time"
)

var t0 = time.Unix(1_800_000_000, 0)

func claudeHeaders(util5h, util7d string) http.Header {
	return http.Header{
		"Anthropic-Ratelimit-Unified-5h-Utilization": {util5h},
		"Anthropic-Ratelimit-Unified-5h-Reset":       {"1800018000"},
		"Anthropic-Ratelimit-Unified-7d-Utilization": {util7d},
		"Anthropic-Ratelimit-Unified-7d-Reset":       {"2026-10-16T23:59:59Z"},
	}
}

func TestClaudeUtilizationIsAFraction(t *testing.T) {
	s := NewStore()
	s.Observe(Observation{Provider: "claude", AuthIndex: "a", Headers: claudeHeaders("0.52", "1.3"), At: t0})
	got := s.Snapshot("")["claude"].Windows
	if got["5h"].UsedPercent != 52 || got["5h"].ResetsAt != 1800018000 {
		t.Fatalf("5h = %+v", got["5h"])
	}
	// Over-limit utilization clamps; RFC 3339 resets parse.
	if got["7d"].UsedPercent != 100 || got["7d"].ResetsAt != time.Date(2026, 10, 16, 23, 59, 59, 0, time.UTC).Unix() {
		t.Fatalf("7d = %+v", got["7d"])
	}
}

func TestCodexWindowsClassifiedByLength(t *testing.T) {
	s := NewStore()
	// Lower-case names as merged from websocket quota events; windows deliberately swapped.
	s.Observe(Observation{Provider: "codex", AuthIndex: "c", At: t0, Headers: http.Header{
		"x-codex-primary-used-percent":          {"40"},
		"x-codex-primary-window-minutes":        {"10080"},
		"x-codex-primary-reset-at":              {"1800500000"},
		"x-codex-secondary-used-percent":        {"7.5"},
		"x-codex-secondary-window-minutes":      {"300"},
		"x-codex-secondary-reset-after-seconds": {"600"},
	}})
	got := s.Snapshot("")["codex"].Windows
	if got["7d"] != (Window{UsedPercent: 40, ResetsAt: 1800500000}) {
		t.Fatalf("7d = %+v", got["7d"])
	}
	if got["5h"] != (Window{UsedPercent: 7.5, ResetsAt: t0.Unix() + 600}) {
		t.Fatalf("5h = %+v", got["5h"])
	}
}

func TestCodexSkipsWindowsThatAreNot5hOr7d(t *testing.T) {
	s := NewStore()
	s.Observe(Observation{Provider: "codex", AuthIndex: "c", At: t0, Headers: http.Header{
		"X-Codex-Primary-Used-Percent":     {"10"},
		"X-Codex-Secondary-Used-Percent":   {"90"},
		"X-Codex-Secondary-Window-Minutes": {"43200"},
	}})
	got := s.Snapshot("")["codex"].Windows
	if _, monthly := got["7d"]; monthly || got["5h"].UsedPercent != 10 {
		t.Fatalf("windows = %+v", got)
	}
}

func TestClientSeesTheAccountThatServedIt(t *testing.T) {
	s := NewStore()
	s.Observe(Observation{Provider: "claude", AuthIndex: "omp-acct", ClientKey: "omp-key", Headers: claudeHeaders("0.10", "0.20"), At: t0})
	s.Observe(Observation{Provider: "claude", AuthIndex: "other-acct", ClientKey: "zed-key", Headers: claudeHeaders("0.90", "0.95"), At: t0.Add(time.Minute)})

	if got := s.Snapshot("omp-key")["claude"].Windows["5h"].UsedPercent; got != 10 {
		t.Fatalf("omp sees %v%%, want its own account's 10%%", got)
	}
	// Unknown or absent keys fall back to whichever account served last.
	for _, key := range []string{"", "never-seen"} {
		if got := s.Snapshot(key)["claude"].Windows["5h"].UsedPercent; got != 90 {
			t.Fatalf("key %q sees %v%%, want latest account's 90%%", key, got)
		}
	}
}

func TestClientFallsBackPerProviderItHasNotUsed(t *testing.T) {
	s := NewStore()
	s.Observe(Observation{Provider: "claude", AuthIndex: "a", ClientKey: "omp-key", Headers: claudeHeaders("0.10", "0.20"), At: t0})
	s.Observe(Observation{Provider: "codex", AuthIndex: "c", ClientKey: "other", At: t0, Headers: http.Header{"X-Codex-Primary-Used-Percent": {"33"}}})
	if got := s.Snapshot("omp-key")["codex"].Windows["5h"].UsedPercent; got != 33 {
		t.Fatalf("codex fallback = %v", got)
	}
}

func TestRequestsWithoutQuotaHeadersChangeNothing(t *testing.T) {
	s := NewStore()
	s.Observe(Observation{Provider: "claude", AuthIndex: "a", ClientKey: "k", Headers: claudeHeaders("0.10", "0.20"), At: t0})
	s.Observe(Observation{Provider: "claude", AuthIndex: "b", ClientKey: "k", Headers: http.Header{"Content-Type": {"text/event-stream"}}, At: t0.Add(time.Minute)})
	s.Observe(Observation{Provider: "gemini", AuthIndex: "g", ClientKey: "k", Headers: claudeHeaders("0.99", "0.99"), At: t0.Add(time.Minute)})
	snap := s.Snapshot("k")
	if len(snap) != 1 || snap["claude"].Windows["5h"].UsedPercent != 10 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestLateOlderRequestDoesNotRollBack(t *testing.T) {
	s := NewStore()
	s.Observe(Observation{Provider: "claude", AuthIndex: "a", Headers: claudeHeaders("0.60", "0.20"), At: t0.Add(time.Minute)})
	s.Observe(Observation{Provider: "claude", AuthIndex: "a", Headers: claudeHeaders("0.50", "0.20"), At: t0})
	if got := s.Snapshot("")["claude"].Windows["5h"].UsedPercent; got != 60 {
		t.Fatalf("5h = %v, want newer 60", got)
	}
}
