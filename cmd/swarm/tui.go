package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nats-io/nats.go"
)

// TUI commands
type tuiTickMsg time.Time
type tuiAgentMsg []agentInfo
type tuiTaskMsg []taskRecord

type agentInfo struct {
	id     string
	name   string
	status string
	load   float64
	caps   []string
}

// TUI styles
var (
	styleHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleIdle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleBusy   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleError  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleBox    = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1)
)

// tuiModel represents the TUI state.
type tuiModel struct {
	natsURL  string
	nc       *nats.Conn
	width    int
	height   int
	agents   []agentInfo
	tasks    []taskRecord
	input    textinput.Model
	focus    int // 0=agents, 1=tasks, 2=input
	selected int
	err      error
}

func newTUIModel(natsURL string) tuiModel {
	ti := textinput.New()
	ti.Placeholder = "submit <capability> \"<task>\""
	ti.Focus()

	return tuiModel{
		natsURL: natsURL,
		input:   ti,
		focus:   2, // Start with input focused
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		connectNATSCmd(m.natsURL),
	)
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			return m, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
			m.focus = (m.focus + 1) % 3
			if m.focus == 2 {
				m.input.Focus()
			} else {
				m.input.Blur()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			if m.focus == 2 && m.input.Value() != "" {
				return m, executeCmd(m.input.Value(), m.natsURL)
			}
		default:
			if m.focus == 2 {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case *nats.Conn:
		m.nc = msg
		return m, subscribeCmd(msg)

	case tuiTickMsg:
		return m, tickCmd()

	case tuiAgentMsg:
		m.agents = msg

	case tuiTaskMsg:
		m.tasks = msg

	case string:
		// Command output
		fmt.Println(msg)
		m.input.SetValue("")

	case error:
		m.err = msg
	}

	return m, nil
}

func (m tuiModel) View() string {
	if m.err != nil {
		return styleError.Render(fmt.Sprintf("Error: %v", m.err))
	}

	// Agent pane
	agentLines := []string{styleHeader.Render("Agents")}
	for _, ag := range m.agents {
		status := ag.status
		if status == "monitoring" || status == "idle" {
			status = styleIdle.Render(status)
		} else {
			status = styleBusy.Render(status)
		}
		agentLines = append(agentLines, fmt.Sprintf("%s %s load=%.0f%%", ag.name, status, ag.load*100))
	}
	if len(m.agents) == 0 {
		agentLines = append(agentLines, "No agents")
	}
	agentPane := styleBox.Render(strings.Join(agentLines, "\n"))

	// Task pane
	taskLines := []string{styleHeader.Render("Tasks")}
	for _, t := range m.tasks {
		taskLines = append(taskLines, fmt.Sprintf("%s %s %s", t.TaskID[:8], t.Capability, t.Status))
	}
	if len(m.tasks) == 0 {
		taskLines = append(taskLines, "No tasks")
	}
	taskPane := styleBox.Render(strings.Join(taskLines, "\n"))

	// Layout
	topRow := lipgloss.JoinHorizontal(lipgloss.Top, agentPane, taskPane)

	// Input
	inputLabel := "> "
	if m.focus == 2 {
		inputLabel = "┌> "
	}
	inputBox := lipgloss.NewStyle().BorderTop(true).Render(inputLabel + m.input.View())

	return lipgloss.JoinVertical(lipgloss.Left, topRow, inputBox)
}

// Commands
func tickCmd() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		return tuiTickMsg(t)
	})
}

func connectNATSCmd(url string) tea.Cmd {
	return func() tea.Msg {
		nc, err := nats.Connect(url)
		if err != nil {
			return err
		}
		return nc
	}
}

func subscribeCmd(nc *nats.Conn) tea.Cmd {
	return func() tea.Msg {
		// Subscribe to heartbeats
		sub, err := nc.SubscribeSync("heartbeat.>")
		if err != nil {
			return err
		}
		defer sub.Unsubscribe()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		agents := []agentInfo{}
		for {
			msg, err := sub.NextMsgWithContext(ctx)
			if err != nil {
				break
			}
			var hb struct {
				AgentID  string            `json:"agent_id"`
				Status   string            `json:"status"`
				Load     float64           `json:"load"`
				Metadata map[string]string `json:"metadata"`
			}
			if err := json.Unmarshal(msg.Data, &hb); err != nil {
				continue
			}
			// Extract name from metadata
			name := hb.AgentID
			if n, ok := hb.Metadata["name"]; ok && n != "" {
				name = n
			}
			// Extract capabilities from metadata
			var caps []string
			if cap, ok := hb.Metadata["capability"]; ok && cap != "" {
				caps = []string{cap}
			}
			agents = append(agents, agentInfo{
				id:     hb.AgentID,
				name:   name,
				status: hb.Status,
				load:   hb.Load,
				caps:   caps,
			})
		}

		// Sort by name
		sort.Slice(agents, func(i, j int) bool { return agents[i].name < agents[j].name })
		return tuiAgentMsg(agents)
	}
}

func executeCmd(cmd, natsURL string) tea.Cmd {
	return func() tea.Msg {
		// Parse command
		parts := strings.Fields(cmd)
		if len(parts) < 3 {
			return fmt.Errorf("usage: submit <capability> \"<task>\"")
		}

		if parts[0] != "submit" {
			return fmt.Errorf("unknown command: %s", parts[0])
		}

		capability := parts[1]
		task := strings.Join(parts[2:], " ")
		task = strings.Trim(task, "\"")

		// Submit via NATS
		nc, err := nats.Connect(natsURL)
		if err != nil {
			return err
		}
		defer nc.Close()

		taskID := fmt.Sprintf("t-%d", time.Now().Unix())
		subject := fmt.Sprintf("work.%s.%s", capability, taskID)

		// Simple task payload
		payload := map[string]string{"task": task}
		data, _ := json.Marshal(payload)

		if err := nc.Publish(subject, data); err != nil {
			return err
		}

		return fmt.Sprintf("Submitted: %s", taskID)
	}
}
