package replaycmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stateWith builds a state directory holding the given sessions and
// returns it. Each session is one header/footer JSONL file.
type sess struct {
	id, name, agentfile, label, status string
	created                            time.Time
	duration                           time.Duration
}

func stateWith(t *testing.T, sessions ...sess) string {
	t.Helper()
	state := t.TempDir()
	dir := filepath.Join(state, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		body := fmt.Sprintf(
			`{"_type":"header","id":%q,"workflow_name":%q,"agentfile":%q,"label":%q,"created_at":%q}`+"\n"+
				`{"_type":"event","seq":1,"type":"user","content":"hi"}`+"\n"+
				`{"_type":"footer","status":%q,"updated_at":%q}`+"\n",
			s.id, s.name, s.agentfile, s.label, s.created.Format(time.RFC3339Nano),
			s.status, s.created.Add(s.duration).Format(time.RFC3339Nano))
		if err := os.WriteFile(filepath.Join(dir, s.id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

// three sessions, oldest first.
func fixture(t *testing.T) string {
	t.Helper()
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	return stateWith(t,
		sess{id: "aaaa1111", name: "hello", agentfile: "/w/hello/Agentfile", status: "complete",
			created: base, duration: 30 * time.Second},
		sess{id: "aaaa2222", name: "hello", agentfile: "/w/hello/Agentfile", label: "worker-1", status: "failed",
			created: base.Add(time.Hour), duration: 5 * time.Second},
		sess{id: "bbbb3333", name: "review", agentfile: "/w/review/Agentfile", label: "worker-2", status: "complete",
			created: base.Add(2 * time.Hour), duration: time.Minute},
	)
}

func TestReplay_ListTable(t *testing.T) {
	state := fixture(t)
	out, err := runCmd(t, "--state", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	loc := func(h int) string {
		return time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC).Add(time.Duration(h) * time.Hour).Local().Format(timeLayout)
	}
	want := "ID        CREATED              NAME    LABEL     STATUS    DURATION\n" +
		"aaaa1111  " + loc(0) + "  hello   -         complete  30s\n" +
		"aaaa2222  " + loc(1) + "  hello   worker-1  failed    5s\n" +
		"bbbb3333  " + loc(2) + "  review  worker-2  complete  1m0s\n"
	if out != want {
		t.Errorf("table\n got:\n%s\nwant:\n%s", out, want)
	}
}

func TestReplay_Selectors(t *testing.T) {
	state := fixture(t)
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"name", []string{"--name", "hello"}, []string{"aaaa1111", "aaaa2222"}},
		{"label", []string{"--label", "worker-2"}, []string{"bbbb3333"}},
		{"status", []string{"--status", "failed"}, []string{"aaaa2222"}},
		{"agentfile absolute", []string{"--agentfile", "/w/review/Agentfile"}, []string{"bbbb3333"}},
		{"agentfile basename", []string{"--agentfile", "Agentfile"}, []string{"aaaa1111", "aaaa2222", "bbbb3333"}},
		{"last", []string{"--last"}, []string{"bbbb3333"}},
		{"last of a name", []string{"--name", "hello", "--last"}, []string{"aaaa2222"}},
		{"prefix", []string{"bbbb"}, []string{"bbbb3333"}},
		{"full id", []string{"aaaa1111"}, []string{"aaaa1111"}},
		{"two ids replay in created order", []string{"bbbb3333", "aaaa1111"}, []string{"aaaa1111", "bbbb3333"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runCmd(t, append([]string{"--no-pager", "--state", state}, tt.args...)...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got []string
			for _, id := range []string{"aaaa1111", "aaaa2222", "bbbb3333"} {
				if i := strings.Index(out, id); i >= 0 {
					got = append(got, id)
				}
			}
			// Order matters: compare the order the ids appear in.
			sortedByAppearance := func(ids []string) []string {
				out2 := append([]string(nil), ids...)
				for i := range out2 {
					for j := i + 1; j < len(out2); j++ {
						if strings.Index(out, out2[j]) < strings.Index(out, out2[i]) {
							out2[i], out2[j] = out2[j], out2[i]
						}
					}
				}
				return out2
			}
			got = sortedByAppearance(got)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("replayed %v, want %v\noutput:\n%s", got, tt.want, out)
			}
		})
	}
}

func TestReplay_Since(t *testing.T) {
	now := time.Now()
	state := stateWith(t,
		sess{id: "old00000", name: "hello", status: "complete", created: now.Add(-48 * time.Hour), duration: time.Second},
		sess{id: "new00000", name: "hello", status: "complete", created: now.Add(-time.Minute), duration: time.Second},
	)
	out, err := runCmd(t, "--state", state, "--since", "1h", "--list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "new00000") || strings.Contains(out, "old00000") {
		t.Errorf("--since 1h listed the wrong sessions:\n%s", out)
	}
	if _, err := runCmd(t, "--state", state, "--since", "1s", "--list"); err == nil {
		t.Error("expected an error when no session matches --since")
	}
}

func TestReplay_AmbiguousPrefix(t *testing.T) {
	state := fixture(t)
	_, err := runCmd(t, "--no-pager", "--state", state, "aaaa")
	if err == nil {
		t.Fatal("expected an error for an ambiguous prefix")
	}
	for _, want := range []string{"matches 2 sessions", "aaaa1111", "aaaa2222", "longer prefix"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v missing %q", err, want)
		}
	}
}

func TestReplay_UnknownArgument(t *testing.T) {
	state := fixture(t)
	_, err := runCmd(t, "--no-pager", "--state", state, "zzzz")
	if err == nil || !strings.Contains(err.Error(), `"zzzz"`) {
		t.Errorf("error = %v, want it to name the unknown argument", err)
	}
}

func TestReplay_ListWithArgument(t *testing.T) {
	state := fixture(t)
	out, err := runCmd(t, "--state", state, "--list", "bbbb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "bbbb3333") || strings.Contains(out, "aaaa1111") {
		t.Errorf("--list with an argument listed the wrong sessions:\n%s", out)
	}
}

func TestReplay_ListFileArgument(t *testing.T) {
	state := fixture(t)
	path := filepath.Join(state, "sessions", "aaaa1111.jsonl")
	out, err := runCmd(t, "--state", state, "--list", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "aaaa1111") || strings.Contains(out, "bbbb3333") {
		t.Errorf("--list of one file listed the wrong sessions:\n%s", out)
	}
}

func TestReplay_ConfigStateLocation(t *testing.T) {
	state := fixture(t)
	cfg := filepath.Join(t.TempDir(), "agent.toml")
	if err := os.WriteFile(cfg, []byte("[state]\nlocation = \""+state+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "--config", cfg, "--list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "aaaa1111") {
		t.Errorf("config [state] location was not used:\n%s", out)
	}
}

func TestReplay_MalformedSessionIsReported(t *testing.T) {
	state := fixture(t)
	if err := os.WriteFile(filepath.Join(state, "sessions", "broken.jsonl"), []byte(`{"_type":"header",`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "--state", state, "--list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "broken.jsonl") || !strings.Contains(out, "warning") {
		t.Errorf("expected a warning naming the malformed file:\n%s", out)
	}
	if !strings.Contains(out, "aaaa1111") {
		t.Errorf("readable sessions still list:\n%s", out)
	}
}

func TestReplay_ListRunningSessionHasNoDuration(t *testing.T) {
	state := t.TempDir()
	dir := filepath.Join(state, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"_type":"header","id":"run00000","workflow_name":"hello","created_at":"2026-03-01T09:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "run00000.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "--state", state, "--list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "run00000  ") || !strings.HasSuffix(strings.TrimSpace(out), "-") {
		t.Errorf("a footerless session should list with no status or duration:\n%s", out)
	}
}

func TestReplay_ArgumentErrors(t *testing.T) {
	state := fixture(t)
	empty := t.TempDir()
	badGlob := filepath.Join(t.TempDir(), "bad[dir")
	if err := os.Mkdir(badGlob, 0o755); err != nil {
		t.Fatal(err)
	}
	unparseable := filepath.Join(t.TempDir(), "broken.jsonl")
	if err := os.WriteFile(unparseable, []byte(`{"_type":"header",`), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, arg, want string }{
		{"empty directory", empty, "no session files in"},
		{"unglobbable directory", badGlob, "cannot glob"},
		{"malformed file", unparseable, "broken.jsonl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runCmd(t, "--state", state, "--list", tt.arg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestReplay_UnreadableConfig(t *testing.T) {
	if _, err := runCmd(t, "--config", filepath.Join(t.TempDir(), "missing.toml"), "--list"); err == nil {
		t.Error("expected an error for a config file that does not exist")
	}
}
