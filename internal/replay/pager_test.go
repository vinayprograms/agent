package replay

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
)

func newReadyPagerModel(content string) *pagerModel {
	m := &pagerModel{title: "test", content: content}
	m.Init()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(*pagerModel)
}

func TestPagerModel_View_Smoke(t *testing.T) {
	m := newReadyPagerModel("line one\nline two\nline three\n")
	view := m.View()
	if !strings.Contains(view, "test") {
		t.Errorf("View() = %q, want it to contain the title %q", view, "test")
	}
	if !strings.Contains(view, "line one") {
		t.Error("View() does not contain rendered content")
	}
}

func TestPagerModel_View_NotReady(t *testing.T) {
	m := &pagerModel{title: "test"}
	if got := m.View(); !strings.Contains(got, "Loading") {
		t.Errorf("View() before ready = %q, want a loading placeholder", got)
	}
}

func TestPagerModel_View_SearchInput(t *testing.T) {
	m := newReadyPagerModel("content")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(*pagerModel)
	if got := m.View(); !strings.Contains(got, "/") {
		t.Errorf("View() during search = %q, want the search prompt", got)
	}
}

func TestPagerModel_View_SearchFailed(t *testing.T) {
	m := newReadyPagerModel("alpha\nbeta\n")
	m.searchQuery = "zzz"
	m.executeSearch()
	if got := m.View(); !strings.Contains(got, "not found") {
		t.Errorf("View() with a failed search = %q, want a not-found footer", got)
	}
}

func TestPagerModel_View_MatchInfo(t *testing.T) {
	m := newReadyPagerModel("alpha\nbeta\nalpha\n")
	m.searchQuery = "alpha"
	m.executeSearch()
	if got := m.View(); !strings.Contains(got, "1/2") {
		t.Errorf("View() with matches = %q, want a [1/2] footer", got)
	}
}

func TestPagerModel_View_LiveIndicator(t *testing.T) {
	m := newReadyPagerModel("content")
	m.live = true
	if got := m.View(); !strings.Contains(got, "LIVE") {
		t.Errorf("View() in live mode = %q, want a LIVE indicator", got)
	}
}

func TestPagerModel_Update_Quit(t *testing.T) {
	m := newReadyPagerModel("content")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("Update('q') returned nil cmd, want tea.Quit")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("Update('q') cmd() = %v, want tea.Quit()", msg)
	}
}

func TestPagerModel_Update_Search(t *testing.T) {
	m := newReadyPagerModel("alpha\nbeta\nalpha again\n")

	// "/" enters search mode.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(*pagerModel)
	if !m.searching {
		t.Fatal("after '/', searching = false, want true")
	}
	if cmd == nil {
		t.Error("after '/', cmd = nil, want textinput.Blink")
	}

	// Type the query.
	for _, r := range "alpha" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(*pagerModel)
	}

	// Enter executes the search.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*pagerModel)
	if m.searching {
		t.Error("after enter, searching = true, want false")
	}
	if len(m.searchLines) != 2 {
		t.Errorf("searchLines = %v, want 2 matches for %q", m.searchLines, "alpha")
	}

	// esc clears the search.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*pagerModel)
	if m.searchQuery != "" {
		t.Errorf("searchQuery after esc = %q, want empty", m.searchQuery)
	}
}

func TestPagerModel_Update_SearchNoMatch(t *testing.T) {
	m := newReadyPagerModel("alpha\nbeta\n")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(*pagerModel)
	for _, r := range "zzz" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(*pagerModel)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*pagerModel)
	if !m.searchFailed {
		t.Error("searchFailed = false, want true for a query with no matches")
	}
}

func TestPagerModel_Update_EscClearsCompletedSearch(t *testing.T) {
	m := newReadyPagerModel("alpha\nbeta\nalpha again\n")
	m.searchQuery = "alpha"
	m.executeSearch()
	if len(m.searchLines) == 0 {
		t.Fatal("setup: expected matches")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*pagerModel)
	if m.searchQuery != "" {
		t.Errorf("searchQuery after esc = %q, want empty", m.searchQuery)
	}
	if cmd != nil {
		if msg := cmd(); msg == tea.Quit() {
			t.Error("esc with an active search must not quit")
		}
	}
}

func TestPagerModel_Update_EscQuitsWithoutSearch(t *testing.T) {
	m := newReadyPagerModel("content")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("esc with no active search must quit")
	}
}

