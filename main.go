// session-rg searches past Claude Code sessions (~/.claude/projects/*/*.jsonl)
// for words in what the user and Claude said, using ripgrep to find the lines.
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxMatches = 3

type match struct {
	Role string `json:"role"`
	Time string `json:"time"`
	Text string `json:"text"`
}

type session struct {
	ID           string    `json:"id"`
	Cwd          string    `json:"cwd"`
	Title        string    `json:"title"`
	LastActivity time.Time `json:"last_activity"`
	Matches      []match   `json:"matches"`

	path  string
	found []bool
}

type record struct {
	Type      string `json:"type"`
	IsMeta    bool   `json:"isMeta"`
	Timestamp string `json:"timestamp"`
	Origin    *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func main() {
	since := flag.String("since", "", "only sessions active within a duration (3d, 12h) or since a date (2026-11-01)")
	limit := flag.Int("limit", 10, "print at most this many sessions, newest first")
	asJSON := flag.Bool("json", false, "print JSON Lines, one session per line")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: session-rg [--since 3d] [--limit 10] [--json] WORD...")
		fmt.Fprintln(os.Stderr, "Finds sessions where every WORD appears in what the user or Claude said (not tool output). Case is ignored.")
		flag.PrintDefaults()
	}
	words, _ := parseArgs(flag.CommandLine, os.Args[1:]) // CommandLine exits on a bad flag
	if len(words) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	var cutoff time.Time
	if *since != "" {
		c, err := parseSince(*since, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "session-rg:", err)
			os.Exit(2)
		}
		cutoff = c
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "session-rg:", err)
		os.Exit(1)
	}
	root := filepath.Join(home, ".claude", "projects")
	sessions, err := search(root, words, cutoff, os.Getenv("CLAUDE_CODE_SESSION_ID"), *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "session-rg:", err)
		os.Exit(1)
	}
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for _, s := range sessions {
		if *asJSON {
			e := json.NewEncoder(w)
			e.SetEscapeHTML(false)
			_ = e.Encode(s)
			continue
		}
		cwd := s.Cwd
		if rest, ok := strings.CutPrefix(cwd, home); ok {
			cwd = "~" + rest
		}
		fmt.Fprintf(w, "%s  %s  %s  %s\n", s.LastActivity.Local().Format("2006-01-02 15:04"), s.ID, cwd, s.Title)
		for _, m := range s.Matches {
			fmt.Fprintf(w, "  %s: %s\n", m.Role, m.Text)
		}
	}
}

