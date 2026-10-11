# quota-signals-cliproxyapi

A [CLIProxyAPI](https://help.router-for.me/plugin/development) plugin that shows the latest Claude and Codex 5-hour and weekly rate-limit windows in the Management Center and serves them as JSON for client status lines.

It makes no upstream calls. CLIProxyAPI hands every completed request's upstream response headers to usage plugins; this plugin keeps the windows they carry, per proxy account, in memory:

- Claude: `anthropic-ratelimit-unified-{5h,7d}-utilization` (a 0..1 fraction) and `-reset`.
- Codex: `x-codex-{primary,secondary}-used-percent`, classified by `-window-minutes` (300 is 5h, 10080 is 7d; other lengths are skipped), with `-reset-at` or `-reset-after-seconds`.

A reading is only as fresh as the last request that account served, and a proxy restart empties it until the next request.

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

The Plugin Store shows a quota-gauge icon; the installed plugin serves the same icon locally. Open **Quota Signals** in the Management Center's plugin menu, or visit:

```
GET /v0/resource/plugins/quota-signals/panel
```

The panel shows used percentages, reported reset times and each provider's last observation time. Missing providers/windows are shown as unreported, not as zero usage. Past reset times are marked as passed; usage is not assumed to have reset without new headers.

**Refresh** reads the existing local snapshot without contacting providers. The panel uses the latest account observed per provider, with no client-key filtering; the JSON endpoint below still supports client-specific readings. It follows the Management Center's light/dark theme and falls back to the browser's preference when opened directly.

Like the JSON endpoint, the panel and icon are unauthenticated plugin resources. They display no keys or account identities.

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
  }
}
```

- `used_percent` is 0..100; `observed_at` and `resets_at` are Unix seconds. `resets_at` is omitted when upstream did not report it.
- The route is a plugin resource and is **not authenticated**. It returns only percentages and times: no keys, emails or account identifiers. Expose the proxy accordingly.
- With several accounts per provider, send the proxy API key the client uses for inference (`Authorization: Bearer <key>` or `x-api-key`) to get the account that served that client's last request. Without a key, or for a provider the key has not used yet, the response describes the account that served any client last. The plugin keeps only SHA-256 hashes of client keys.

## Build

Built and tested against CLIProxyAPI `v8.0.23` with Go 1.27.2. Release builds come from `.github/workflows/build.yml` on a `v*` tag (Linux amd64 and arm64). Locally, without Go installed:

```sh
docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD":/src -w /src golang:1.27.2 \
  sh -c 'go test ./... && CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o dist/quota-signals.so .'
```
