# quota-signals-cliproxyapi

Include parent `AGENTS.md`.

## Releases

- Every release gets a new version; never re-cut or move a published tag (Plugin Store clarity).
  Bump `registry.json` to the tag's version in the same change.
- Build each release against the newest CLIProxyAPI SDK release: run
  `go get github.com/router-for-me/CLIProxyAPI/v8@latest` (check for a newer major first; it
  changes the import path), then update the tested version in `README.md`.
- No Go on conductor: build and test in a throwaway `golang:<go.mod version>` container.
