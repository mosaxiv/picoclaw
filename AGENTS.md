# Repository Guidelines

## Project Structure

- `cmd/clawlet/`: CLI entrypoint (`clawlet agent`, `gateway`, `onboard`, `cron`, etc.)
- Core modules: `agent/`, `tools/`, `llm/`, `channels/`, `bus/`, `session/`, `memory/`, `skills/`
- Config and state helpers: `config/`, `paths/`
- Tests live next to code as `*_test.go` (e.g. `tools/`, `config/`, `heartbeat/`).

## Build, Test, and Development Commands

```bash
# Build a local binary
go build -o clawlet ./cmd/clawlet

# Run from source
go run ./cmd/clawlet --help
go run ./cmd/clawlet agent -m "hello"

# Test and basic static checks
go fix ./...
go test ./...
go vet ./...
```

## Coding Style & Naming

- Go formatting is mandatory: run `gofmt` on touched files.
- Prefer small, readable functions and explicit error handling.
- Naming: packages are lowercase; exported identifiers are `PascalCase`; unexported are `camelCase`.

## Testing Guidelines

- Add/adjust tests for user-visible behavior and tool safety boundaries.
- Test names should be descriptive: `TestXxx_Scenario` (table tests are welcome).
- Run `go test ./...` before opening a PR; use `go test -race ./...` when changing concurrency code (`bus/`, `channels/`, `agent/`).

## Commit & Pull Request Guidelines

- Commit messages in this repo are short, imperative sentences (e.g. “Add …”, “Fix …”).
- PRs should include: what/why, how to test (commands + expected output), and any config/workspace changes.
- If behavior changes, update `README.md` (and keep diagrams consistent with code).

## Notes

- `gateway` mode is long-lived and routes chat messages via an in-memory bus; keep I/O and timeouts conservative.
- Tools are security-sensitive; don’t broaden file or exec access without a clear rationale and tests.
