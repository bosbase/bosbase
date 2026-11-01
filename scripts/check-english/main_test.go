package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommentLines(t *testing.T) {
	scenarios := []struct {
		name    string
		ext     string
		content string
		flagged []int // 1-based lines expected to be reported
	}{
		{"go line comment", ".go", "x := 1 // 中文\ny := 2 // english", []int{1}},
		{"go string is data", ".go", "s := \"中文\" // english", nil},
		{"go raw string spanning lines", ".go", "s := `\n中文\n` // ok", nil},
		{"go block comment spanning lines", ".go", "/* start\n中文\nend */ x := 1", []int{2}},
		{"url is not a comment", ".js", "const u = 'https://x' + \"中\"", nil},
		{"escaped quote", ".js", "const s = \"a \\\" 中\" // en", nil},
		{"svelte html comment", ".svelte", "<!-- 中文 -->\n<p>don't</p> <!-- ok -->", []int{1}},
		{"yaml comment", ".yml", "name: \"中\" # 注释", []int{1}},
		{"sql comment", ".sql", "SELECT '中' -- 注释", []int{1}},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			lines := commentLines(s.content, syntaxes[s.ext])
			got := []int{}
			for i, c := range lines {
				if hasCJK(c) {
					got = append(got, i+1)
				}
			}
			if len(got) != len(s.flagged) {
				t.Fatalf("expected lines %v, got %v (%q)", s.flagged, got, lines)
			}
			for i := range got {
				if got[i] != s.flagged[i] {
					t.Fatalf("expected lines %v, got %v", s.flagged, got)
				}
			}
		})
	}
}

func TestCheckFileAllowedPaths(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(dir)

	files := map[string]string{
		"docs/guide.md":           "# Guide\n中文",
		"docs/guide.zh-CN.md":     "# 指南",
		"ui/src/locales/zh.json":  "{\"a\": \"中\"}",
		"ui/src/locales/index.js": "// 中文",
		"chromem-go/x.go":         "// 中文",
	}
	for name, content := range files {
		os.MkdirAll(filepath.Dir(name), 0o755)
		os.WriteFile(name, []byte(content), 0o644)
	}

	for name, want := range map[string]int{
		"docs/guide.md": 1, "docs/guide.zh-CN.md": 0, "ui/src/locales/index.js": 0, "chromem-go/x.go": 0,
	} {
		v, err := checkFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if len(v) != want {
			t.Fatalf("%s: expected %d violations, got %v", name, want, v)
		}
	}
}
