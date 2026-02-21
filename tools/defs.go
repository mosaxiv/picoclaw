package tools

import (
	"encoding/json"

	"github.com/mosaxiv/clawlet/llm"
)

func defReadFile() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "read_file",
			Description: "Read a UTF-8 text file from disk.",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"path": {Type: "string", Description: "File path (relative to workspace recommended)."},
				},
				Required: []string{"path"},
			},
		},
	}
}

func defWriteFile() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "write_file",
			Description: "Write a UTF-8 text file to disk (creates parent dirs).",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"path":    {Type: "string"},
					"content": {Type: "string"},
				},
				Required: []string{"path", "content"},
			},
		},
	}
}

func defEditFile() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "edit_file",
			Description: "Edit a file by replacing old_text with new_text. old_text must appear exactly once.",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"path":     {Type: "string"},
					"old_text": {Type: "string", Description: "Exact text to replace (must be unique)."},
					"new_text": {Type: "string", Description: "Replacement text."},
				},
				Required: []string{"path", "old_text", "new_text"},
			},
		},
	}
}

func defListDir() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "list_dir",
			Description: "List directory entries (names only).",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"path":       {Type: "string"},
					"recursive":  {Type: "boolean"},
					"maxEntries": {Type: "integer", Description: "Limit results (default 200)."},
				},
				Required: []string{"path"},
			},
		},
	}
}

func defExec() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "exec",
			Description: "Execute a shell command in the workspace directory.",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"command": {Type: "string"},
				},
				Required: []string{"command"},
			},
		},
	}
}

func defReadSkill() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "read_skill",
			Description: "Read a bundled skill (SKILL.md) by name.",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"name": {Type: "string"},
				},
				Required: []string{"name"},
			},
		},
	}
}

func defWebFetch() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "web_fetch",
			Description: "Fetch a URL and extract readable content.",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"url": {Type: "string"},
					"extractMode": {
						Type: "string",
						Enum: []string{"markdown", "text"},
					},
					"maxChars": {Type: "integer", Description: "Max characters in extracted text (default 50000)."},
					"headers": {
						Raw: json.RawMessage(`{"type":"object","description":"HTTP request headers to include (e.g. {\"Authorization\":\"Bearer token\"}).","additionalProperties":{"type":"string"}}`),
					},
				},
				Required: []string{"url"},
			},
		},
	}
}

func defWebSearch() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "web_search",
			Description: "Search the web (Brave Search API). Returns titles, URLs, and snippets.",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"query": {Type: "string"},
					"count": {Type: "integer"},
				},
				Required: []string{"query"},
			},
		},
	}
}

func defMessage() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "message",
			Description: "Send a message to a specific channel/chat_id. Do not use for replying to the current conversation.",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"content": {Type: "string"},
					"channel": {Type: "string"},
					"chat_id": {Type: "string"},
				},
				Required: []string{"content", "channel", "chat_id"},
			},
		},
	}
}

func defSpawn() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "spawn",
			Description: "Spawn a subagent to handle a task in the background and report back.",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"task":  {Type: "string"},
					"label": {Type: "string"},
				},
				Required: []string{"task"},
			},
		},
	}
}

func defCron() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "cron",
			Description: "Schedule reminders and recurring tasks. Actions: add, list, remove.",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"action": {
						Type: "string",
						Enum: []string{"add", "list", "remove"},
					},
					"message":       {Type: "string"},
					"every_seconds": {Type: "integer"},
					"cron_expr":     {Type: "string"},
					"job_id":        {Type: "string"},
				},
				Required: []string{"action"},
			},
		},
	}
}

func defMemorySearch() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "memory_search",
			Description: "Semantic memory search over MEMORY.md and memory/*.md.",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"query":      {Type: "string"},
					"maxResults": {Type: "integer"},
					"minScore":   {Type: "number"},
				},
				Required: []string{"query"},
			},
		},
	}
}

func defMemoryGet() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        "memory_get",
			Description: "Read a safe snippet from MEMORY.md or memory/*.md.",
			Parameters: llm.JSONSchema{
				Type: "object",
				Properties: map[string]llm.JSONSchema{
					"path":  {Type: "string"},
					"from":  {Type: "integer"},
					"lines": {Type: "integer"},
				},
				Required: []string{"path"},
			},
		},
	}
}