// parseArgs lets flags come after words as well (session-rg herdr --since 3d):
// the flag package stops at the first word, so parse again after each one.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var words []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return words, nil
		}
		words = append(words, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// search returns the sessions under root where every word appears in a user or
// assistant message, newest first, skipping the session whose ID is self.
func search(root string, words []string, cutoff time.Time, self string, limit int) ([]*session, error) {
	args := []string{"--fixed-strings", "--ignore-case", "--null", "--no-line-number", "--with-filename",
		"--no-messages", "--max-depth", "2", "--glob", "*.jsonl"}
	for _, w := range words {
		args = append(args, "-e", jsonEscape(w))
	}
	byPath := map[string]*session{}
	err := rg(append(args, root), func(path string, line []byte) {
		s, seen := byPath[path]
		if !seen {
			s = newSession(path, len(words), cutoff, self)
			byPath[path] = s
		}
		if s == nil || !bytes.Contains(line, []byte(`"type":"user"`)) && !bytes.Contains(line, []byte(`"type":"assistant"`)) {
			return
		}
		var r record
		if json.Unmarshal(line, &r) != nil {
			return
		}
		text := spoken(r)
		lower := strings.ToLower(text)
		hit := ""
		for i, w := range words {
			if strings.Contains(lower, strings.ToLower(w)) {
				s.found[i] = true
				if hit == "" {
					hit = w
				}
			}
		}
		if hit != "" && len(s.Matches) < maxMatches {
			s.Matches = append(s.Matches, match{Role: r.Type, Time: r.Timestamp, Text: snippet(text, hit)})
		}
	})
	if err != nil {
		return nil, err
	}
	var out []*session
	for _, s := range byPath {
		if s != nil && !slices.Contains(s.found, false) {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b *session) int { return b.LastActivity.Compare(a.LastActivity) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	if len(out) == 0 {
		return out, nil
	}
	titles := map[string][2]string{} // path -> {custom title, AI title}; the last of each wins
	paths := []string{"--fixed-strings", "--null", "--no-line-number", "--with-filename", "--no-messages",
		"-e", `"type":"custom-title"`, "-e", `"type":"ai-title"`}
	for _, s := range out {
		s.Cwd = firstCwd(s.path)
		paths = append(paths, s.path)
	}
	err = rg(paths, func(path string, line []byte) {
		var t struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
			AITitle     string `json:"aiTitle"`
		}
		if json.Unmarshal(line, &t) != nil {
			return
		}
		v := titles[path]
		if t.Type == "custom-title" && t.CustomTitle != "" {
			v[0] = t.CustomTitle
		} else if t.Type == "ai-title" && t.AITitle != "" {
			v[1] = t.AITitle
		}
		titles[path] = v
	})
	if err != nil {
		return nil, err
	}
	for _, s := range out {
		s.Title = cmp.Or(titles[s.path][0], titles[s.path][1])
	}
	return out, nil
}

// newSession returns nil for a file to skip: the searching session itself, or one idle since before cutoff.
func newSession(path string, words int, cutoff time.Time, self string) *session {
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if self != "" && id == self {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || info.ModTime().Before(cutoff) {
		return nil
	}
	return &session{ID: id, LastActivity: info.ModTime(), Matches: []match{}, path: path, found: make([]bool, words)}
}

// rg runs ripgrep and calls fn with each output line split into its file path and content.
// Lines can be megabytes long (pasted images), so they are read whole rather than with a Scanner.
func rg(args []string, fn func(path string, line []byte)) error {
	cmd := exec.Command("rg", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r := bufio.NewReaderSize(out, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if path, rest, ok := bytes.Cut(line, []byte{0}); ok {
			fn(string(path), rest)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = cmd.Wait()
			return err
		}
	}
	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 { // 1 means nothing matched
			return err
		}
	}
	return nil
}

// spoken returns what the user typed or Claude wrote in the record: not tool input or output,
// thinking, skill text the harness inserted (isMeta), or background task notifications.
func spoken(r record) string {
	if r.Type == "user" && (r.IsMeta || r.Origin != nil && r.Origin.Kind == "task-notification") {
		return ""
	}
	var s string
	if json.Unmarshal(r.Message.Content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(r.Message.Content, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// firstCwd returns the cwd of the file's first record that has one: where the session was
// started, and where claude --resume finds it. Later records follow the session's cd.
func firstCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var rec struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Cwd != "" {
			return rec.Cwd
		}
		if err != nil {
			return ""
		}
	}
}

// snippet returns about 100 characters of text on one line, starting a little before word.
func snippet(text, word string) string {
	rs := []rune(strings.Join(strings.Fields(text), " "))
	lower := strings.ToLower(string(rs))
	start := 0
	if b := strings.Index(lower, strings.ToLower(word)); b > 0 && utf8.RuneCountInString(lower) == len(rs) {
		start = max(0, utf8.RuneCountInString(lower[:b])-30)
	}
	end := min(len(rs), start+100)
	s := string(rs[start:end])
	if start > 0 {
		s = "…" + s
	}
	if end < len(rs) {
		s += "…"
	}
	return s
}

// jsonEscape returns w as it appears inside a JSON string in the logs, so ripgrep can find
// words with quotes or backslashes. HTML escaping is off: the logs keep < and > as they are.
func jsonEscape(w string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(w)
	s := strings.TrimSpace(b.String())
	return s[1 : len(s)-1]
}

func parseSince(s string, now time.Time) (time.Time, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		if d, err := strconv.Atoi(n); err == nil {
			return now.AddDate(0, 0, -d), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("--since %q: use a duration like 3d or 12h, or a date like 2026-11-01", s)
}
