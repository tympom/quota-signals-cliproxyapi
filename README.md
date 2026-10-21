# quota-signals-cliproxyapi

A [CLIProxyAPI](https://help.router-for.me/plugin/development) plugin that shows per-provider request counts and the latest Claude and Codex 5-hour and weekly rate-limit windows in the Management Center, and serves them as JSON for client status lines.

No upstream calls: readings come from completed requests and are stored per proxy account in memory.

Readings are only as fresh as that account's last request. Readings and counts clear on plugin restart; prior traffic is not reconstructed.

## Install

Add the registry to the CLIProxyAPI config, with plugins enabled:

```yaml
plugins:
  enabled: true
  store-sources:
    - "https://raw.githubusercontent.com/tympom/quota-signals-cliproxyapi/main/registry.json"
```

Then install **Quota Signals** from the Management Center's Plugin Store page and restart CLIProxyAPI. The plugin has no configuration.

## Interface

Open **Quota Signals** in the Management Center's plugin menu, or visit:

```
GET /v0/resource/plugins/quota-signals/panel
```

The panel shows provider request counts **Since plugin start**, with the start time. Claude/Codex cards show used percentages, reported reset times and the last quota observation; other providers have counters only. Missing windows are unreported, not zero. Passed reset times do not imply usage has reset without new headers.

**Refresh** reads the local snapshot. The panel selects the latest account per provider without client-key filtering; JSON supports client-specific readings below. It follows the Management Center theme, or browser preference when opened directly.

## Endpoint

```
GET /v0/resource/plugins/quota-signals/quota
```

```json
{
  "providers": {
    "claude": {
      "observed_at": 1791749198,
      "windows": {
        "5h": { "used_percent": 37, "resets_at": 1791756398 },
        "7d": { "used_percent": 61, "resets_at": 1792049198 }
      }
    },
    "codex": { "observed_at": 1791749198, "windows": { "5h": { "used_percent": 12, "resets_at": 1791752798 } } }
  },
  "requests": {
    "claude": { "total": 120, "successful": 118, "failed": 2 },
    "codex": { "total": 54, "successful": 53, "failed": 1 },
    "gemini": { "total": 8, "successful": 8, "failed": 0 }
  },
  "started_at": 1791740000
}
```

- `used_percent` is 0..100; `observed_at` and `resets_at` are Unix seconds. `resets_at` is omitted when upstream did not report it.
- `requests` counts completed usage records across all accounts and clients, including failures and records without quota headers; `total = successful + failed`. Retries can count separately: totals need not equal unique inbound requests or dashboard totals. Before traffic, `providers` and `requests` are empty objects; the panel shows zero Claude/Codex counts.
- `started_at` is the plugin start time in Unix seconds. Counts are cumulative until restart, independent of quota resets. Reading either interface does not increment them.
- Panel, JSON and icon are **unauthenticated** plugin resources. They expose no keys, emails or account identifiers, but panel/JSON reveal aggregate request volumes. Expose the proxy accordingly.
- With several accounts per provider, send the proxy API key the client uses for inference (`Authorization: Bearer <key>` or `x-api-key`) to get the account that served that client's last request. Without a key, or for a provider the key has not used yet, the response describes the account that served any client last. The plugin keeps only SHA-256 hashes of client keys.

## Build

Built and tested against CLIProxyAPI `v8.0.38` with Go 1.27.3. Available for Linux amd64 and arm64. To build locally without Go installed:

```sh
docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD":/src -w /src golang:1.27.3 \
  sh -c 'go test ./... && CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o dist/quota-signals.so .'
```
