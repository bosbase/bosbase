package apis

import "testing"

func TestDecodeLLMJSON(t *testing.T) {
	scenarios := []struct {
		name    string
		answer  string
		summary string
		facts   int
		wantErr bool
	}{
		{"plain", `{"summary":"ok","facts":["a"]}`, "ok", 1, false},
		{"markdown fences", "```json\n{\"summary\":\"ok\",\"facts\":[]}\n```", "ok", 0, false},
		{"raw newline in string", "{\"summary\":\"line1\nline2\",\"facts\":[\"a\",\"b\"]}", "line1\nline2", 2, false},
		{"raw tab and CR", "{\"summary\":\"a\tb\r\",\"facts\":[]}", "a\tb\r", 0, false},
		{"text after the object", `{"summary":"ok","facts":[]} Hope this helps {not json}`, "ok", 0, false},
		{"text before the object", `Here is the JSON: {"summary":"ok","facts":[]}`, "ok", 0, false},
		{"escaped quotes and braces", `{"summary":"he said \"use {x}\"","facts":[]}`, `he said "use {x}"`, 0, false},
		{"object facts", `{"summary":"s","facts":[{"title":"t","content":"c","pinned":true}]}`, "s", 1, false},
		{"no object", `sorry, I can't`, "", 0, true},
		{"truncated", `{"summary":"abc`, "", 0, true},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			dst := &mcpSessionInput{}
			err := mcpDecodeLLMJSON(s.answer, dst)
			if (err != nil) != s.wantErr {
				t.Fatalf("expected error %v, got %v", s.wantErr, err)
			}
			if s.wantErr {
				return
			}
			if dst.Summary != s.summary || len(dst.Facts) != s.facts {
				t.Fatalf("got summary %q with %d facts", dst.Summary, len(dst.Facts))
			}
		})
	}
}
