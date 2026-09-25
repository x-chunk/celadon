// Package tui is celadon's full-screen terminal interface, built on Bubble
// Tea. Each part of the API is a tab; every call goes through teal.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/api"
	"github.com/x-chunk/celadon/internal/metrics"
	"github.com/x-chunk/celadon/internal/output"
)

// Minimum terminal size the layout is drawn for.
const (
	minWidth  = 60
	minHeight = 16
)

// tab is one screen of the interface. Tabs are pointers and change in
// place; Update returns only the command to run next.
type tab interface {
	title() string
	// init is called the first time the tab is shown, so a tab nobody
	// opens costs no requests.
	init() tea.Cmd
	update(msg tea.Msg) tea.Cmd
	view(width, height int) string
	// capturing reports whether a text field has the keyboard, in which
	// case the root's single-letter keys belong to the field.
	capturing() bool
	keys() []keyHelp
}

// leaver is a tab with something to forget when it is left — the vault,
// which takes a revealed secret off the screen.
type leaver interface{ leave() }

// Options configure the interface.
type Options struct {
	// Profile and BaseURL are shown in the header.
	Profile string
	BaseURL string
	// Refresh is how often the metrics dashboard refreshes the tab in
	// front: zero for the default, negative for never.
	Refresh time.Duration
}

// Model is the root of the interface.
type Model struct {
	be   *backend
	opts Options

	tabs    []tab
	active  int
	started []bool

	width, height int
	help          bool

	// admin marks the admin interface, whose header names the deployment
	// rather than an application, and whose calls cost nothing.
	admin bool
	// metrics marks the metrics dashboard: its header shows readiness, and
	// a ticker refreshes whichever tab is open.
	metrics bool
	ready   *metrics.Readiness
	refresh time.Duration
	// note closes the help screen: what the calls cost, and where they go.
	note string

	app       *teal.Application
	balance   string
	status    string
	statusErr bool
	now       func() time.Time
}

// New builds the interface over a client. ctx bounds every request it makes.
func New(ctx context.Context, client *teal.Client, opts Options) *Model {
	be := &backend{ctx: ctx, client: client, timeout: requestTimeout}
	m := &Model{
		be:   be,
		opts: opts,
		now:  time.Now,
		note: "Every call goes to " + opts.BaseURL + ". A search, each page of one and a portrait may be billed; " +
			"the status line shows what the last call cost.",
	}
	m.tabs = []tab{
		newOverviewTab(be),
		newArchiveTab(be),
		newActionsTab(be),
		newVaultTab(be),
		newInsightsTab(be),
		newSettingsTab(be),
	}
	m.started = make([]bool, len(m.tabs))
	return m
}

// Run starts the interface on the terminal and blocks until it is closed.
func Run(ctx context.Context, client *teal.Client, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return runProgram(ctx, New(ctx, client, opts))
}

// runProgram runs a model full screen until it quits.
func runProgram(ctx context.Context, m *Model) error {
	// The adaptive colors need to know whether the background is dark,
	// which is a question put to the terminal and answered on its input.
	// Asked here, before the program reads the keyboard, the answer cannot
	// swallow the first keys somebody presses.
	lipgloss.HasDarkBackground()
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	if err != nil && ctx.Err() != nil {
		// Canceled from outside — a signal — which is a way of quitting.
		return nil
	}
	return err
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(tea.SetWindowTitle("celadon"), m.start(0), m.tick())
}

// tickMsg asks the open tab to refresh what it shows.
type tickMsg struct{}

// tick schedules the next refresh, when the interface refreshes at all.
func (m *Model) tick() tea.Cmd {
	if m.refresh <= 0 {
		return nil
	}
	return tea.Tick(m.refresh, func(time.Time) tea.Msg { return tickMsg{} })
}

// start opens a tab, loading it the first time.
func (m *Model) start(i int) tea.Cmd {
	if m.started[i] {
		return nil
	}
	m.started[i] = true
	return m.tabs[i].init()
}

// switchTo makes tab i the active one.
func (m *Model) switchTo(i int) tea.Cmd {
	if i == m.active || i < 0 || i >= len(m.tabs) {
		return nil
	}
	if l, ok := m.tabs[m.active].(leaver); ok {
		l.leave()
	}
	m.active = i
	m.help = false
	return m.start(i)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if cmd, handled := m.globalKey(msg); handled {
			return m, cmd
		}
		return m, m.tabs[m.active].update(msg)

	case tickMsg:
		// Only the tab in front refreshes: nobody is looking at the others.
		return m, tea.Batch(m.tabs[m.active].update(msg), m.tick())

	case openPortraitMsg:
		i := m.find(insightsTitle)
		if i < 0 {
			return m, nil
		}
		cmd := m.switchTo(i)
		return m, tea.Batch(cmd, m.tabs[i].update(msg))
	}

	if o, ok := msg.(outcome); ok {
		m.noteOutcome(o)
	}
	if d, ok := msg.(done[metrics.Readiness]); ok && d.err == nil {
		ready := d.val
		m.ready = &ready
	}
	if d, ok := msg.(done[teal.Application]); ok && d.err == nil {
		app := d.val
		m.app = &app
		m.balance = output.Money(app.Balance)
	}

	// Answers go to every tab: each one knows its own and ignores the rest,
	// and a tab that was left while its call was in flight still wants it.
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		cmds = append(cmds, t.update(msg))
	}
	return m, tea.Batch(cmds...)
}

