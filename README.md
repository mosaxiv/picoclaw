<div align="center">
  <img src="clawlet.png" alt="clawlet" width="500">
  <h1>Clawlet</h1>
  <h3>Ultra-Lightweight&Efficient Personal AI Assistant</h3>
  <p>Single Binary · No CGO · No Runtime · No Dependencies</p>
</div>

**Clawlet** is a lightweight and efficient personal AI agent with built-in hybrid semantic memory search — and it still ships as a single, dependency-free binary.
Powered by bundled SQLite + sqlite-vec. No CGO, no runtime, no dependencies. Drop it on any machine and memory search just works.  

This project is inspired by **OpenClaw** and **nanobot**.

## Install

Download from [GitHub Releases](https://github.com/mosaxiv/clawlet/releases/latest).

macOS (Apple Silicon):
```bash
curl -L https://github.com/mosaxiv/clawlet/releases/latest/download/clawlet_Darwin_arm64.tar.gz | tar xz
mv clawlet ~/.local/bin/
```

## Quick Start

```bash
# Initialize
clawlet onboard \
  --openrouter-api-key "sk-or-..." \
  --model "openrouter/anthropic/claude-sonnet-4.5"

# Check effective configuration
clawlet status

# Chat
clawlet agent -m "What is 2+2?"
```

## Configuration (`~/.clawlet/config.json`)

Config file: `~/.clawlet/config.json`

### Supported providers

clawlet currently supports these LLM providers:

- **OpenAI** (`openai/<model>`, API key: `env.OPENAI_API_KEY`)
- **OpenRouter** (`openrouter/<provider>/<model>`, API key: `env.OPENROUTER_API_KEY`)
- **Anthropic** (`anthropic/<model>`, API key: `env.ANTHROPIC_API_KEY`)
- **Gemini** (`gemini/<model>`, API key: `env.GEMINI_API_KEY` or `env.GOOGLE_API_KEY`)
- **Local (Ollama / vLLM / OpenAI-compatible local endpoint)** (`ollama/<model>` or `local/<model>`, default base URL: `http://localhost:11434/v1`, API key optional)

Minimal config (OpenRouter):

```json
{
  "env": { "OPENROUTER_API_KEY": "sk-or-..." },
  "agents": { "defaults": { "model": "openrouter/anthropic/claude-sonnet-4-5" } }
}
```

Agent generation defaults are configurable:

```json
{
  "agents": {
    "defaults": {
      "model": "openrouter/anthropic/claude-sonnet-4-5",
      "maxTokens": 8192,
      "temperature": 0.7
    }
  }
}
```

Minimal config (Local via Ollama):

```json
{
  "agents": { "defaults": { "model": "ollama/qwen2.5:14b" } }
}
```

Minimal config (Local via vLLM using the same `ollama/` route):

```json
{
  "agents": { "defaults": { "model": "ollama/meta-llama/Llama-3.1-8B-Instruct" } },
  "llm": { "baseURL": "http://localhost:8000/v1" }
}
```

clawlet will fill in sensible defaults for missing sections (tools, gateway, cron, heartbeat, channels).

### Option: Memory search setup

To enable semantic memory search, add `memorySearch` to the agent defaults:

```json
{
  "env": {
    "OPENAI_API_KEY": "sk-..."
  },
  "agents": {
    "defaults": {
      "memorySearch": {
        "enabled": true,
        "provider": "openai",
        "model": "text-embedding-3-small"
      }
    }
  }
}
```

When enabled:
- The agent gains `memory_search` and `memory_get` tools for retrieving past context.
- clawlet indexes `MEMORY.md`, `memory.md`, and `memory/**/*.md` for retrieval.
- The index DB is created at `{workspace}/.memory/index.sqlite`.

When disabled (default):
- `memorySearch.enabled` defaults to `false`; the search tools are not exposed to the model.
- Memory files (`memory/MEMORY.md`, `memory/YYYY-MM-DD.md`) are still injected into context as usual.
- Normal chat behavior is otherwise unchanged.


### Safety defaults

clawlet is conservative by default:

- `tools.restrictToWorkspace` defaults to `true` (tools can only access files inside the workspace directory)

## Chat Apps

Chat app integrations are configured under `channels` (examples below).

<details>
<summary><b>Telegram</b></summary>

Uses **Telegram Bot API long polling** (`getUpdates`) so no public webhook endpoint is required.

1. Create a bot with `@BotFather` and copy the bot token.
2. (Optional but recommended) Restrict access with `allowFrom`.
   - Telegram numeric user ID works best.
   - Username is also supported (without `@`).

Example config (merge into `~/.clawlet/config.json`):

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "123456:ABCDEF...",
      "allowFrom": ["123456789"]
    }
  }
}
```

Then run:

```bash
clawlet gateway
```

</details>

<details>
<summary><b>WhatsApp</b></summary>

Uses **WhatsApp Web Multi-Device**. No Meta webhook/public endpoint is required.

1. Enable channel and (recommended) set `allowFrom`.
2. Run login once:
   - `clawlet channels login --channel whatsapp`
   - Scan the QR shown in terminal from WhatsApp `Linked devices`.
3. Start normal runtime with `clawlet gateway`.

Example config (merge into `~/.clawlet/config.json`):

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "allowFrom": ["15551234567"]
    }
  }
}
```

