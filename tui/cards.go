package tui

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basenana/friday/core/actor/events"
)

const cardPreviewLimit = 2 << 20

type cardState struct {
	id, kind, title string
	document        map[string]any
	dismissed       bool
	sourceVersion   uint64
	sourceLoading   bool
	sourceLoaded    bool
	sourceRows      []any
	sourceDiff      string
	sourceErr       string
}

type cardSourceLoadedMsg struct {
	cardID, diff string
	version      uint64
	rows         []any
	err          error
}

func (m *model) handleCardEvent(evt events.Event) tea.Cmd {
	switch evt.Name {
	case events.CustomCardEmitted:
		var d events.CardEmittedBody
		if events.DecodePayload(evt, &d) != nil || d.CardID == "" {
			return nil
		}
		card := &cardState{id: d.CardID, kind: d.Kind, title: d.Title,
			document: map[string]any{"title": d.Title, "component": cloneMap(d.Component)}}
		m.cards[d.CardID] = card
		m.appendBlock(chatBlock{kind: blockCard, id: d.CardID, card: card})
		return m.prepareCardSource(card)
	case events.CustomCardUpdated:
		var d events.CardUpdatedBody
		if events.DecodePayload(evt, &d) != nil {
			return nil
		}
		card := m.cards[d.CardID]
		if card == nil {
			m.appendBlock(chatBlock{kind: blockError, content: "card update for unknown id " + shortID(d.CardID)})
			return nil
		}
		doc, err := applyCardPatch(card.document, d.Patch)
		if err != nil {
			m.appendBlock(chatBlock{kind: blockError, content: "card patch " + shortID(d.CardID) + ": " + err.Error()})
			return nil
		}
		card.document = doc
		card.title, _ = doc["title"].(string)
		m.invalidateCard(card)
		return m.prepareCardSource(card)
	case events.CustomCardDismissed:
		var d events.CardDismissedBody
		if events.DecodePayload(evt, &d) == nil {
			if card := m.cards[d.CardID]; card != nil {
				card.dismissed = true
				m.invalidateCard(card)
			}
		}
	}
	return nil
}

func (m *model) invalidateCard(card *cardState) {
	for i := range m.messages {
		if m.messages[i].card == card {
			m.messages[i].rendered = ""
		}
	}
}

func (m *model) renderCard(card *cardState) string {
	if card == nil || card.dismissed {
		return ""
	}
	component, _ := card.document["component"].(map[string]any)
	title := card.title
	if title == "" {
		title = card.kind
	}
	header := accentStyle.Copy().Bold(true).Render("◆ " + terminalSafe(title))
	var body string
	switch card.kind {
	case "table", "spreadsheet":
		body = m.renderGridCard(card, component)
	case "diff":
		body = m.renderDiffCard(card, component)
	case "plan":
		body = renderPlanCard(component)
	case "chart":
		body = renderChartCard(component)
	case "file":
		body = renderFileCard(component)
	case "image":
		body = renderImageCard(component)
	case "html-preview":
		body = fmt.Sprintf("HTML preview · %s", terminalSafe(stringValue(component["path"])))
	case "mermaid":
		body = "```mermaid\n" + stringValue(component["source"]) + "\n```"
		body = m.markdown(body)
	case "form":
		body = renderFormPreview(component)
	case "decision":
		body = renderDecisionCard(component)
	case "igv-command":
		body = renderIGVCard(component)
	default:
		body = prettyJSON(component)
	}
	if _, err := m.cardOpenTarget(card); err == nil {
		body += "\n" + mutedStyle.Render("/open "+shortID(card.id)+" · opens after confirmation")
	}
	style := menuStyle.Copy().BorderForeground(themeAccent)
	return style.Width(max(m.width-4, 20)).Render(header + "\n" + strings.TrimRight(body, "\n"))
}

