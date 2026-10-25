package apis

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Obsidian flavoured markdown helpers used by the MCP notes/memories.

var (
	mcpWikiLinkRegex = regexp.MustCompile(`!?\[\[([^\[\]\|]+?)(\|[^\[\]]*)?\]\]`)
	mcpMdLinkRegex   = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+\.md)(#[^)]*)?\)`)
	mcpTagRegex      = regexp.MustCompile(`(?:^|[\s(\[,;])#([\p{L}\p{N}_/\-]+)`)
	mcpH1Regex       = regexp.MustCompile(`(?m)^#\s+(.+?)\s*#*\s*$`)
	mcpInlineCode    = regexp.MustCompile("`[^`\n]*`")
)

// mcpParsedNote holds the information extracted from a markdown document.
type mcpParsedNote struct {
	Properties map[string]any
	Body       string
	Title      string
	Tags       []string
	Aliases    []string
	Links      []string
}

func mcpParseMarkdown(content string) *mcpParsedNote {
	result := &mcpParsedNote{Properties: map[string]any{}}

	result.Properties, result.Body = mcpParseFrontmatter(content)

	// strip code blocks and inline code before extracting tags and links
	text := mcpStripCode(result.Body)

	if t, ok := result.Properties["title"].(string); ok && strings.TrimSpace(t) != "" {
		result.Title = strings.TrimSpace(t)
	} else if m := mcpH1Regex.FindStringSubmatch(text); m != nil {
		result.Title = strings.TrimSpace(m[1])
	}

	tags := mcpPropertyStrings(result.Properties["tags"])
	tags = append(tags, mcpPropertyStrings(result.Properties["tag"])...)
	for _, m := range mcpTagRegex.FindAllStringSubmatch(text, -1) {
		tag := strings.TrimRight(m[1], "/-")
		// tags must contain at least one non numeric character (eg. "#123" is not a tag)
		if _, err := strconv.Atoi(strings.ReplaceAll(tag, "/", "")); err == nil || tag == "" {
			continue
		}
		tags = append(tags, tag)
	}
	for i, t := range tags {
		tags[i] = strings.TrimPrefix(strings.TrimSpace(t), "#")
	}
	result.Tags = normalizeTags(tags)

	result.Aliases = mcpPropertyStrings(result.Properties["aliases"])
	result.Aliases = append(result.Aliases, mcpPropertyStrings(result.Properties["alias"])...)

	links := []string{}
	for _, m := range mcpWikiLinkRegex.FindAllStringSubmatch(text, -1) {
		if target := mcpNormalizeLinkTarget(m[1]); target != "" {
			links = append(links, target)
		}
	}
	for _, m := range mcpMdLinkRegex.FindAllStringSubmatch(text, -1) {
		if strings.Contains(m[1], "://") {
			continue
		}
		if target := mcpNormalizeLinkTarget(m[1]); target != "" {
			links = append(links, target)
		}
	}
	result.Links = mcpUniqueFold(links)

	return result
}

// mcpNormalizeLinkTarget removes the heading/block reference and the .md extension of a link target.
func mcpNormalizeLinkTarget(target string) string {
	if i := strings.IndexAny(target, "#^"); i >= 0 {
		target = target[:i]
	}
	target = strings.TrimSpace(strings.ReplaceAll(target, "%20", " "))
	target = strings.TrimPrefix(target, "./")
	target = strings.TrimPrefix(target, "/")
	return strings.TrimSuffix(target, ".md")
}

func mcpStripCode(text string) string {
	var b strings.Builder
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") || strings.HasPrefix(strings.TrimSpace(line), "~~~") {
			inFence = !inFence
			b.WriteString("\n")
			continue
		}
		if inFence {
			b.WriteString("\n")
			continue
		}
		b.WriteString(mcpInlineCode.ReplaceAllString(line, " "))
		b.WriteString("\n")
	}
	return b.String()
}

func mcpUniqueFold(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, v := range values {
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, v)
	}
	return result
}