Then run:

```bash
# one-time login (required before gateway)
clawlet channels login --channel whatsapp

# normal runtime
clawlet gateway
```

Notes:
- Send retries are applied for transient/rate-limit errors with exponential backoff.
- Session state is persisted by default at `~/.clawlet/whatsapp-auth/session.db`.
- You can override store path with `sessionStorePath` if needed.
- `clawlet gateway` does not perform QR login; if not linked, it exits with a login command hint.

</details>

<details>
<summary><b>Discord</b></summary>

1. Create the bot and copy the token
Go to https://discord.com/developers/applications, create an application, then `Bot` → `Add Bot`. Copy the bot token.

2. Invite the bot to your server (OAuth2 URL Generator)
In `OAuth2` → `URL Generator`, choose `Scopes: bot`. For `Bot Permissions`, the minimal set is `View Channels`, `Send Messages`, `Read Message History`. Open the generated URL and add the bot to your server.

3. Enable Message Content Intent (required for guild message text)
In the Developer Portal bot settings, enable **MESSAGE CONTENT INTENT**. Without it, the bot won't receive message text in servers.

4. Get your User ID (for allowFrom)
Enable Developer Mode in Discord settings, then right-click your profile and select `Copy User ID`.

5. Configure clawlet
`channels.discord.allowFrom` is the list of user IDs allowed to talk to the agent (empty = allow everyone).

Example config (merge into `~/.clawlet/config.json`):

```json
{
  "channels": {
    "discord": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allowFrom": ["YOUR_USER_ID"]
    }
  }
}
```

6. Run

```bash
clawlet gateway
```

</details>

<details>
<summary><b>Slack</b></summary>

Uses **Socket Mode** (no public URL required). clawlet currently supports Socket Mode only.

1. Create a Slack app
2. Configure the app:
   - Socket Mode: ON, generate an App-Level Token (`xapp-...`) with `connections:write`
   - OAuth scopes (bot): `chat:write`, `reactions:write`, `app_mentions:read`, `im:history`, `channels:history`
   - Event Subscriptions: subscribe to `message.im`, `message.channels`, `app_mention`
3. Install the app to your workspace and copy the Bot Token (`xoxb-...`)
4. Set `channels.slack.enabled=true`, and configure `botToken` + `appToken`.
   - groupPolicy: "mention" (default — respond only when @mentioned), "open" (respond to all channel messages), or "allowlist" (restrict to specific channels).
   - DM policy defaults to open. Set "dm": {"enabled": false} to disable DMs.

Example config (merge into `~/.clawlet/config.json`):

```json
{
  "channels": {
    "slack": {
      "enabled": true,
      "botToken": "xoxb-...",
      "appToken": "xapp-...",
      "groupPolicy": "mention",
      "allowFrom": ["U012345"]
    }
  }
}
```

Then run:

```bash
clawlet gateway
```

</details>

## CLI Reference

| Command | Description |
| --- | --- |
| `clawlet onboard` | Initialize a workspace and write a minimal config. |
| `clawlet status` | Print the effective configuration (after defaults and routing). |
| `clawlet agent` | Run the agent in CLI mode (interactive or single message). |
| `clawlet gateway` | Run the long-lived gateway (channels + cron + heartbeat). |
| `clawlet channels status` | Show which chat channels are enabled/configured. |
| `clawlet cron list` | List scheduled jobs. |
| `clawlet cron add` | Add a scheduled job. |
| `clawlet cron remove` | Remove a scheduled job. |
| `clawlet cron toggle` | Enable/disable a scheduled job. |
| `clawlet cron run` | Run a job immediately. |

### `clawlet cron add` formats

`--message` is required, and exactly one of `--every`, `--cron`, or `--at` must be set.

```bash
# Every N seconds
clawlet cron add --message "summarize my inbox" --every 3600

# Cron expression (5-field)
clawlet cron add --message "daily standup notes" --cron "0 9 * * 1-5"

# Run once at a specific time (RFC3339)
clawlet cron add --message "remind me" --at "2026-02-10T09:00:00Z"

# Deliver to a chat (requires both --channel and --to)
clawlet cron add --message "ping" --every 600 --channel slack --to U012345
```
## 🐳 Docker

Build and run clawlet in a container:

```bash
# Build the image
docker build -t clawlet .

# Initialize config (first time only)
docker run -v ~/.clawlet:/root/.clawlet --rm clawlet onboard

# Edit config on host to add API keys
vim ~/.clawlet/config.json

# Run the gateway
docker run -v ~/.clawlet:/root/.clawlet -p 18790:18790 clawlet gateway

# Or run a single command
docker run -v ~/.clawlet:/root/.clawlet --rm clawlet agent -m "Hello"
docker run -v ~/.clawlet:/root/.clawlet --rm clawlet status
```
