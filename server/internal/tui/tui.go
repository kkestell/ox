// Package tui is the Ox terminal client's interface: a transcript, an
// approval dialog, a composer, and pickers for sessions and models.
package tui

import (
	"errors"
	"image/color"
	"slices"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	protocol "github.com/coder/acp-go-sdk"

	"ox/internal/client"
	"ox/internal/settings"
)

// Run shows the session until the user quits or the server closes the
// connection. favorites are saved to the global settings file at configPath.
func Run(conn *client.Conn, session *client.Session, favorites []string, configPath string) error {
	m := &model{conn: conn, session: session, favorites: favorites, configPath: configPath, focused: true}
	m.listen = func() tea.Msg { return conn.Next() }
	// The theme's exact colors look the same under every terminal color
	// scheme, so they are never reduced to a palette.
	if _, err := tea.NewProgram(m, tea.WithColorProfile(colorprofile.TrueColor)).Run(); err != nil {
		return err
	}
	return m.err
}

// model is the client's state. While a session request is in flight, it
// defers every other message, so keys and server messages apply in order to
// the session state the request produces.
type model struct {
	conn    *client.Conn
	session *client.Session
	// listen waits for the next server event. Each handled event listens
	// again, so events arrive one at a time and in order.
	listen           tea.Cmd
	view             transcript
	input            input
	approvalSelected int
	approvalScroll   int
	showThinking     bool
	toolOutput       toolOutput
	picker           *picker
	resumeAfterTurn  bool
	// favorites are model IDs, in the order they were added.
	favorites  []string
	configPath string

	width, height int
	// layout is what the last frame gave each pageable region.
	layout layout
	// focused stays true in terminals that never report focus, so they get
	// no bells or unseen results.
	focused bool
	// unseen is the result of the last turn when it ended while the terminal
	// was unfocused.
	unseen string

	waiting  bool
	deferred []tea.Msg
	// err ends the program when it is set.
	err error
}

// result applies a session request's response.
type result func(m *model) tea.Cmd

type tick struct{}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.listen, m.tick())
}

// tick redraws every second, so running thinking placeholders count.
func (m *model) tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tick{} })
}

// request runs work off the event loop and defers other messages until the
// result it returns applies.
func (m *model) request(work func() result) tea.Cmd {
	m.waiting = true
	return func() tea.Msg { return work() }
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.waiting {
		apply, ok := msg.(result)
		if !ok {
			m.deferred = append(m.deferred, msg)
			return m, nil
		}
		m.waiting = false
		cmds := []tea.Cmd{apply(m)}
		for len(m.deferred) > 0 && !m.waiting {
			next := m.deferred[0]
			m.deferred = m.deferred[1:]
			cmds = append(cmds, m.update(next))
		}
		return m, m.done(tea.Batch(cmds...))
	}
	return m, m.done(m.update(msg))
}

// done quits once an error is set.
func (m *model) done(cmd tea.Cmd) tea.Cmd {
	if m.err != nil {
		return tea.Quit
	}
	return cmd
}

func (m *model) update(msg tea.Msg) tea.Cmd {
	now := time.Now()
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tick:
		return m.tick()
	case tea.FocusMsg:
		m.focused = true
		m.unseen = ""
	case tea.BlurMsg:
		m.focused = false
	case tea.PasteMsg:
		if m.picker != nil {
			for _, r := range msg.Content {
				if !unicode.IsControl(r) {
					m.picker.query += string(r)
				}
			}
			m.picker.filter()
		} else {
			m.input.paste(msg.Content)
		}
	case tea.KeyPressMsg:
		return m.key(msg, now)
	case tea.MouseWheelMsg:
		m.mouse(msg.Mouse())
	case client.Event:
		cmd := m.event(msg, now)
		if _, closed := msg.(client.Closed); closed {
			return cmd
		}
		if m.resumeAfterTurn && !m.session.Busy {
			m.resumeAfterTurn = false
			return tea.Batch(cmd, m.listen, m.openSessionPicker())
		}
		return tea.Batch(cmd, m.listen)
	}
	return nil
}

// fail ends the program with the error.
func (m *model) fail(err error) {
	if err != nil && m.err == nil {
		m.err = err
	}
}