func (m *model) renderGridCard(card *cardState, component map[string]any) string {
	columns, _ := component["columns"].([]any)
	rows, _ := component["rows"].([]any)
	if len(rows) == 0 {
		if _, ok := component["source"].(map[string]any); ok {
			switch {
			case card.sourceErr != "":
				return errorStyle.Render(terminalSafe(card.sourceErr))
			case card.sourceLoading:
				return mutedStyle.Render("loading source…")
			case card.sourceLoaded:
				rows = card.sourceRows
			}
		}
	}
	if len(columns) == 0 {
		return prettyJSON(rows)
	}
	type col struct{ key, label string }
	cols := make([]col, 0, len(columns))
	for _, raw := range columns {
		if c, ok := raw.(map[string]any); ok {
			key := stringValue(c["key"])
			if key == "" {
				key = stringValue(c["name"])
			}
			label := stringValue(c["label"])
			if label == "" {
				label = key
			}
			cols = append(cols, col{key, label})
		}
	}
	if len(cols) == 0 {
		return prettyJSON(rows)
	}
	maxRows := min(len(rows), 200)
	data := make([][]string, 0, maxRows+1)
	head := make([]string, len(cols))
	for i := range cols {
		head[i] = cols[i].label
	}
	data = append(data, head)
	for _, raw := range rows[:maxRows] {
		line := make([]string, len(cols))
		switch row := raw.(type) {
		case map[string]any:
			for i, c := range cols {
				line[i] = fmt.Sprint(row[c.key])
			}
		case []any:
			for i := range line {
				if i < len(row) {
					line[i] = fmt.Sprint(row[i])
				}
			}
		}
		data = append(data, line)
	}
	widths := make([]int, len(cols))
	available := max((m.width-8)/len(cols), 6)
	for _, row := range data {
		for i, value := range row {
			value = strings.Join(strings.Fields(terminalSafe(value)), " ")
			row[i] = value
			widths[i] = min(max(widths[i], lipgloss.Width(value)), available)
		}
	}
	var lines []string
	for ri, row := range data {
		cells := make([]string, len(row))
		for i, value := range row {
			value = truncateWidth(value, widths[i])
			cells[i] = lipgloss.NewStyle().Width(widths[i]).Render(value)
		}
		line := strings.Join(cells, " │ ")
		if ri == 0 {
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		lines = append(lines, line)
	}
	if len(rows) > maxRows {
		lines = append(lines, fmt.Sprintf("… (%d more rows)", len(rows)-maxRows))
	}
	return strings.Join(lines, "\n")
}

func (m *model) loadGridSource(source map[string]any) ([]any, error) {
	return loadGridSourceAt(m.workdir, source)
}

func loadGridSourceAt(workdir string, source map[string]any) ([]any, error) {
	path, err := safePathWithin(workdir, stringValue(source["path"]))
	if err != nil {
		return nil, err
	}
	data, truncated, err := readPreview(path)
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("source exceeds %d MiB preview limit; use /open", cardPreviewLimit>>20)
	}
	switch stringValue(source["format"]) {
	case "json":
		var rows []any
		if err := json.Unmarshal(data, &rows); err != nil {
			return nil, err
		}
		return rows, nil
	case "csv", "tsv":
		r := csv.NewReader(bytes.NewReader(data))
		if stringValue(source["format"]) == "tsv" {
			r.Comma = '\t'
		}
		records, err := r.ReadAll()
		if err != nil || len(records) == 0 {
			return nil, err
		}
		head := records[0]
		rows := make([]any, 0, len(records)-1)
		for _, record := range records[1:] {
			row := make(map[string]any, len(head))
			for i, key := range head {
				if i < len(record) {
					row[key] = record[i]
				}
			}
			rows = append(rows, row)
		}
		return rows, nil
	default:
		return nil, errors.New("unsupported grid source format")
	}
}

func (m *model) renderDiffCard(card *cardState, component map[string]any) string {
	diff := terminalSafe(stringValue(component["unified_diff"]))
	if diff == "" {
		if _, ok := component["source"].(map[string]any); ok {
			switch {
			case card.sourceErr != "":
				return errorStyle.Render(terminalSafe(card.sourceErr))
			case card.sourceLoading:
				return mutedStyle.Render("loading source…")
			case card.sourceLoaded:
				diff = terminalSafe(card.sourceDiff)
			}
		}
	}
	lines := strings.Split(diff, "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			lines[i] = lipgloss.NewStyle().Foreground(themeAdded).Render(line)
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			lines[i] = lipgloss.NewStyle().Foreground(themeRemoved).Render(line)
		case strings.HasPrefix(line, "@@"):
			lines[i] = accentStyle.Render(line)
		}
	}
	return truncateLines(strings.Join(lines, "\n"), 200)
}

