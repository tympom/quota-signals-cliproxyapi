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
