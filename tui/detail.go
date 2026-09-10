package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

type detailState struct {
	title string
	view  viewport.Model
}

func newDetailState(title, content string, width, height int) *detailState {
	v := viewport.New(viewport.WithWidth(max(width-8, 20)), viewport.WithHeight(max(min(height/2, 14), 4)))
	v.SetContent(terminalSafe(content))
	return &detailState{title: title, view: v}
}

func (d *detailState) View(width int) string {
	d.view.SetWidth(max(width-8, 20))
	return menuStyle.Width(max(width-4, 20)).Render(accentStyle.Copy().Bold(true).Render(terminalSafe(d.title)) + "\n" +
		d.view.View() + "\n" + mutedStyle.Render("PgUp/PgDn scroll · Esc close"))
}

func (m *model) updateDetail(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == tea.KeyEsc {
		m.detail = nil
		m.layout()
		return m, nil
	}
	var cmd tea.Cmd
	m.detail.view, cmd = m.detail.view.Update(msg)
	return m, cmd
}

func (m *model) handleShowCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.appendBlock(chatBlock{kind: blockError, content: "usage: /show <tool-call-id>"})
		return m.dispatchIfIdle()
	}
	var found *chatBlock
	for i := range m.messages {
		block := &m.messages[i]
		if block.kind != blockToolCall || (block.id != args[0] && !strings.HasPrefix(block.id, args[0])) {
			continue
		}
		if found != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "ambiguous tool id prefix: " + args[0]})
			return m, nil
		}
		found = block
	}
	if found == nil {
		m.appendBlock(chatBlock{kind: blockError, content: "tool call not found: " + args[0]})
		return m, nil
	}
	m.detail = newDetailState(fmt.Sprintf("%s · %s", found.toolName, shortID(found.id)), found.content, m.width, m.height)
	m.layout()
	return m, nil
}