func renderPlanCard(component map[string]any) string {
	steps, _ := component["steps"].([]any)
	var lines []string
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		status := stringValue(step["status"])
		icon := map[string]string{"done": "✓", "active": "●", "running": "●", "blocked": "!", "failed": "✗"}[status]
		if icon == "" {
			icon = "○"
		}
		title := stringValue(step["title"])
		detail := stringValue(step["detail"])
		if detail == "" {
			detail = stringValue(step["description"])
		}
		line := icon + " " + title
		if detail != "" {
			line += " — " + detail
		}
		lines = append(lines, line)
	}
	return terminalSafe(strings.Join(lines, "\n"))
}

func renderChartCard(component map[string]any) string {
	chartType := stringValue(component["type"])
	lines := []string{"chart · " + chartType}
	data, _ := component["data"].(map[string]any)
	labels, _ := data["labels"].([]any)
	datasets, _ := data["datasets"].([]any)
	for _, raw := range datasets {
		dataset, _ := raw.(map[string]any)
		values, _ := dataset["data"].([]any)
		lines = append(lines, stringValue(dataset["label"])+":")
		for i, value := range values {
			label := strconv.Itoa(i + 1)
			if i < len(labels) {
				label = fmt.Sprint(labels[i])
			}
			lines = append(lines, fmt.Sprintf("  %-16s %v", label, value))
		}
	}
	return terminalSafe(strings.Join(lines, "\n"))
}

func renderFileCard(c map[string]any) string {
	parts := []string{stringValue(c["path"])}
	if mime := stringValue(c["mime"]); mime != "" {
		parts = append(parts, mime)
	}
	if size, ok := c["size"].(float64); ok && size > 0 {
		parts = append(parts, fmt.Sprintf("%.0f bytes", size))
	}
	return terminalSafe(strings.Join(parts, " · "))
}

func renderImageCard(c map[string]any) string {
	target := stringValue(c["path"])
	if target == "" {
		target = stringValue(c["url"])
	}
	if alt := stringValue(c["alt"]); alt != "" {
		return terminalSafe(alt + "\n" + target)
	}
	return terminalSafe(target)
}

func renderFormPreview(c map[string]any) string {
	fields, _ := c["fields"].([]any)
	lines := make([]string, 0, len(fields))
	for _, raw := range fields {
		field, _ := raw.(map[string]any)
		label := stringValue(field["label"])
		if label == "" {
			label = stringValue(field["name"])
		}
		lines = append(lines, fmt.Sprintf("○ %s  <%s>", label, stringValue(field["type"])))
	}
	return terminalSafe(strings.Join(lines, "\n"))
}

func renderDecisionCard(c map[string]any) string {
	lines := []string{stringValue(c["prompt"])}
	if options, ok := c["options"].([]any); ok {
		for _, option := range options {
			lines = append(lines, "  ○ "+fmt.Sprint(option))
		}
	}
	return terminalSafe(strings.Join(lines, "\n"))
}

func renderIGVCard(c map[string]any) string {
	actions, _ := c["actions"].([]any)
	lines := []string{"IGV commands"}
	for _, raw := range actions {
		action, _ := raw.(map[string]any)
		lines = append(lines, fmt.Sprintf("  %s %s", stringValue(action["type"]), stringValue(action["locus"])))
	}
	return terminalSafe(strings.Join(lines, "\n"))
}