// bell rings when the terminal is unfocused.
func (m *model) bell() tea.Cmd {
	if m.focused {
		return nil
	}
	return tea.Raw("\a")
}

func (m *model) notice(text string, c color.Color) {
	m.view.notice(text, c, time.Now())
}

// event handles a message from the server or the end of a prompt.
func (m *model) event(event client.Event, now time.Time) tea.Cmd {
	switch event := event.(type) {
	case client.Update:
		if m.session.Accepts(event.SessionID) {
			m.session.Update(event.Update)
			m.view.update(event.Update, event.Name, now)
		}
	case client.Diagnostic:
		m.view.notice(string(event), dim, now)
	case *client.Permission:
		if !m.session.Accepts(event.SessionID) {
			m.fail(event.Cancel())
			return nil
		}
		m.fail(m.session.Ask(event))
		if m.session.Permission != nil {
			m.approvalSelected, m.approvalScroll = 0, 0
			m.view.changed()
			return m.bell()
		}
	case client.Finished:
		if !m.session.Accepts(event.SessionID) {
			return nil
		}
		queued, err := m.session.Finished()
		m.fail(err)
		m.view.endTurn(now)
		if !m.focused {
			m.unseen = "finished"
			if event.Err != nil {
				m.unseen = "turn error"
			}
		}
		if event.Err != nil {
			m.view.notice("Turn error: "+event.Err.Error(), red, now)
		}
		if queued != "" {
			m.view.user(queued, now)
		}
		return m.bell()
	case client.Closed:
		m.fail(errors.New("server closed the ACP connection"))
	}
	return nil
}

// commands returns the slash command names the composer completes: the
// client's own and the session's available commands, sorted.
func (m *model) commands() []string {
	commands := append(slices.Clone(m.session.Commands), "model", "new", "quit", "resume")
	slices.Sort(commands)
	return commands
}

func (m *model) quit() tea.Cmd {
	m.fail(m.session.Cancel())
	return tea.Quit
}

// key handles one key press.
func (m *model) key(key tea.KeyPressMsg, now time.Time) tea.Cmd {
	control := key.Mod.Contains(tea.ModCtrl)
	text := key.Text != "" && !key.Mod.Contains(tea.ModCtrl) && !key.Mod.Contains(tea.ModAlt)
	if m.picker != nil {
		return m.pickerKey(key, control, text)
	}
	approving := m.session.Permission != nil
	switch {
	case key.Code == tea.KeyTab && !control && !key.Mod.Contains(tea.ModAlt):
		forward := !key.Mod.Contains(tea.ModShift)
		if ghost := m.input.ghostText(m.commands()); forward && ghost != "" {
			m.input.paste(ghost)
			return nil
		}
		return m.cycle(protocol.SessionConfigOptionCategoryMode, forward, "Mode change failed")
	case control && (key.Code == 'c' || key.Code == 'd'):
		return m.quit()
	case control && key.Code == 't':
		m.showThinking = !m.showThinking
	case control && (key.Code == 'e' || key.Code == 'E'):
		return m.cycle(protocol.SessionConfigOptionCategoryThoughtLevel, true, "Effort change failed")
	case control && key.Code == 'o':
		m.toolOutput = m.toolOutput.next()
	case control && key.Code == 'u':
		m.input.clear()
	case text:
		m.input.insert(key.Text)
	case key.Code == tea.KeyEnter && key.Mod.Contains(tea.ModShift):
		m.input.newline()
	case key.Code == tea.KeyEnter:
		return m.enter(now)
	case key.Code == tea.KeyEscape:
		if approving {
			m.answer(m.rejectOption())
		} else if m.session.Busy {
			m.fail(m.session.Cancel())
		}
	case key.Code == tea.KeyUp:
		if approving {
			m.approvalSelected = max(m.approvalSelected-1, 0)
		} else {
			m.input.up()
		}
	case key.Code == tea.KeyDown:
		if approving {
			m.approvalSelected = min(m.approvalSelected+1, max(len(m.session.Permission.Options)-1, 0))
		} else {
			m.input.down()
		}
	case key.Code == tea.KeyLeft:
		m.input.left()
	case key.Code == tea.KeyRight:
		m.input.right()
	case key.Code == tea.KeyHome:
		m.input.home()
	case key.Code == tea.KeyEnd:
		m.input.end()
		m.view.end()
	case key.Code == tea.KeyBackspace:
		m.input.backspace()
	case key.Code == tea.KeyDelete:
		m.input.delete()
	case key.Code == tea.KeyPgUp && approving:
		m.approvalScroll = max(m.approvalScroll-page(m.layout.approvalHeight), 0)
	case key.Code == tea.KeyPgDown && approving:
		last := max(m.layout.approvalBodyLines-m.layout.approvalHeight, 0)
		m.approvalScroll = min(m.approvalScroll+page(m.layout.approvalHeight), last)
	case key.Code == tea.KeyPgUp:
		m.view.pageUp(m.layout.height, m.layout.lines)
	case key.Code == tea.KeyPgDown:
		m.view.pageDown(m.layout.height, m.layout.lines)
	}
	return nil
}

