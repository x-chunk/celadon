package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.xchunk.org/teal"

	"github.com/x-chunk/celadon/internal/output"
	"github.com/x-chunk/celadon/internal/query"
)

type archiveFocus int

const (
	focusChats archiveFocus = iota
	focusResults
	focusQuery
)

// archiveTab browses and searches the archive: the chats on the left, the
// matches on the right, one message at a time on a card of its own.
type archiveTab struct {
	be *backend

	focus archiveFocus
	input textinput.Model

	chats        []teal.Chat
	chatsPage    int
	chatsTotal   int64
	chatsErr     error
	chatsLoading bool
	chatsSeq     int
	chatCur      selection

	// The search in force: what was typed, the chat it is narrowed to, and
	// the page shown.
	conds      []teal.Condition
	chat       *teal.Chat
	page       int
	results    *teal.SearchResult
	resultsErr error
	searching  bool
	searchSeq  int
	resultCur  selection
	parseErr   error

	detail *messageCard
}

// messageCard is one message opened from the results, with its versions
// once asked for.
type messageCard struct {
	msg        teal.Message
	full       *teal.Message
	versions   *teal.VersionList
	err        error
	loadingVer bool
	seq        int
	vp         viewport.Model
	content    string
}

func newArchiveTab(be *backend) *archiveTab {
	in := newInput()
	in.Prompt = "/ "
	in.Placeholder = `words  field=value  field~part  |field=value (OR)  "quoted words"`
	in.CharLimit = 512
	return &archiveTab{be: be, input: in, focus: focusChats}
}

func (t *archiveTab) title() string   { return "Archive" }
func (t *archiveTab) capturing() bool { return t.focus == focusQuery }

func (t *archiveTab) keys() []keyHelp {
	switch {
	case t.focus == focusQuery:
		return []keyHelp{{"enter", "search"}, {"esc", "cancel"}}
	case t.detail != nil:
		keys := []keyHelp{{"v", "edit history"}}
		if t.detail.answers() != 0 {
			keys = append(keys, keyHelp{"r", "the message it answers"})
		}
		return append(keys, keyHelp{"↑/↓", "scroll"}, keyHelp{"esc", "back"})
	case t.focus == focusChats:
		return []keyHelp{{"/", "search"}, {"enter", "search this chat"}, {"p", "portrait"}, {"[ ]", "page"}, {"→", "results"}, {"r", "reload"}}
	default:
		return []keyHelp{{"/", "search"}, {"enter", "open"}, {"[ ]", "page"}, {"c", "all chats"}, {"←", "chats"}}
	}
}

func (t *archiveTab) init() tea.Cmd { return t.loadChats(0) }

func (t *archiveTab) loadChats(page int) tea.Cmd {
	t.chatsSeq++
	t.chatsLoading = true
	t.chatsErr = nil
	return call(t.be, "chats", t.chatsSeq, func(ctx context.Context, c *teal.Client) reply[teal.ChatList] {
		return c.Archive.Chats(ctx, &teal.ChatsRequest{Page: page})
	})
}

// search runs the query in force at a page. Every search is billed as one
// query, so it only ever happens on a key the person pressed.
func (t *archiveTab) search(page int) tea.Cmd {
	req := t.request(page)
	return t.fetch(func(ctx context.Context, c *teal.Client) reply[teal.SearchResult] {
		return c.Archive.Search(ctx, req)
	})
}

// turn shows another page of the search in force. The pages of a query
// already run are free for 30 minutes after it ran; one that has expired
// since, or was dropped — the application keeps its 20 most recent — is run
// again, and billed as the search it is.
func (t *archiveTab) turn(page int) tea.Cmd {
	if t.results == nil || t.results.QueryID == "" {
		return t.search(page)
	}
	id, req := t.results.QueryID, t.request(page)
	return t.fetch(func(ctx context.Context, c *teal.Client) reply[teal.SearchResult] {
		turned := c.Archive.SearchPage(ctx, teal.SearchPageRequest{QueryID: id, Page: page})
		if teal.IsCode(turned.Error(), teal.CodeNotFound) {
			return c.Archive.Search(ctx, req)
		}
		return turned
	})
}

