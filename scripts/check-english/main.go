// Command check-english enforces the project rule that documentation and code
// comments are written in English (see CLAUDE.md).
//
// It scans the files tracked by git and reports the CJK characters found in
// code comments and Markdown documents, excluding the allowed paths (vendored
// modules, UI locales and translated documents named <name>.<language>.md).
//
// Usage: go run ./scripts/check-english [paths...]
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// vendoredDirs are third party modules kept in the repository.
var vendoredDirs = []string{
	"chromem-go/", "freecache/", "langchaingo/", "dbx/", "ws/", "rueidis/", "tygoja/", "graphql-go/",
	"ui/node_modules/", "ui/dist/",
}

// allowedDirs may contain non-English text (localized UI strings).
var allowedDirs = []string{"ui/src/locales/"}

// translationDoc matches translated documents, eg. mcp-guide.zh-CN.md.
var translationDoc = regexp.MustCompile(`\.[a-z]{2}(-[A-Za-z]{2,4})?\.md$`)

type commentSyntax struct {
	line       []string // line comment markers
	blockStart string
	blockEnd   string
	quotes     string // string delimiters to skip
}

var syntaxes = map[string]commentSyntax{
	".go":     {line: []string{"//"}, blockStart: "/*", blockEnd: "*/", quotes: "\"'`"},
	".js":     {line: []string{"//"}, blockStart: "/*", blockEnd: "*/", quotes: "\"'`"},
	".mjs":    {line: []string{"//"}, blockStart: "/*", blockEnd: "*/", quotes: "\"'`"},
	".ts":     {line: []string{"//"}, blockStart: "/*", blockEnd: "*/", quotes: "\"'`"},
	".scss":   {line: []string{"//"}, blockStart: "/*", blockEnd: "*/", quotes: "\"'"},
	".css":    {blockStart: "/*", blockEnd: "*/", quotes: "\"'"},
	".rs":     {line: []string{"//"}, blockStart: "/*", blockEnd: "*/", quotes: "\""},
	".svelte": {line: []string{"//"}, blockStart: "<!--", blockEnd: "-->"},
	".html":   {blockStart: "<!--", blockEnd: "-->"},
	".sql":    {line: []string{"--"}, blockStart: "/*", blockEnd: "*/", quotes: "'"},
	".sh":     {line: []string{"#"}, quotes: "\"'"},
	".py":     {line: []string{"#"}, quotes: "\"'"},
	".yml":    {line: []string{"#"}, quotes: "\"'"},
	".yaml":   {line: []string{"#"}, quotes: "\"'"},
	".toml":   {line: []string{"#"}, quotes: "\"'"},
}

func isCJK(r rune) bool {
	return (r >= 0x3000 && r <= 0x30ff) || // CJK punctuation, kana
		(r >= 0x3400 && r <= 0x4dbf) || // CJK extension A
		(r >= 0x4e00 && r <= 0x9fff) || // CJK unified ideographs
		(r >= 0xac00 && r <= 0xd7af) || // hangul
		(r >= 0xff01 && r <= 0xff60) // fullwidth forms
}

func hasCJK(s string) bool {
	for _, r := range s {
		if isCJK(r) {
			return true
		}
	}
	return false
}

type violation struct {
	file string
	line int
	text string
}

// commentLines returns the comment text of each line (index = line number - 1).
func commentLines(content string, syn commentSyntax) []string {
	lines := strings.Split(content, "\n")
	result := make([]string, len(lines))
	inBlock := false
	var quote rune

	for i, line := range lines {
		var comment strings.Builder
		for pos := 0; pos < len(line); {
			rest := line[pos:]
			r, size := utf8.DecodeRuneInString(rest)

			switch {
			case inBlock:
				if strings.HasPrefix(rest, syn.blockEnd) {
					inBlock = false
					pos += len(syn.blockEnd)
					continue
				}
				comment.WriteRune(r)
			case quote != 0:
				if r == '\\' && quote != '`' {
					pos += size
					if pos < len(line) {
						_, s := utf8.DecodeRuneInString(line[pos:])
						pos += s
					}
					continue
				}
				if r == quote {
					quote = 0
				}
			case syn.blockStart != "" && strings.HasPrefix(rest, syn.blockStart):
				inBlock = true
				pos += len(syn.blockStart)
				continue
			case strings.ContainsRune(syn.quotes, r):
				quote = r
			default:
				for _, marker := range syn.line {
					// "://" is an URL, not a comment
					if strings.HasPrefix(rest, marker) && !(marker == "//" && pos > 0 && line[pos-1] == ':') {
						comment.WriteString(rest[len(marker):])
						pos = len(line)
						break
					}
				}
				if pos >= len(line) {
					continue
				}
			}
			pos += size
		}
		// single and double quoted strings don't span lines
		if quote == '"' || quote == '\'' {
			quote = 0
		}
		result[i] = comment.String()
	}
	return result
}

func checkFile(path string) ([]violation, error) {
	slash := filepath.ToSlash(path)
	for _, dir := range append(append([]string{}, vendoredDirs...), allowedDirs...) {
		if strings.HasPrefix(slash, dir) {
			return nil, nil
		}
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".md" {
		if _, ok := syntaxes[ext]; !ok {
			return nil, nil
		}
	} else if translationDoc.MatchString(slash) {
		return nil, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(raw)
	if !hasCJK(content) {
		return nil, nil
	}

	lines := strings.Split(content, "\n")
	checked := lines
	if ext != ".md" {
		checked = commentLines(content, syntaxes[ext])
	}

	result := []violation{}
	for i, text := range checked {
		if hasCJK(text) {
			result = append(result, violation{file: slash, line: i + 1, text: strings.TrimSpace(lines[i])})
		}
	}
	return result, nil
}

func trackedFiles(paths []string) ([]string, error) {
	args := append([]string{"ls-files", "--"}, paths...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files failed: %w", err)
	}
	files := []string{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		if f := strings.TrimSpace(scanner.Text()); f != "" {
			files = append(files, f)
		}
	}
	return files, scanner.Err()
}

func main() {
	files, err := trackedFiles(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	total := 0
	for _, f := range files {
		violations, err := checkFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", f, err)
			continue
		}
		for _, v := range violations {
			text := v.text
			if utf8.RuneCountInString(text) > 120 {
				text = string([]rune(text)[:120]) + "..."
			}
			fmt.Printf("%s:%d: %s\n", v.file, v.line, text)
			total++
		}
	}

	if total > 0 {
		fmt.Printf("\n%d line(s) with non-English text in code comments or documentation.\n", total)
		fmt.Println("Documentation and code comments must be written in English (see CLAUDE.md).")
		os.Exit(1)
	}
	fmt.Println("check-english: OK")
}