// enter runs a slash command, sends the input, or answers the approval
// dialog when the input is empty.
func (m *model) enter(now time.Time) tea.Cmd {
	if m.resumeAfterTurn {
		return nil
	}
	if m.input.empty() {
		if m.session.Permission != nil {
			m.answer(m.approvalSelected)
		}
		return nil
	}
	switch m.input.text {
	case "/resume":
		m.input.clear()
		switch {
		case !m.conn.CanResume:
			m.view.notice("Session resume is unavailable", red, now)
		case m.session.Busy:
			m.session.Queued = ""
			m.resumeAfterTurn = true
			m.fail(m.session.Cancel())
		default:
			return m.openSessionPicker()
		}
	case "/quit":
		return m.quit()
	case "/new":
		m.input.clear()
		return m.newSession()
	case "/model":
		m.input.clear()
		m.openModelPicker()
	default:
		if word := m.input.unknownCommand(m.commands()); word != "" {
			m.view.notice("Unknown command "+word, red, now)
		} else {
			m.submit(now)
		}
	}
	return nil
}

// submit sends the input. During a turn the prompt is queued, and its message
// joins the transcript when the turn finishes.
func (m *model) submit(now time.Time) {
	busy := m.session.Busy
	sent, err := m.session.Prompt(m.input.text)
	m.fail(err)
	if sent {
		text := m.input.take()
		if !busy {
			m.view.user(text, now)
		}
	}
}

func (m *model) answer(index int) {
	_, err := m.session.Answer(index + 1)
	m.fail(err)
	m.approvalSelected, m.approvalScroll = 0, 0
}

// rejectOption is the first rejecting option, else the last option.
func (m *model) rejectOption() int {
	options := m.session.Permission.Options
	for i, option := range options {
		if option.Kind == protocol.PermissionOptionKindRejectOnce || option.Kind == protocol.PermissionOptionKindRejectAlways {
			return i
		}
	}
	return max(len(options)-1, 0)
}

// cycle chooses the next or previous choice of the select option in the
// category.
func (m *model) cycle(category protocol.SessionConfigOptionCategory, forward bool, failure string) tea.Cmd {
	option, value, ok := nextChoice(m.session.ConfigOptions, category, forward)
	if !ok {
		return nil
	}
	return m.setConfigOption(option, value, func(m *model, err error) {
		if err != nil {
			m.notice(failure+": "+err.Error(), red)
		}
	})
}

// setConfigOption sets a session config option, then calls done with the
// request's error.
func (m *model) setConfigOption(option protocol.SessionConfigId, value protocol.SessionConfigValueId, done func(*model, error)) tea.Cmd {
	conn, id := m.conn, m.session.ID
	return m.request(func() result {
		options, err := conn.SetConfigOption(id, option, value)
		return func(m *model) tea.Cmd {
			if err == nil {
				m.session.ConfigOptions = options
			}
			done(m, err)
			return nil
		}
	})
}

func nextChoice(options []protocol.SessionConfigOption, category protocol.SessionConfigOptionCategory, forward bool) (protocol.SessionConfigId, protocol.SessionConfigValueId, bool) {
	option := selectOption(options, category)
	if option == nil {
		return "", "", false
	}
	all := choices(option)
	count := len(all)
	current := slices.IndexFunc(all, func(choice protocol.SessionConfigSelectOption) bool { return choice.Value == option.CurrentValue })
	if count < 2 || current < 0 {
		return "", "", false
	}
	next := (current + 1) % count
	if !forward {
		next = (current + count - 1) % count
	}
	return option.Id, all[next].Value, true
}

