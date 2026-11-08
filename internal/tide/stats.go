package tide

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/style"
)

// The stats are a tab of the agent pane in each project, of no agent, as the
// settings tab is: machine-wide facts about the AI coding agents, read from the
// daemon when the tab comes to show and again on each project event while it
// shows. Usage is its first section (the wdx doc USAGE, docs/TIDE_STATS.md).

const (
	statsKey = "S" // of the menu
	// statsTab is the stats' tab, which is of no agent.
	statsTab = "stats"
	// usageBarCells is how many cells a window's bar has.
	usageBarCells = 10
	// usageWarning and usageCritical are the percentages from which a bar warns
	// and alarms. Fixed for now: the context thresholds are token counts and cannot
	// colour a percentage.
	usageWarning, usageCritical = 75, 90
)

// usageListed is the answer to reading the usage, with the ACAs that can be used,
// so that one without rows is shown as such.
type usageListed struct {
	usage []api.UsageWindow
	acas  []api.Aca
	err   error
}

// statsMinute says a minute passed while the stats showed: the time left to a
// reset is drawn again.
type statsMinute struct{}

// statsShown reports whether the agent pane shows the stats.
func (m *Model) statsShown() bool {
	l := m.list()
	return l != nil && l.active == statsTab
}

// statsOffered reports whether C-q S applies: a tab needs a project, and nothing
// moves when the stats are what has the focus.
func (m *Model) statsOffered() bool {
	return m.list() != nil && (m.layout.focus != agent || !m.statsShown())
}

// openStats opens the stats' tab, or goes to it, and focuses it.
func (m *Model) openStats() {
	m.list().openTab(statsTab)
	m.layout.setFocus(agent)
	if m.layout.zoomed != none {
		m.layout.zoomed = agent
	}
	m.changed("stats")
	m.log.Info("stats opened")
}

// listUsage reads the usage and the ACAs that can be used.
func (m *Model) listUsage() tea.Cmd {
	return func() tea.Msg {
		u, err := m.client.ListUsage(m.ctx)
		if err != nil {
			return usageListed{err: err}
		}
		acas, err := m.client.ListAcas(m.ctx)
		return usageListed{usage: u.Usage, acas: acas.Acas, err: err}
	}
}

// usageListed keeps what the daemon answered, and starts the minute timer while the
// stats show.
func (m *Model) usageListed(msg usageListed) tea.Cmd {
	if msg.err != nil {
		m.log.Info("usage not listed", "err", msg.err)
		m.usage, m.usageErr = nil, msg.err.Error()
		return nil
	}
	m.usage, m.usageAcas, m.usageErr, m.usageRead = msg.usage, msg.acas, "", true
	return m.statsTimer()
}

// statsTimer starts the minute timer, unless it runs or the stats do not show.
func (m *Model) statsTimer() tea.Cmd {
	if m.statsTiming || !m.statsShown() {
		return nil
	}
	m.statsTiming = true
	return m.tick(time.Minute, func(time.Time) tea.Msg { return statsMinute{} })
}

// statsMinuteMsg starts the timer again while the stats show: the view is drawn
// again by the update itself.
func (m *Model) statsMinuteMsg() tea.Cmd {
	m.statsTiming = false
	return m.statsTimer()
}

// statsRows are the rows of the stats: for each bucket of usage its heading, with
// whether its ACA can be used, a row per window, and as of when; then each ACA that
// can be used but has told nothing.
func (m *Model) statsRows() []valueRow {
	now := time.Now()
	var rows []valueRow
	told := map[string]bool{}
	for i, b := range api.UsageBuckets(m.usage) {
		told[b.Aca] = true
		heading := valueRow{key: b.Aca, values: []string{api.AvailabilityText(b, now)}, gap: i > 0, paint: m.st.Success}
		if b.Bucket != "" {
			heading.key = b.Aca + " " + b.Bucket
		}
		if !b.Available {
			heading.paint = m.st.Failure
		}
		rows = append(rows, heading)
		for _, w := range b.Windows {
			rows = append(rows, valueRow{key: "  " + api.UsageLabel(w.Aca, w.Window), values: []string{usageText(w, now)}, paint: m.usagePaint(w)})
		}
		rows = append(rows, valueRow{key: "  as of", values: []string{b.ObservedAt.Local().Format("15:04")}, paint: m.st.Muted})
	}
	for _, a := range m.usageAcas {
		if !told[a.Id] {
			rows = append(rows, valueRow{key: a.Id, values: []string{"nothing observed yet"}, gap: len(rows) > 0, paint: m.st.Muted})
		}
	}
	return rows
}

// usageText is a window's line: the percentage, a bar, and when it resets.
func usageText(w api.UsageWindow, now time.Time) string {
	filled := min(usageBarCells, (w.UsedPercent*usageBarCells+50)/100)
	text := strconv.Itoa(w.UsedPercent) + "% " + strings.Repeat("▰", filled) + strings.Repeat("▱", usageBarCells-filled)
	switch {
	case w.Limited && w.ResetsAt != nil:
		return text + "  limited, resets " + api.ResetText(*w.ResetsAt, now)
	case w.Limited:
		return text + "  limited"
	case w.ResetsAt != nil:
		return text + "  resets " + api.ResetText(*w.ResetsAt, now)
	}
	return text
}

// usagePaint styles a window's line by how much is used: the failure style once
// limited, else the context styles from the fixed percentages.
func (m *Model) usagePaint(w api.UsageWindow) func(string) string {
	switch {
	case w.Limited:
		return m.st.Failure
	case w.UsedPercent >= usageCritical:
		return m.st.ContextCritical
	case w.UsedPercent >= usageWarning:
		return m.st.ContextWarning
	}
	return m.st.ContextFine
}

func (m *Model) drawStats(buf uv.Screen, area uv.Rectangle, st style.Style) {
	switch {
	case m.usageErr != "":
		uv.NewStyledString(st.Failure("stats could not be read: "+m.usageErr)).Draw(buf, area)
	case !m.usageRead:
		uv.NewStyledString(st.Muted("loading…")).Draw(buf, area)
	case len(m.usage) == 0 && len(m.usageAcas) == 0:
		uv.NewStyledString(st.Muted("no AI coding agent is installed and enabled")).Draw(buf, area)
	default:
		m.values.draw(buf, area, m.statsRows(), m.layout.focus == agent, st)
	}
}