// request is the query in force, at a page.
func (t *archiveTab) request(page int) teal.SearchRequest {
	req := teal.SearchRequest{Conditions: t.conds, Page: page}
	if t.chat != nil {
		id := t.chat.ID
		req.Chat = &id
	}
	return req
}

// fetch puts the results pane in wait for an answer to the search.
func (t *archiveTab) fetch(fn func(context.Context, *teal.Client) reply[teal.SearchResult]) tea.Cmd {
	t.searchSeq++
	t.searching = true
	t.resultsErr = nil
	t.detail = nil
	return call(t.be, "search", t.searchSeq, fn)
}

func (t *archiveTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[teal.ChatList]:
		if msg.op != "chats" || msg.seq != t.chatsSeq {
			return nil
		}
		t.chatsLoading = false
		if msg.err != nil {
			t.chatsErr = msg.err
			return nil
		}
		t.chats, t.chatsPage, t.chatsTotal = msg.val.Chats, msg.val.Page, msg.val.Total
		t.chatCur = selection{}
		return nil

	case done[teal.SearchResult]:
		if msg.op != "search" || msg.seq != t.searchSeq {
			return nil
		}
		t.searching = false
		if msg.err != nil {
			t.resultsErr = msg.err
			return nil
		}
		res := msg.val
		t.results, t.page = &res, res.Page
		t.resultCur = selection{}
		t.focus = focusResults
		return nil

	case done[teal.Message]:
		if t.detail != nil && msg.op == "message" && msg.seq == t.detail.seq {
			if msg.err != nil {
				t.detail.err = msg.err
			} else {
				full := msg.val
				t.detail.full = &full
			}
		}
		return nil

	case done[teal.VersionList]:
		if t.detail != nil && msg.op == "versions" && msg.seq == t.detail.seq {
			t.detail.loadingVer = false
			if msg.err != nil {
				t.detail.err = msg.err
			} else {
				vl := msg.val
				t.detail.versions = &vl
			}
		}
		return nil

	case tea.KeyMsg:
		return t.key(msg)
	}
	return nil
}

func (t *archiveTab) key(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if t.focus == focusQuery {
		switch k {
		case "esc":
			t.focus = focusResults
			t.input.Blur()
			return nil
		case "enter":
			conds, err := query.ParseLine(t.input.Value())
			if err != nil {
				t.parseErr = err
				return nil
			}
			t.parseErr = nil
			t.conds = conds
			t.input.Blur()
			t.focus = focusResults
			return t.search(0)
		}
		var cmd tea.Cmd
		t.input, cmd = t.input.Update(msg)
		return cmd
	}

	if t.detail != nil {
		switch k {
		case "esc", "backspace":
			t.detail = nil
		case "v":
			if t.detail.versions == nil && !t.detail.loadingVer {
				t.detail.loadingVer = true
				id := t.detail.msg.ID
				return call(t.be, "versions", t.detail.seq, func(ctx context.Context, c *teal.Client) reply[teal.VersionList] {
					return c.Archive.Versions(ctx, id)
				})
			}
		case "r":
			// The message answered is read back by the same id as any
			// other, onto a card of its own.
			if id := t.detail.answers(); id != 0 {
				return t.open(teal.Message{ID: id})
			}
		default:
			var cmd tea.Cmd
			t.detail.vp, cmd = t.detail.vp.Update(msg)
			return cmd
		}
		return nil
	}

	switch k {
	case "/":
		t.focus = focusQuery
		return t.input.Focus()
	case "left", "h":
		t.focus = focusChats
	case "right", "l":
		t.focus = focusResults
	case "c":
		if t.chat != nil {
			t.chat = nil
			if t.results != nil {
				return t.search(0)
			}
		}
	}

	if t.focus == focusChats {
		switch k {
		case "up", "k":
			t.chatCur.move(-1, len(t.chats))
		case "down", "j":
			t.chatCur.move(1, len(t.chats))
		case "]":
			if int64((t.chatsPage+1)*max(len(t.chats), 1)) < t.chatsTotal && len(t.chats) > 0 {
				return t.loadChats(t.chatsPage + 1)
			}
		case "[":
			if t.chatsPage > 0 {
				return t.loadChats(t.chatsPage - 1)
			}
		case "r":
			return t.loadChats(t.chatsPage)
		case "enter":
			if len(t.chats) > 0 {
				ch := t.chats[t.chatCur.pos]
				t.chat = &ch
				return t.search(0)
			}
		case "p":
			if len(t.chats) > 0 {
				id := t.chats[t.chatCur.pos].ID
				return func() tea.Msg { return openPortraitMsg{chat: id} }
			}
		}
		return nil
	}

	var msgs []teal.Message
	if t.results != nil {
		msgs = t.results.Messages
	}
	switch k {
	case "up", "k":
		t.resultCur.move(-1, len(msgs))
	case "down", "j":
		t.resultCur.move(1, len(msgs))
	case "]":
		if t.results != nil && t.page+1 < t.results.Pages {
			return t.turn(t.page + 1)
		}
	case "[":
		if t.results != nil && t.page > 0 {
			return t.turn(t.page - 1)
		}
	case "enter":
		if len(msgs) > 0 {
			return t.open(msgs[t.resultCur.pos])
		}
	}
	return nil
}

