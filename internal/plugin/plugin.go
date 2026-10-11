// Package plugin implements the CLIProxyAPI usage observer and browser resources.
package plugin

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"

	"quota-signals-cliproxyapi/internal/quota"
	"quota-signals-cliproxyapi/resources"
)

// Version is set at build time with -ldflags "-X ...plugin.Version=<v>".
var Version = "0.0.0-dev"

// ResourcePath is the route under /v0/resource/plugins/<pluginID>/.
const ResourcePath = "/quota"

const resourceBasePath = "/v0/resource/plugins/quota-signals"

type registration struct {
	SchemaVersion uint32             `json:"schema_version"`
	Metadata      pluginapi.Metadata `json:"metadata"`
	Capabilities  capabilities       `json:"capabilities"`
}

type capabilities struct {
	UsagePlugin   bool `json:"usage_plugin"`
	ManagementAPI bool `json:"management_api"`
}

type managementRegistration struct {
	Resources []resourceRoute `json:"resources"`
}

// resourceRoute mirrors pluginapi.ResourceRoute without its Handler field.
type resourceRoute struct {
	Path        string
	Menu        string
	Description string
}

type snapshotResponse struct {
	Providers map[string]quota.Reading `json:"providers"`
}

// Plugin dispatches RPC methods to a quota.Store.
type Plugin struct {
	store *quota.Store
	now   func() time.Time
}

func New() *Plugin {
	return &Plugin{store: quota.NewStore(), now: time.Now}
}

// HandleCall answers one RPC method with a JSON envelope.
func (p *Plugin) HandleCall(method string, request []byte) []byte {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return ok(registration{
			SchemaVersion: pluginabi.SchemaVersion,
			Metadata: pluginapi.Metadata{
				Name:             "Quota Signals",
				Version:          Version,
				Author:           "tympom",
				GitHubRepository: "https://github.com/tympom/quota-signals-cliproxyapi",
				Logo:             resourceBasePath + "/icon.svg",
			},
			Capabilities: capabilities{UsagePlugin: true, ManagementAPI: true},
		})
	case pluginabi.MethodPluginShutdown:
		return ok(struct{}{})
	case pluginabi.MethodUsageHandle:
		var record pluginapi.UsageRecord
		if err := json.Unmarshal(request, &record); err != nil {
			return Failure("invalid_request", "usage record is not valid JSON")
		}
		at := record.RequestedAt
		if at.IsZero() {
			at = p.now()
		}
		p.store.Observe(quota.Observation{
			Provider:  record.Provider,
			AuthIndex: record.AuthIndex,
			ClientKey: record.APIKey,
			Headers:   record.ResponseHeaders,
			At:        at,
		})
		return ok(struct{}{})
	case pluginabi.MethodManagementRegister:
		return ok(managementRegistration{Resources: []resourceRoute{
			{Path: ResourcePath, Description: "Latest 5h and 7d rate-limit windows from Claude and Codex response headers."},
			{Path: "/panel", Menu: "Quota Signals", Description: "View Claude and Codex quota windows from response headers."},
			{Path: "/icon.svg", Description: "Quota Signals icon."},
		}})
	case pluginabi.MethodManagementHandle:
		var req pluginapi.ManagementRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return Failure("invalid_request", "management request is not valid JSON")
		}
		return ok(p.serveResource(req))
	default:
		return Failure("unknown_method", "unknown method: "+method)
	}
}

func (p *Plugin) serveResource(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	if req.Path != resourceBasePath+ResourcePath && req.Path != resourceBasePath+"/icon.svg" && req.Path != resourceBasePath+"/panel" {
		return pluginapi.ManagementResponse{StatusCode: http.StatusNotFound}
	}
	if req.Method != http.MethodGet {
		return pluginapi.ManagementResponse{
			StatusCode: http.StatusMethodNotAllowed,
			Headers:    http.Header{"Allow": {http.MethodGet}},
		}
	}
	if req.Path == resourceBasePath+ResourcePath {
		return p.serveSnapshot(req)
	}
	if req.Path == resourceBasePath+"/panel" {
		return pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers: http.Header{
				"Content-Type":  {"text/html; charset=utf-8"},
				"Cache-Control": {"no-store"},
			},
			Body: resources.QuotaPage,
		}
	}
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": {"image/svg+xml"}},
		Body:       resources.Icon,
	}
}

// serveSnapshot answers GET /quota. The caller may present its proxy API key
// (Authorization: Bearer or x-api-key) to get the accounts that served its own
// requests; the response never contains keys or account identities.
func (p *Plugin) serveSnapshot(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	body, err := json.Marshal(snapshotResponse{Providers: p.store.Snapshot(clientKey(req.Headers))})
	if err != nil {
		return pluginapi.ManagementResponse{StatusCode: http.StatusInternalServerError}
	}
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers: http.Header{
			"Content-Type":  {"application/json"},
			"Cache-Control": {"no-store"},
		},
		Body: body,
	}
}

func clientKey(headers http.Header) string {
	if bearer, found := strings.CutPrefix(headers.Get("Authorization"), "Bearer "); found {
		return strings.TrimSpace(bearer)
	}
	return strings.TrimSpace(headers.Get("X-Api-Key"))
}

func ok(result any) []byte {
	raw, err := json.Marshal(result)
	if err != nil {
		return Failure("plugin_error", "encode result")
	}
	envelope, _ := json.Marshal(pluginabi.Envelope{OK: true, Result: raw})
	return envelope
}

// Failure builds an error envelope; the cgo glue also uses it for transport faults.
func Failure(code, message string) []byte {
	envelope, _ := json.Marshal(pluginabi.Envelope{Error: &pluginabi.Error{Code: code, Message: message}})
	return envelope
}