// closeSession closes the open session, if any, and returns how to record
// the close. It runs in a request's goroutine.
func closeSession(conn *client.Conn, active bool, id protocol.SessionId) (func(*model), error) {
	if !active {
		return func(*model) {}, nil
	}
	if err := conn.CloseSession(id); err != nil {
		return nil, err
	}
	return func(m *model) { m.fail(m.session.Closed()) }, nil
}

func (m *model) newSession() tea.Cmd {
	conn, active, id := m.conn, m.session.Active(), m.session.ID
	return m.request(func() result {
		closed, err := closeSession(conn, active, id)
		if err != nil {
			return func(m *model) tea.Cmd {
				m.notice("New session failed: "+err.Error(), red)
				return nil
			}
		}
		created, err := conn.NewSession()
		return func(m *model) tea.Cmd {
			closed(m)
			m.view = transcript{}
			if err != nil {
				m.notice("New session failed: "+err.Error(), red)
			} else {
				m.session.Opened(created.SessionId, created.ConfigOptions)
			}
			return nil
		}
	})
}

func (m *model) openSessionPicker() tea.Cmd {
	conn := m.conn
	return m.request(func() result {
		sessions, err := conn.ListSessions()
		return func(m *model) tea.Cmd {
			if err != nil {
				m.notice("Session list failed: "+err.Error(), red)
			} else {
				m.picker = newSessionPicker(sessions)
			}
			return nil
		}
	})
}

// openModelPicker opens the model picker on Favorites when the session offers
// a favorite, else on All, with the current model selected when it is shown.
func (m *model) openModelPicker() {
	option := selectOption(m.session.ConfigOptions, protocol.SessionConfigOptionCategoryModel)
	if option == nil {
		m.notice("Model choice is unavailable", red)
		return
	}
	var models []modelChoice
	for _, choice := range choices(option) {
		models = append(models, newModelChoice(choice))
	}
	current := slices.IndexFunc(models, func(model modelChoice) bool { return model.value == option.CurrentValue })
	m.picker = newModelPicker(newModelRows(models, m.favorites))
	// The next frame scrolls it into view.
	m.picker.selected = max(slices.Index(m.picker.matches, current), 0)
}

func (m *model) pickerKey(key tea.KeyPressMsg, control, text bool) tea.Cmd {
	p := m.picker
	rows := m.layout.height
	// The session picker stays open until a session is active.
	closable := p.models != nil || m.session.Active()
	switch {
	case key.Code == tea.KeyUp:
		p.moveTo(p.selected-1, rows)
	case key.Code == tea.KeyDown:
		p.moveTo(p.selected+1, rows)
	case key.Code == tea.KeyPgUp:
		p.moveTo(p.selected-rows, rows)
	case key.Code == tea.KeyPgDown:
		p.moveTo(p.selected+rows, rows)
	case key.Code == tea.KeyHome:
		p.moveTo(0, rows)
	case key.Code == tea.KeyEnd:
		p.moveTo(len(p.matches)-1, rows)
	case key.Code == tea.KeyLeft || key.Code == tea.KeyRight:
		if p.models != nil && p.models.hasFavorites() {
			p.models.showingFavorites = key.Code == tea.KeyLeft
			p.filter()
		}
	case control && key.Code == 'f':
		m.toggleFavorite()
	case key.Code == tea.KeyEscape && closable:
		m.picker = nil
	case key.Code == tea.KeyEnter:
		return m.choose()
	case control && (key.Code == 'c' || key.Code == 'd'):
		return tea.Quit
	case text:
		p.query += key.Text
		p.filter()
	case key.Code == tea.KeyBackspace:
		if runes := []rune(p.query); len(runes) > 0 {
			p.query = string(runes[:len(runes)-1])
		}
		p.filter()
	}
	return nil
}