func mcpPropertyStrings(v any) []string {
	switch vv := v.(type) {
	case string:
		// "a, b" or "a b" style tag lists
		parts := strings.FieldsFunc(vv, func(r rune) bool { return r == ',' })
		if len(parts) == 1 && !strings.Contains(vv, ",") {
			parts = []string{vv}
		}
		result := []string{}
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				result = append(result, p)
			}
		}
		return result
	case []any:
		result := []string{}
		for _, item := range vv {
			if s := strings.TrimSpace(mcpScalarString(item)); s != "" {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func mcpScalarString(v any) string {
	switch vv := v.(type) {
	case string:
		return vv
	case nil:
		return ""
	default:
		return toStringAny(vv)
	}
}

func toStringAny(v any) string {
	switch vv := v.(type) {
	case bool:
		return strconv.FormatBool(vv)
	case int:
		return strconv.Itoa(vv)
	case int64:
		return strconv.FormatInt(vv, 10)
	case float64:
		return strconv.FormatFloat(vv, 'f', -1, 64)
	case string:
		return vv
	}
	return ""
}

// mcpParseFrontmatter parses a leading YAML frontmatter block.
//
// Only the subset of YAML used by Obsidian properties is supported:
// scalars, quoted strings, inline lists ([a, b]) and block lists ("- item").
func mcpParseFrontmatter(content string) (map[string]any, string) {
	props := map[string]any{}

	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return props, content
	}
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return props, content
	}
	block := normalized[4 : 4+end]
	body := normalized[4+end+4:]
	if i := strings.Index(body, "\n"); i >= 0 && strings.TrimSpace(body[:i]) == "" {
		body = body[i+1:]
	} else if strings.TrimSpace(body) == "" {
		body = ""
	}

	var currentKey string
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			if currentKey == "" {
				continue
			}
			list, _ := props[currentKey].([]any)
			props[currentKey] = append(list, mcpYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))))
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		value := strings.TrimSpace(line[i+1:])
		currentKey = key
		if value == "" {
			props[key] = []any{}
			continue
		}
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			items := []any{}
			for _, part := range strings.Split(value[1:len(value)-1], ",") {
				if part = strings.TrimSpace(part); part != "" {
					items = append(items, mcpYAMLScalar(part))
				}
			}
			props[key] = items
			continue
		}
		props[key] = mcpYAMLScalar(value)
	}

	// normalize empty lists that never received items to null
	for k, v := range props {
		if list, ok := v.([]any); ok && len(list) == 0 {
			props[k] = nil
		}
	}

	return props, body
}

func mcpYAMLScalar(v string) any {
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		if v[0] == '"' {
			if s, err := strconv.Unquote(v); err == nil {
				return s
			}
		}
		return v[1 : len(v)-1]
	}
	switch strings.ToLower(v) {
	case "true", "yes":
		return true
	case "false", "no":
		return false
	case "null", "~":
		return nil
	}
	if i, err := strconv.ParseInt(v, 10, 64); err == nil {
		return float64(i)
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	return v
}

// mcpNormalizeNotePath cleans a vault relative note path and ensures the .md extension.
func mcpNormalizeNotePath(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" || p == "." {
		return "", mcpToolErr("path is required.")
	}
	if strings.HasPrefix(p, "..") {
		return "", mcpToolErr("invalid path.")
	}
	if !strings.HasSuffix(strings.ToLower(p), ".md") {
		p += ".md"
	}
	if len(p) > 512 {
		return "", mcpToolErr("path is too long (max 512 characters).")
	}
	return p, nil
}

// mcpNoteBaseName returns the file name of a note path without the .md extension.
func mcpNoteBaseName(p string) string {
	return strings.TrimSuffix(path.Base(p), path.Ext(p))
}

// mcpRewriteLinks rewrites the [[oldTarget]] and [text](oldTarget.md) links to point to newTarget,
// preserving the heading reference and display alias.
func mcpRewriteLinks(content string, oldTargets []string, newTarget string) (string, bool) {
	matchesOld := func(target string) bool {
		t := strings.ToLower(mcpNormalizeLinkTarget(target))
		for _, o := range oldTargets {
			if t == strings.ToLower(o) {
				return true
			}
		}
		return false
	}

	changed := false
	content = mcpWikiLinkRegex.ReplaceAllStringFunc(content, func(m string) string {
		sub := mcpWikiLinkRegex.FindStringSubmatch(m)
		if !matchesOld(sub[1]) {
			return m
		}
		suffix := ""
		if i := strings.IndexAny(sub[1], "#^"); i >= 0 {
			suffix = sub[1][i:]
		}
		prefix := ""
		if strings.HasPrefix(m, "!") {
			prefix = "!"
		}
		changed = true
		return prefix + "[[" + newTarget + suffix + sub[2] + "]]"
	})
	content = mcpMdLinkRegex.ReplaceAllStringFunc(content, func(m string) string {
		sub := mcpMdLinkRegex.FindStringSubmatch(m)
		if !matchesOld(sub[1]) {
			return m
		}
		changed = true
		return strings.Replace(m, sub[1], strings.ReplaceAll(newTarget, " ", "%20")+".md", 1)
	})

	return content, changed
}