func prettyJSON(value any) string {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(b)
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

func cloneMap(in map[string]any) map[string]any {
	b, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func applyCardPatch(document map[string]any, patch []interface{}) (map[string]any, error) {
	copy := cloneMap(document)
	var root any = copy
	for i, raw := range patch {
		op, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("operation %d is not an object", i)
		}
		path := stringValue(op["path"])
		if path != "/title" && path != "/component" && !strings.HasPrefix(path, "/title/") && !strings.HasPrefix(path, "/component/") {
			return nil, fmt.Errorf("operation %d has unsupported path %q", i, path)
		}
		tokens, err := pointerTokens(path)
		if err != nil {
			return nil, err
		}
		root, err = patchValue(root, tokens, stringValue(op["op"]), op["value"])
		if err != nil {
			return nil, fmt.Errorf("operation %d: %w", i, err)
		}
	}
	out, ok := root.(map[string]any)
	if !ok {
		return nil, errors.New("patch replaced card document")
	}
	return out, nil
}

func pointerTokens(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil, errors.New("invalid JSON pointer")
	}
	parts := strings.Split(path[1:], "/")
	for i, part := range parts {
		if strings.Contains(part, "~") {
			for j := 0; j < len(part); j++ {
				if part[j] == '~' && (j+1 >= len(part) || (part[j+1] != '0' && part[j+1] != '1')) {
					return nil, errors.New("invalid JSON pointer escape")
				}
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func patchValue(node any, tokens []string, operation string, value any) (any, error) {
	if len(tokens) == 0 {
		if operation == "test" {
			if !reflect.DeepEqual(node, value) {
				return node, errors.New("test failed")
			}
			return node, nil
		}
		if operation == "remove" {
			return nil, nil
		}
		return value, nil
	}
	key := tokens[0]
	last := len(tokens) == 1
	switch container := node.(type) {
	case map[string]any:
		current, exists := container[key]
		if last {
			switch operation {
			case "add":
				container[key] = value
			case "replace":
				if !exists {
					return node, errors.New("replace target missing")
				}
				container[key] = value
			case "remove":
				if !exists {
					return node, errors.New("remove target missing")
				}
				delete(container, key)
			case "test":
				if !exists || !reflect.DeepEqual(current, value) {
					return node, errors.New("test failed")
				}
			default:
				return node, fmt.Errorf("unsupported op %q", operation)
			}
			return node, nil
		}
		if !exists {
			return node, errors.New("parent target missing")
		}
		next, err := patchValue(current, tokens[1:], operation, value)
		if err == nil {
			container[key] = next
		}
		return node, err
	case []any:
		index := len(container)
		if key != "-" {
			var err error
			index, err = strconv.Atoi(key)
			if err != nil || index < 0 {
				return node, errors.New("invalid array index")
			}
		}
		if last {
			switch operation {
			case "add":
				if index > len(container) {
					return node, errors.New("array index out of range")
				}
				container = append(container, nil)
				copy(container[index+1:], container[index:])
				container[index] = value
			case "replace", "test", "remove":
				if index >= len(container) {
					return node, errors.New("array index out of range")
				}
				if operation == "test" {
					if !reflect.DeepEqual(container[index], value) {
						return node, errors.New("test failed")
					}
				} else if operation == "remove" {
					container = append(container[:index], container[index+1:]...)
				} else {
					container[index] = value
				}
			default:
				return node, fmt.Errorf("unsupported op %q", operation)
			}
			return container, nil
		}
		if index >= len(container) {
			return node, errors.New("array index out of range")
		}
		next, err := patchValue(container[index], tokens[1:], operation, value)
		if err == nil {
			container[index] = next
		}
		return container, err
	default:
		return node, errors.New("target parent is scalar")
	}
}

func (m *model) safePath(name string) (string, error) {
	return safePathWithin(m.workdir, name)
}

func safePathWithin(workdir, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("card path is empty")
	}
	root, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		return "", err
	}
	target := name
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.EvalSymlinks(filepath.Clean(target))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("card path escapes the current workdir")
	}
	return target, nil
}

func (m *model) prepareCardSource(card *cardState) tea.Cmd {
	card.sourceVersion++
	version := card.sourceVersion
	card.sourceLoading = false
	card.sourceLoaded = false
	card.sourceRows = nil
	card.sourceDiff = ""
	card.sourceErr = ""
	component, _ := card.document["component"].(map[string]any)
	source, ok := component["source"].(map[string]any)
	if !ok {
		return nil
	}
	if (card.kind == "table" || card.kind == "spreadsheet") && lenAnySlice(component["rows"]) > 0 {
		return nil
	}
	if card.kind == "diff" && stringValue(component["unified_diff"]) != "" {
		return nil
	}
	if card.kind != "table" && card.kind != "spreadsheet" && card.kind != "diff" {
		return nil
	}
	card.sourceLoading = true
	workdir, cardID, kind := m.workdir, card.id, card.kind
	source = cloneMap(source)
	load := func() cardSourceLoadedMsg {
		if kind == "diff" {
			path, err := safePathWithin(workdir, stringValue(source["path"]))
			if err != nil {
				return cardSourceLoadedMsg{cardID: cardID, version: version, err: err}
			}
			data, truncated, err := readPreview(path)
			if err != nil {
				return cardSourceLoadedMsg{cardID: cardID, version: version, err: err}
			}
			diff := string(data)
			if truncated {
				diff += "\n… preview truncated"
			}
			return cardSourceLoadedMsg{cardID: cardID, version: version, diff: diff}
		}
		rows, err := loadGridSourceAt(workdir, source)
		return cardSourceLoadedMsg{cardID: cardID, version: version, rows: rows, err: err}
	}
	if m.replaying {
		m.applyCardSourceLoaded(load())
		return nil
	}
	return func() tea.Msg { return load() }
}

func (m *model) applyCardSourceLoaded(msg cardSourceLoadedMsg) {
	card := m.cards[msg.cardID]
	if card == nil || card.sourceVersion != msg.version {
		return
	}
	card.sourceLoading = false
	card.sourceLoaded = msg.err == nil
	card.sourceRows = msg.rows
	card.sourceDiff = msg.diff
	if msg.err != nil {
		card.sourceErr = msg.err.Error()
	}
	m.invalidateCard(card)
}

func lenAnySlice(value any) int {
	items, _ := value.([]any)
	return len(items)
}

func readPreview(path string) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, cardPreviewLimit+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > cardPreviewLimit {
		return data[:cardPreviewLimit], true, nil
	}
	return data, false, nil
}