// toggleFavorite adds the selected model to the favorites or removes it, and
// saves the favorites.
func (m *model) toggleFavorite() {
	p := m.picker
	if p.models == nil || p.selected >= len(p.matches) {
		return
	}
	id := string(p.models.all[p.matches[p.selected]].value)
	favorites := slices.Clone(m.favorites)
	if position := slices.Index(favorites, id); position >= 0 {
		favorites = slices.Delete(favorites, position, position+1)
	} else {
		favorites = append(favorites, id)
	}
	if err := settings.SaveFavorites(m.configPath, favorites); err != nil {
		p.err = "Favorite failed: " + err.Error()
		return
	}
	p.models.setFavorites(favorites)
	m.favorites = favorites
	selected := p.selected
	p.filter()
	p.moveTo(selected, m.layout.height)
}

// choose loads the selected session or chooses the selected model.
func (m *model) choose() tea.Cmd {
	p := m.picker
	if p.selected >= len(p.matches) {
		return nil
	}
	index := p.matches[p.selected]
	if p.models != nil {
		option := selectOption(m.session.ConfigOptions, protocol.SessionConfigOptionCategoryModel)
		if option == nil {
			p.err = "Model choice is unavailable"
			return nil
		}
		return m.setConfigOption(option.Id, p.models.all[index].value, func(m *model, err error) {
			if err != nil {
				m.picker.err = "Model change failed: " + err.Error()
			} else {
				m.picker = nil
			}
		})
	}
	id := p.sessions[index].SessionId
	conn, active, current := m.conn, m.session.Active(), m.session.ID
	return m.request(func() result {
		closed, err := closeSession(conn, active, current)
		if err != nil {
			return func(m *model) tea.Cmd {
				m.picker.err = "Close failed: " + err.Error()
				return nil
			}
		}
		loaded, err := conn.LoadSession(id)
		return func(m *model) tea.Cmd {
			closed(m)
			m.view = transcript{}
			if err != nil {
				m.picker.err = "Load failed: " + err.Error()
				return nil
			}
			m.session.Opened(id, loaded.ConfigOptions)
			m.picker = nil
			m.input.clear()
			return nil
		}
	})
}

func (m *model) mouse(mouse tea.Mouse) {
	if m.picker != nil || m.layout.height == 0 || mouse.Y >= m.layout.height+2*marginY {
		return
	}
	switch mouse.Button {
	case tea.MouseWheelUp:
		m.view.scrollUp(m.layout.height, m.layout.lines, 1)
	case tea.MouseWheelDown:
		m.view.scrollDown(m.layout.height, m.layout.lines, 1)
	}
}

// status is the terminal title's state.
func (m *model) status() string {
	switch {
	case m.picker != nil && m.picker.models != nil:
		return "choose model"
	case m.picker != nil:
		return "resume session"
	case m.session.Permission != nil:
		return "needs permission"
	case m.session.Busy:
		return "working"
	case m.unseen != "":
		return m.unseen
	}
	return "ready"
}

// View draws the frame and keeps its layout, since paging and scrolling
// move by what the last frame showed.
func (m *model) View() tea.View {
	view := tea.View{AltScreen: true, ReportFocus: true, MouseMode: tea.MouseModeCellMotion, WindowTitle: "ox: " + m.status()}
	if m.width == 0 || m.height == 0 {
		return view
	}
	if m.picker != nil {
		m.picker.moveTo(m.picker.selected, pickerRows(m.height))
	}
	buf := uv.NewBuffer(m.width, m.height)
	cursor, layout := draw(buf, m.screen(time.Now()))
	m.layout = layout
	m.approvalScroll = min(m.approvalScroll, max(layout.approvalBodyLines-layout.approvalHeight, 0))
	view.SetContent(buf.Render())
	view.Cursor = tea.NewCursor(cursor.X, cursor.Y)
	return view
}

func (m *model) screen(now time.Time) screen {
	return screen{
		view: &m.view, input: &m.input, commands: m.commands(), approval: m.session.Permission,
		approvalSelected: m.approvalSelected, approvalScroll: m.approvalScroll, picker: m.picker,
		settings: settingsSummary(m.session.ConfigOptions), usage: usageSummary(m.session.Usage),
		showThinking: m.showThinking, toolOutput: m.toolOutput, now: now,
	}
}