func TestPagerModel_Update_Navigation(t *testing.T) {
	content := strings.Repeat("line\n", 200)
	m := newReadyPagerModel(content)
	m.viewport.GotoBottom()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = updated.(*pagerModel)
	if m.viewport.YOffset != 0 {
		t.Errorf("after 'g', YOffset = %d, want 0", m.viewport.YOffset)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = updated.(*pagerModel)
	if !m.viewport.AtBottom() {
		t.Error("after 'G', expected viewport at bottom")
	}
}

func TestPagerModel_Update_FollowModeLive(t *testing.T) {
	content := strings.Repeat("line\n", 200)
	m := newReadyPagerModel(content)
	m.live = true
	m.viewport.GotoTop()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = updated.(*pagerModel)
	if !m.viewport.AtBottom() {
		t.Error("'f' in live mode must jump to bottom")
	}
}

func TestPagerModel_Update_FollowModeNotLive(t *testing.T) {
	content := strings.Repeat("line\n", 200)
	m := newReadyPagerModel(content)
	m.viewport.GotoTop()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = updated.(*pagerModel)
	if m.viewport.AtBottom() {
		t.Error("'f' outside live mode must not jump")
	}
}

func TestPagerModel_Update_NextPrevMatch(t *testing.T) {
	m := newReadyPagerModel("alpha\nbeta\nalpha\ngamma\nalpha\n")
	m.searchQuery = "alpha"
	m.executeSearch()
	if len(m.searchLines) != 3 {
		t.Fatalf("setup: expected 3 matches, got %d", len(m.searchLines))
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(*pagerModel)
	if m.searchIndex != 1 {
		t.Errorf("after 'n', searchIndex = %d, want 1", m.searchIndex)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	m = updated.(*pagerModel)
	if m.searchIndex != 0 {
		t.Errorf("after 'N', searchIndex = %d, want 0", m.searchIndex)
	}

	// N wraps to the end from index 0.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	m = updated.(*pagerModel)
	if m.searchIndex != 2 {
		t.Errorf("after wrapping 'N', searchIndex = %d, want 2", m.searchIndex)
	}
}

func TestPagerModel_Update_SearchCancelViaCtrlC(t *testing.T) {
	m := newReadyPagerModel("content")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(*pagerModel)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(*pagerModel)
	if m.searching {
		t.Error("ctrl+c during search must cancel searching")
	}
}

func TestPagerModel_Update_FileChanged(t *testing.T) {
	calls := 0
	m := newReadyPagerModel("v1")
	m.live = true
	m.renderFunc = func() (string, error) {
		calls++
		return "v2", nil
	}

	updated, _ := m.Update(fileChangedMsg{})
	m = updated.(*pagerModel)
	if calls != 1 {
		t.Fatalf("renderFunc called %d times, want 1", calls)
	}
	if m.content != "v2" {
		t.Errorf("content after fileChangedMsg = %q, want %q", m.content, "v2")
	}
}

// TestWatchFile drives watchFile's tea.Cmd against a real fsnotify watcher
// on a temp file, using a near-zero debounce so the test stays fast.
func TestWatchFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/watched.txt"
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("fsnotify.NewWatcher() error = %v", err)
	}
	defer watcher.Close()
	if err := watcher.Add(path); err != nil {
		t.Fatalf("watcher.Add() error = %v", err)
	}

	m := &pagerModel{watcher: watcher, debounce: time.Millisecond}
	cmd := m.watchFile()

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	if err := os.WriteFile(path, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-done:
		if _, ok := msg.(fileChangedMsg); !ok {
			t.Errorf("watchFile() cmd = %T, want fileChangedMsg", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watchFile() did not report the write within 2s")
	}
}

func TestPager_Run_QuitsOnQ(t *testing.T) {
	p := newPager("test title")
	in := strings.NewReader("q")
	if err := p.Run("hello world", tea.WithInput(in), tea.WithoutRenderer(), tea.WithoutSignals()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestPager_RunLive_QuitsOnQ(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/live.txt"
	if err := os.WriteFile(path, []byte("initial"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	p := newPager("live title")
	in := strings.NewReader("q")
	renderFunc := func() (string, error) { return "rendered", nil }
	if err := p.RunLive(path, renderFunc, tea.WithInput(in), tea.WithoutRenderer(), tea.WithoutSignals()); err != nil {
		t.Fatalf("RunLive: %v", err)
	}
}

func TestPager_RunLive_MissingFile(t *testing.T) {
	p := newPager("live title")
	renderFunc := func() (string, error) { return "rendered", nil }
	if err := p.RunLive("/nonexistent/path/for/test", renderFunc); err == nil {
		t.Fatal("RunLive with a nonexistent file must fail to add a watch")
	}
}

func TestNewPager(t *testing.T) {
	p := newPager("my title")
	if p.title != "my title" {
		t.Errorf("title = %q, want %q", p.title, "my title")
	}
}

func TestWrapContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		width   int
	}{
		{"zero width passthrough", "some line", 0},
		{"short line untouched", "short", 40},
		{"table row wraps content column", "    1 │ 12:00:00 │ a very long message that needs to wrap across lines", 40},
		{"non-table long line wraps", strings.Repeat("word ", 20), 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapContent(tt.content, tt.width)
			if tt.width <= 0 {
				if got != tt.content {
					t.Errorf("wrapContent(width=0) = %q, want passthrough %q", got, tt.content)
				}
				return
			}
			if got == "" {
				t.Error("wrapContent() returned empty string")
			}
		})
	}
}