// open puts a message on its card. The search result already carries the
// text; reading it again fetches what a search leaves out, such as the file
// id.
func (t *archiveTab) open(m teal.Message) tea.Cmd {
	t.searchSeq++ // a card replaces the list; nothing late may redraw it
	t.detail = &messageCard{msg: m, seq: t.searchSeq, vp: viewport.New(0, 0)}
	id := m.ID
	return call(t.be, "message", t.detail.seq, func(ctx context.Context, c *teal.Client) reply[teal.Message] {
		return c.Archive.Message(ctx, id)
	})
}

func (t *archiveTab) view(width, height int) string {
	top := ""
	if t.focus == focusQuery || t.input.Value() != "" {
		t.input.Width = max(width-4, 10)
		top = t.input.View()
		if t.parseErr != nil {
			top += "\n" + styleError.Render(output.Truncate(t.parseErr.Error(), width))
		}
	} else {
		top = styleMuted.Render("Press / to search. Each search is billed as a query; turning its pages is free for 30 minutes.")
	}
	topH := lipgloss.Height(top)
	bodyH := height - topH

	leftW := min(max(width/3, 26), 44)
	rightW := width - leftW
	left := pane(t.chatsTitle(), t.chatsBody(leftW-4, bodyH-3), leftW, bodyH, t.focus == focusChats && t.detail == nil)
	var right string
	if t.detail != nil {
		right = pane("Message #"+strconv.FormatInt(t.detail.msg.ID, 10), t.cardBody(rightW-4, bodyH-3), rightW, bodyH, true)
	} else {
		right = pane(t.resultsTitle(), t.resultsBody(rightW-4, bodyH-3), rightW, bodyH, t.focus == focusResults)
	}
	return top + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (t *archiveTab) chatsTitle() string {
	if t.chatsTotal > 0 {
		return fmt.Sprintf("Chats · %d · page %d", t.chatsTotal, t.chatsPage+1)
	}
	return "Chats"
}

func (t *archiveTab) chatsBody(width, height int) string {
	switch {
	case t.chatsErr != nil:
		return errorView(t.chatsErr, width)
	case t.chats == nil && t.chatsLoading:
		return loading("chats")
	case len(t.chats) == 0:
		return styleMuted.Render("The archive holds no chats.")
	}
	from, to := t.chatCur.window(len(t.chats), height)
	var b strings.Builder
	for i := from; i < to; i++ {
		ch := t.chats[i]
		count := strconv.FormatInt(ch.Messages, 10)
		name := output.Truncate(output.OneLine(output.Or(ch.Title)), max(width-len(count)-1, 1))
		row := name + strings.Repeat(" ", max(width-output.Width(name)-len(count), 1)) + count
		b.WriteString(line(row, width, i == t.chatCur.pos && t.focus == focusChats) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (t *archiveTab) resultsTitle() string {
	title := "Results"
	if t.results != nil {
		title += fmt.Sprintf(" · %s · page %d/%d", plural(t.results.Total, "match", "matches"), t.page+1, max(t.results.Pages, 1))
	}
	if t.chat != nil {
		title += " · in " + output.OneLine(output.Or(t.chat.Title))
	}
	return title
}

func (t *archiveTab) resultsBody(width, height int) string {
	switch {
	case t.searching:
		return loading("matches")
	case t.resultsErr != nil:
		return errorView(t.resultsErr, width)
	case t.results == nil:
		return styleMuted.Render(wrapText("Pick a chat and press enter to read it, or press / to search the whole archive.", width))
	case len(t.results.Messages) == 0:
		return styleMuted.Render("No messages match.")
	}
	msgs := t.results.Messages
	from, to := t.resultCur.window(len(msgs), height)
	var b strings.Builder
	for i := from; i < to; i++ {
		m := msgs[i]
		text := output.OneLine(m.Text)
		if text == "" && m.MediaType != "" {
			text = "[" + m.MediaType + "]"
		}
		if m.ReplyToID != 0 || m.ReplyToMessageID != 0 {
			text = "↩ " + text
		}
		if m.Deleted {
			text = "✗ " + text
		}
		row := fmt.Sprintf("%-14s %s  %s", output.Truncate(output.OneLine(output.Sender(m)), 14), m.CreatedAt.Local().Format("01-02 15:04"), text)
		b.WriteString(line(row, width, i == t.resultCur.pos && t.focus == focusResults) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (t *archiveTab) cardBody(width, height int) string {
	d := t.detail
	m := d.msg
	if d.full != nil {
		m = *d.full
	}
	chat := strconv.FormatInt(m.Chat, 10)
	if name := output.ChatName(m); name != "" {
		chat += " · " + name
	}
	pairs := []field{
		{"From", output.SenderFull(m)},
		{"Chat", chat},
		{"Sent", output.Time(m.CreatedAt)},
		{"Telegram id", strconv.FormatInt(m.MessageID, 10)},
	}
	if reply := output.ReplyTo(m); reply != "" {
		pairs = append(pairs, field{"Answers", reply})
	}
	if m.MediaType != "" {
		media := m.MediaType
		if m.FileID != "" {
			media += " · " + m.FileID
		}
		pairs = append(pairs, field{"Media", media})
	}
	if m.Deleted {
		deleted := "in Telegram"
		if m.DeletedAt != 0 {
			deleted += " " + output.Unix(m.DeletedAt)
		}
		pairs = append(pairs, field{"Deleted", deleted + "; kept in the archive"})
	}
	var b strings.Builder
	b.WriteString(fields(pairs, width) + "\n\n")
	b.WriteString(wrapText(m.Text, width))
	if d.err != nil {
		b.WriteString("\n\n" + errorView(d.err, width))
	}
	switch {
	case d.loadingVer:
		b.WriteString("\n\n" + loading("the edit history"))
	case d.versions != nil:
		b.WriteString("\n\n" + styleHeading.Render("Edit history") + "\n")
		for _, v := range d.versions.Versions {
			label := output.Unix(v.At)
			if v.Current {
				label += " (current)"
			}
			b.WriteString(styleMuted.Render(label) + "\n" + wrapText(output.Or(v.Text), width) + "\n\n")
		}
	case d.edits() > 0:
		edits := "once"
		if n := d.edits(); n > 1 {
			edits = fmt.Sprintf("%d times", n)
		}
		b.WriteString("\n\n" + styleMuted.Render("Edited "+edits+" — press v for the history."))
	}
	content := strings.TrimRight(b.String(), "\n")
	d.vp.Width, d.vp.Height = width, height
	if content != d.content {
		d.content = content
		d.vp.SetContent(content)
	}
	return d.vp.View()
}

// edits is how many times the message on the card was edited. A search
// counts the texts a message no longer carries and a read by id need not,
// so the search's count stands when the read has none.
func (d *messageCard) edits() int {
	if d.full != nil && d.full.Versions > 0 {
		return d.full.Versions
	}
	return d.msg.Versions
}

// answers is the archive's id of the message the one on the card answers,
// zero when it answers nothing the archive holds.
func (d *messageCard) answers() int64 {
	if d.full != nil && d.full.ReplyToID != 0 {
		return d.full.ReplyToID
	}
	return d.msg.ReplyToID
}
