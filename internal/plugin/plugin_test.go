package plugin

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"

	"quota-signals-cliproxyapi/internal/quota"
)

func managementResponse(t *testing.T, p *Plugin, req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	t.Helper()
	request, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var envelope pluginabi.Envelope
	if err := json.Unmarshal(p.HandleCall(pluginabi.MethodManagementHandle, request), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatalf("management call failed: %+v", envelope.Error)
	}
	var response pluginapi.ManagementResponse
	if err := json.Unmarshal(envelope.Result, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestResourceBoundaries(t *testing.T) {
	p := New()
	for _, path := range []string{ResourcePath, "/panel", "/icon.svg"} {
		t.Run(path, func(t *testing.T) {
			response := managementResponse(t, p, pluginapi.ManagementRequest{Method: http.MethodPost, Path: resourceBasePath + path})
			if response.StatusCode != http.StatusMethodNotAllowed || response.Headers.Get("Allow") != http.MethodGet {
				t.Fatalf("POST %s: status=%d Allow=%q", path, response.StatusCode, response.Headers.Get("Allow"))
			}
		})
	}
	for _, path := range []string{resourceBasePath + "/missing", "/v0/resource/plugins/other/quota"} {
		response := managementResponse(t, p, pluginapi.ManagementRequest{Method: http.MethodGet, Path: path})
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s: status=%d, want 404", path, response.StatusCode)
		}
	}
}

func TestSnapshotClientSelection(t *testing.T) {
	p := New()
	for index, key := range []string{"client-a", "client-b"} {
		p.store.Observe(quota.Observation{
			Provider:  "claude",
			AuthIndex: key,
			ClientKey: key,
			Headers: http.Header{
				"Anthropic-Ratelimit-Unified-5h-Utilization": {[]string{"0.25", "0.80"}[index]},
			},
			At: time.Unix(1791749198+int64(index), 0),
		})
	}
	for _, tc := range []struct {
		name    string
		headers http.Header
		used    float64
	}{
		{name: "panel's unkeyed snapshot", used: 80},
		{name: "bearer client selection", headers: http.Header{"Authorization": {"Bearer client-a"}}, used: 25},
		{name: "api key client selection", headers: http.Header{"X-Api-Key": {"client-a"}}, used: 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := managementResponse(t, p, pluginapi.ManagementRequest{Method: http.MethodGet, Path: resourceBasePath + ResourcePath, Headers: tc.headers})
			if response.StatusCode != http.StatusOK || response.Headers.Get("Content-Type") != "application/json" || response.Headers.Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected snapshot response: status=%d headers=%v", response.StatusCode, response.Headers)
			}
			var snapshot snapshotResponse
			if err := json.Unmarshal(response.Body, &snapshot); err != nil {
				t.Fatal(err)
			}
			if got := snapshot.Providers["claude"].Windows["5h"].UsedPercent; got != tc.used {
				t.Fatalf("used percent=%v, want %v", got, tc.used)
			}
		})
	}
}

func TestRequestCountsIncludeFailuresWithoutQuotaHeaders(t *testing.T) {
	p := New()
	for _, record := range []pluginapi.UsageRecord{
		{Provider: "claude", AuthIndex: "a", APIKey: "client-a", RequestedAt: time.Unix(1791749198, 0), ResponseHeaders: http.Header{"Anthropic-Ratelimit-Unified-5h-Utilization": {"0.25"}}},
		{Provider: " Claude ", AuthIndex: "b", APIKey: "client-b", Failed: true},
		{Provider: "claude", AuthIndex: "a", RequestedAt: time.Unix(1791749197, 0), ResponseHeaders: http.Header{"Anthropic-Ratelimit-Unified-5h-Utilization": {"0.10"}}},
		{Provider: "codex", Failed: true},
		{Provider: "gemini"},
	} {
		request, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		var envelope pluginabi.Envelope
		if err := json.Unmarshal(p.HandleCall(pluginabi.MethodUsageHandle, request), &envelope); err != nil || !envelope.OK {
			t.Fatalf("usage call failed: %v, %+v", err, envelope.Error)
		}
	}
	want := map[string]quota.RequestCounts{
		"claude": {Total: 3, Successful: 2, Failed: 1},
		"codex":  {Total: 1, Failed: 1},
		"gemini": {Total: 1, Successful: 1},
	}
	// Client-key selection affects quotas, never provider-wide counters; reads
	// must not add requests, even when repeatedly polled by a status line.
	for range 10 {
		response := managementResponse(t, p, pluginapi.ManagementRequest{
			Method: http.MethodGet, Path: resourceBasePath + ResourcePath,
			Headers: http.Header{"Authorization": {"Bearer client-a"}},
		})
		var snapshot snapshotResponse
		if err := json.Unmarshal(response.Body, &snapshot); err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Requests) != len(want) {
			t.Fatalf("counts = %+v, want %+v", snapshot.Requests, want)
		}
		for provider, counts := range want {
			if snapshot.Requests[provider] != counts {
				t.Fatalf("%s counts = %+v, want %+v", provider, snapshot.Requests[provider], counts)
			}
		}
		if snapshot.Providers["claude"].Windows["5h"].UsedPercent != 25 {
			t.Fatalf("quota rolled back: %+v", snapshot.Providers)
		}
		if snapshot.StartedAt != p.startedAt.Unix() {
			t.Fatalf("start time = %d, want %d", snapshot.StartedAt, p.startedAt.Unix())
		}
	}
	fresh := New()
	response := managementResponse(t, fresh, pluginapi.ManagementRequest{Method: http.MethodGet, Path: resourceBasePath + ResourcePath})
	var snapshot snapshotResponse
	if err := json.Unmarshal(response.Body, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Requests) != 0 || len(snapshot.Providers) != 0 {
		t.Fatalf("new plugin inherited usage: %+v", snapshot)
	}
}