// globalKey handles the keys that belong to the whole interface. A tab with
// a text field focused keeps every key but ctrl+c and esc for itself.
func (m *Model) globalKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.tabs[m.active].capturing() {
		return nil, false
	}
	if m.help {
		m.help = false
		return nil, true
	}
	switch msg.String() {
	case "q":
		return tea.Quit, true
	case "?":
		m.help = true
		return nil, true
	case "tab":
		return m.switchTo((m.active + 1) % len(m.tabs)), true
	case "shift+tab":
		return m.switchTo((m.active + len(m.tabs) - 1) % len(m.tabs)), true
	}
	if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		if i := int(s[0] - '1'); i < len(m.tabs) {
			return m.switchTo(i), true
		}
	}
	return nil, false
}

// noteOutcome puts the last answer's cost or failure in the status line.
func (m *Model) noteOutcome(o outcome) {
	if meta := o.metaOf(); meta != nil && meta.HasBalance {
		m.balance = meta.Balance.String()
	}
	if err := o.errOf(); err != nil {
		p := api.Explain(err, m.now())
		m.status, m.statusErr = p.Title, true
		if p.Hint != "" {
			m.status += " — " + p.Hint
		}
		return
	}
	if line := output.MetaLine(o.metaOf()); line != "" {
		m.status, m.statusErr = line, false
	} else if m.statusErr {
		// A call that went through clears the last failure: it is no
		// longer what is wrong.
		m.status, m.statusErr = "", false
	}
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.width == 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("celadon needs a terminal of at least %d×%d (this one is %d×%d).", minWidth, minHeight, m.width, m.height)
	}

	header := m.header()
	tabs := m.tabBar()
	footer := m.footer()
	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(tabs) - lipgloss.Height(footer)

	var body string
	if m.help {
		body = m.helpView(m.width, bodyHeight)
	} else {
		body = m.tabs[m.active].view(m.width, bodyHeight)
	}
	body = lipgloss.NewStyle().Width(m.width).Height(bodyHeight).MaxHeight(bodyHeight).MaxWidth(m.width).Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, header, tabs, body, footer)
}

func (m *Model) header() string {
	if m.metrics {
		left := styleTitle.Render("celadon metrics") + styleMuted.Render(" · "+m.opts.Profile)
		state := styleFaint.Render("○ checking")
		if m.ready != nil {
			if m.ready.Ready {
				state = styleOK.Render("● ready")
			} else {
				state = styleError.Render("● not ready")
			}
		}
		url := styleMuted.Render(" · " + m.opts.BaseURL)
		right := state + url
		if m.width-lipgloss.Width(left)-lipgloss.Width(right) < 1 {
			right = state
		}
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			return output.Truncate(left, m.width)
		}
		return left + strings.Repeat(" ", gap) + right
	}
	if m.admin {
		left := styleTitle.Render("celadon admin") + styleMuted.Render(" · "+m.opts.Profile)
		right := styleMuted.Render(output.Truncate(m.opts.BaseURL, max(m.width-lipgloss.Width(left)-2, 0)))
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			return output.Truncate(left, m.width)
		}
		return left + strings.Repeat(" ", gap) + right
	}
	left := styleTitle.Render("celadon")
	if m.app != nil {
		left += styleMuted.Render(" · ") + styleHeading.Render(output.OneLine(m.app.Name)) +
			styleMuted.Render(" · "+string(m.app.Billing))
	}
	left += styleMuted.Render(" · " + m.opts.Profile)
	right := ""
	if m.balance != "" {
		right = styleMuted.Render("balance ") + styleHeading.Render(m.balance)
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return output.Truncate(left, m.width)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) tabBar() string {
	parts := make([]string, 0, len(m.tabs))
	for i, t := range m.tabs {
		label := fmt.Sprintf("%d %s", i+1, t.title())
		if i == m.active {
			parts = append(parts, styleTabActive.Render(label))
		} else {
			parts = append(parts, styleTabInactive.Render(label))
		}
	}
	bar := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	rule := styleFaint.Render(strings.Repeat("─", m.width))
	return output.Truncate(bar, m.width) + "\n" + rule
}

func (m *Model) footer() string {
	rule := styleFaint.Render(strings.Repeat("─", m.width))
	status := ""
	if m.status != "" {
		style := styleMuted
		if m.statusErr {
			style = styleError
		}
		status = style.Render(output.Truncate(m.status, m.width))
	}
	keys := m.tabs[m.active].keys()
	if !m.tabs[m.active].capturing() {
		keys = append(keys, keyHelp{"tab", "next tab"}, keyHelp{"?", "help"}, keyHelp{"q", "quit"})
	}
	return rule + "\n" + status + "\n" + renderKeys(keys, m.width)
}

func (m *Model) helpView(width, height int) string {
	var b strings.Builder
	b.WriteString(styleHeading.Render("Everywhere") + "\n")
	b.WriteString(renderKeys([]keyHelp{
		{fmt.Sprintf("1-%d", len(m.tabs)), "open a tab"}, {"tab/shift+tab", "next/previous tab"}, {"?", "this help"}, {"q", "quit"}, {"ctrl+c", "quit from anywhere"},
	}, width) + "\n\n")
	for i, t := range m.tabs {
		b.WriteString(styleHeading.Render(fmt.Sprintf("%d %s", i+1, t.title())) + "\n")
		b.WriteString(renderKeys(t.keys(), width) + "\n")
	}
	b.WriteString("\n" + styleMuted.Render(wrapText(m.note+" Press any key to close this help.", width)))
	return clip(b.String(), height)
}

// find returns the position of the tab with the given title, or -1. Tabs
// are found by title rather than by position, so the order they are shown
// in is decided in one place, New.
func (m *Model) find(title string) int {
	for i, t := range m.tabs {
		if t.title() == title {
			return i
		}
	}
	return -1
}

// insightsTitle is the tab a picked chat's portrait opens in.
const insightsTitle = "Insights"

// openPortraitMsg asks the insights tab for a chat's portrait, from
// wherever a chat was picked.
type openPortraitMsg struct{ chat int64 }
