package skills

import (
	"embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed builtin/skills/**/*
var builtinFS embed.FS

type SkillInfo struct {
	Name        string
	Description string
	Location    string
	Available   bool
	Requires    string
	Source      string // "workspace" or "builtin"
}

type Loader struct {
	Workspace string
}

func New(workspace string) *Loader {
	return &Loader{Workspace: workspace}
}

func (l *Loader) ListAll() []SkillInfo {
	seen := map[string]bool{}
	var out []SkillInfo

	// Workspace skills take precedence.
	wsDir := filepath.Join(l.Workspace, "skills")
	entries, _ := os.ReadDir(wsDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(wsDir, name, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		meta := readFrontmatterFile(path)
		desc, avail, req := summarize(meta)
		out = append(out, SkillInfo{
			Name:        name,
			Description: desc,
			Location:    path,
			Available:   avail,
			Requires:    req,
			Source:      "workspace",
		})
		seen[name] = true
	}

	// Builtin skills (embedded).
	// Layout: builtin/skills/<name>/SKILL.md
	const root = "builtin/skills"
	files, _ := builtinListSkillFiles(root)
	for _, p := range files {
		name := strings.Split(strings.TrimPrefix(p, root+"/"), "/")[0]
		if name == "" || seen[name] {
			continue
		}
		b, err := builtinFS.ReadFile(p)
		if err != nil {
			continue
		}
		meta := readFrontmatter(string(b))
		desc, avail, req := summarize(meta)
		out = append(out, SkillInfo{
			Name:        name,
			Description: desc,
			Location:    "builtin:" + p,
			Available:   avail,
			Requires:    req,
			Source:      "builtin",
		})
	}

	return out
}

func (l *Loader) Load(name string) (string, bool) {
	// Workspace first
	wsPath := filepath.Join(l.Workspace, "skills", name, "SKILL.md")
	if b, err := os.ReadFile(wsPath); err == nil {
		return string(b), true
	}
	// Builtin
	p := "builtin/skills/" + name + "/SKILL.md"
	if b, err := builtinFS.ReadFile(p); err == nil {
		return string(b), true
	}
	return "", false
}

func (l *Loader) SummaryXML() string {
	all := l.ListAll()
	if len(all) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<skills>\n")
	for _, s := range all {
		b.WriteString("  <skill available=\"")
		if s.Available {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		b.WriteString("\">\n")
		b.WriteString("    <name>" + escapeXML(s.Name) + "</name>\n")
		b.WriteString("    <description>" + escapeXML(s.Description) + "</description>\n")
		b.WriteString("    <location>" + escapeXML(s.Location) + "</location>\n")
		if !s.Available && s.Requires != "" {
			b.WriteString("    <requires>" + escapeXML(s.Requires) + "</requires>\n")
		}
		b.WriteString("  </skill>\n")
	}
	b.WriteString("</skills>")
	return b.String()
}

func builtinListSkillFiles(root string) ([]string, error) {
	var out []string
	// embed.FS doesn't support WalkDir on Go 1.16? It does via fs.WalkDir.
	// Keep it minimal: known SKILL.md locations are small; list dirs via hard-coded glob-ish approach.
	// We'll use ReadDir recursively at depth 2.
	lv1, err := builtinFS.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, d := range lv1 {
		if !d.IsDir() {
			continue
		}
		p := root + "/" + d.Name() + "/SKILL.md"
		if _, err := builtinFS.ReadFile(p); err == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

var fmRe = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n`)

func readFrontmatterFile(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return readFrontmatter(string(b))
}

func readFrontmatter(content string) map[string]string {
	m := fmRe.FindStringSubmatch(content)
	if len(m) != 2 {
		return nil
	}
	meta := map[string]string{}
	for line := range strings.SplitSeq(m[1], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return meta
}

func summarize(meta map[string]string) (desc string, available bool, requires string) {
	desc = ""
	if meta != nil {
		desc = meta["description"]
	}
	if desc == "" {
		desc = meta["name"]
	}
	available = true
	requires = ""

	// Skill metadata is JSON in `metadata:` field.
	raw := ""
	if meta != nil {
		raw = meta["metadata"]
	}
	if raw == "" {
		return desc, available, requires
	}

	var outer map[string]any
	if err := json.Unmarshal([]byte(raw), &outer); err != nil {
		return desc, available, requires
	}
	metaKey, _ := outer["clawlet"].(map[string]any)
	if metaKey == nil && len(outer) == 1 {
		// Backward compatibility for older skills: accept a single unknown namespace.
		for _, v := range outer {
			metaKey, _ = v.(map[string]any)
		}
	}
	req, _ := metaKey["requires"].(map[string]any)
	var missing []string

	// bins
	if bins, ok := req["bins"].([]any); ok {
		for _, v := range bins {
			s, _ := v.(string)
			if s == "" {
				continue
			}
			if _, err := exec.LookPath(s); err != nil {
				available = false
				missing = append(missing, "CLI: "+s)
			}
		}
	}
	// env
	if envs, ok := req["env"].([]any); ok {
		for _, v := range envs {
			s, _ := v.(string)
			if s == "" {
				continue
			}
			if os.Getenv(s) == "" {
				available = false
				missing = append(missing, "ENV: "+s)
			}
		}
	}
	requires = strings.Join(missing, ", ")
	return desc, available, requires
}
