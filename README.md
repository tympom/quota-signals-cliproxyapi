# quota-signals-cliproxyapi

A [CLIProxyAPI](https://help.router-for.me/plugin/development) plugin showing per-provider request counts and the latest Claude and Codex 5-hour and weekly rate-limit windows, as a Management Center panel and as JSON for status lines. It makes no upstream calls: readings come from completed requests, are held in memory and clear on restart.

## Install

```yaml
plugins:
  enabled: true
  store-sources:
    - "https://raw.githubusercontent.com/tympom/quota-signals-cliproxyapi/main/registry.json"
```

Install **Quota Signals** from the Plugin Store and restart CLIProxyAPI. No configuration.

## Use

- Panel: `GET /v0/resource/plugins/quota-signals/panel`
- JSON: `GET /v0/resource/plugins/quota-signals/quota`

```json
{
  "providers": {
    "claude": { "observed_at": 1791749198,
      "windows": { "5h": { "used_percent": 37, "resets_at": 1791756398 } } }
  },
  "requests": { "claude": { "total": 120, "successful": 118, "failed": 2 } },
  "started_at": 1791740000
}
```

Times are Unix seconds; a missing window means unreported, not zero. Send the client's proxy key (`Authorization: Bearer` or `x-api-key`) to get the account that served that client; otherwise the latest account per provider. Both endpoints are unauthenticated and reveal request volumes, but no keys or account identifiers.

## Build

Tested with CLIProxyAPI `v8.0.38` and Go 1.27.2:

```sh
docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD":/src -w /src golang:1.27.2 \
  sh -c 'go test ./... && CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o dist/quota-signals.so .'
```