type openConfirmation struct {
	cardID, target string
	remote         bool
}

func (c *openConfirmation) View(width int) string {
	return menuStyle.Width(max(width-4, 20)).Render("Open this artifact with the system application?\n" +
		truncateWidth(terminalSafe(c.target), max(width-8, 16)) + "\n" + accentStyle.Render("y") + " open · " + mutedStyle.Render("n/esc cancel"))
}

func (m *model) handleOpenCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.appendBlock(chatBlock{kind: blockError, content: "usage: /open <card-id>"})
		return m.dispatchIfIdle()
	}
	var card *cardState
	for id, candidate := range m.cards {
		if id == args[0] || strings.HasPrefix(id, args[0]) {
			if card != nil {
				m.appendBlock(chatBlock{kind: blockError, content: "ambiguous card id prefix: " + args[0]})
				return m, nil
			}
			card = candidate
		}
	}
	if card == nil {
		m.appendBlock(chatBlock{kind: blockError, content: "card not found: " + args[0]})
		return m, nil
	}
	target, err := m.cardOpenTarget(card)
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: err.Error()})
		return m, nil
	}
	u, _ := url.Parse(target)
	m.confirm = &openConfirmation{cardID: card.id, target: target, remote: u.Scheme == "http" || u.Scheme == "https"}
	m.layout()
	return m, nil
}

func (m *model) cardOpenTarget(card *cardState) (string, error) {
	component, _ := card.document["component"].(map[string]any)
	var target string
	switch card.kind {
	case "file", "image", "html-preview":
		target = stringValue(component["path"])
		if target == "" && card.kind == "image" {
			target = stringValue(component["url"])
		}
	case "table", "spreadsheet", "diff":
		if source, ok := component["source"].(map[string]any); ok {
			target = stringValue(source["path"])
		}
	}
	if target == "" {
		return "", errors.New("card has no openable artifact")
	}
	if u, err := url.Parse(target); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return target, nil
	}
	return m.safePath(target)
}

type openFinishedMsg struct{ err error }

func (m *model) updateOpenConfirmation(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(key.String()) {
	case "y":
		target := m.confirm.target
		m.confirm = nil
		return m, openTargetCmd(target)
	case "n", "esc":
		m.confirm = nil
		m.layout()
	}
	return m, nil
}

func openTargetCmd(target string) tea.Cmd {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	return tea.ExecProcess(command, func(err error) tea.Msg { return openFinishedMsg{err: err} })
}
