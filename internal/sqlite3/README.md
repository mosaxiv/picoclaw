# internal/sqlite3

This directory contains SQLite initialization and bundled `sqlite-vec` assets for `clawlet`.

## Files

- `sqlitedriver.go`
  - Loads `sqlite-vec.wasm` via `go:embed` and assigns it to `github.com/ncruces/go-sqlite3` (`sqlite3.Binary`).
- `sqlite-vec.wasm`
  - The Wasm binary embedded into the application.
- `build-sqlite-vec-wasm.sh`
  - Script to rebuild `sqlite-vec.wasm`.
- `sqlite-vec.patch`
  - Patch applied to the `go-sqlite3` build output to include and auto-register `sqlite-vec`.

## Rebuild

Requirements:
- `wasi-sdk`
- `binaryen`
- `curl`, `tar`, `patch`

Run:

```bash
./internal/sqlite3/build-sqlite-vec-wasm.sh
```

To override versions explicitly:

```bash
GO_SQLITE3_VERSION=v0.30.5 SQLITE_VEC_VERSION=v0.1.6 ./internal/sqlite3/build-sqlite-vec-wasm.sh
```

Pinned defaults:
- `GO_SQLITE3_VERSION=v0.30.5`
- `SQLITE_VEC_VERSION=v0.1.6`
