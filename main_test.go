package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLog(t *testing.T, root, id string, lines ...string) {
	t.Helper()
	dir := filepath.Join(root, "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ids(ss []*session) string {
	var out []string
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return strings.Join(out, ",")
}

func TestSearch(t *testing.T) {
	root := t.TempDir()
	writeLog(t, root, "talk",
		`{"type":"mode","mode":"normal"}`,
		`{"type":"user","cwd":"/work/app","timestamp":"t1","message":{"role":"user","content":"Deploy の手順を \"確認\" して"}}`,
		`{"type":"assistant","cwd":"/work/app/sub","timestamp":"t2","message":{"content":[{"type":"thinking","thinking":"secret"},{"type":"text","text":"staging に出しました"}]}}`,
		`{"type":"ai-title","aiTitle":"AI title"}`,
		`{"type":"custom-title","customTitle":"my title"}`,
		`{"type":"ai-title","aiTitle":"later AI title"}`,
	)
	writeLog(t, root, "tools",
		`{"type":"user","cwd":"/work/app","message":{"content":[{"type":"tool_result","content":"deploy staging"}]}}`,
		`{"type":"assistant","cwd":"/work/app","message":{"content":[{"type":"tool_use","input":{"command":"deploy staging"}}]}}`,
		`{"type":"user","isMeta":true,"cwd":"/work/app","message":{"content":"deploy staging"}}`,
		`{"type":"user","origin":{"kind":"task-notification"},"cwd":"/work/app","message":{"content":"deploy staging"}}`,
		`{"type":"attachment","cwd":"/work/app","attachment":"deploy staging"}`,
	)
	writeLog(t, root, "self", `{"type":"user","cwd":"/work/app","message":{"content":"deploy staging"}}`)

	got, err := search(root, []string{"deploy", "STAGING"}, time.Time{}, "self", 10)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "talk" {
		t.Fatalf("sessions = %q, want talk (words in different messages, case ignored; tool output, meta, notifications and self skipped)", ids(got))
	}
	s := got[0]
	if s.Cwd != "/work/app" || s.Title != "my title" || len(s.Matches) != 2 || s.Matches[1].Role != "assistant" {
		t.Fatalf("session = %+v", s)
	}

	if got, _ := search(root, []string{`"確認"`}, time.Time{}, "", 10); ids(got) != "talk" {
		t.Fatalf("quoted word: sessions = %q, want talk", ids(got))
	}
	if got, _ := search(root, []string{"secret"}, time.Time{}, "", 10); len(got) != 0 {
		t.Fatalf("thinking: sessions = %q, want none", ids(got))
	}
	if got, _ := search(root, []string{"deploy"}, time.Time{}, "", 10); ids(got) != "self,talk" && ids(got) != "talk,self" {
		t.Fatalf("without self: sessions = %q, want self and talk", ids(got))
	}
	if got, _ := search(root, []string{"deploy"}, time.Now().Add(time.Hour), "", 10); len(got) != 0 {
		t.Fatalf("since the future: sessions = %q, want none", ids(got))
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"3d":         now.AddDate(0, 0, -3),
		"12h":        now.Add(-12 * time.Hour),
		"2026-11-01": time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local),
	} {
		if got, err := parseSince(in, now); err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseSince("yesterday", now); err == nil {
		t.Error("parseSince(yesterday): want an error")
	}
}

func TestSnippet(t *testing.T) {
	long := strings.Repeat("あ", 50) + "\n target " + strings.Repeat("い", 200)
	got := snippet(long, "TARGET")
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") || !strings.Contains(got, "あ target い") {
		t.Fatalf("snippet = %q", got)
	}
}
