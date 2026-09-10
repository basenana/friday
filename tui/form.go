package tui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/cards"
)

type formFieldState struct {
	schema  cards.Field
	text    string
	option  int
	multi   map[int]bool
	boolean bool
}

type formState struct {
	id, title, description string
	fields                 []formFieldState
	active                 int
	editor                 textarea.Model
	err                    string
	dirty                  bool
	confirmCancel          bool
	submitting             bool
}

func newFormState(id string, raw map[string]any, width int) (*formState, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var schema cards.FormSchema
	if err := json.Unmarshal(b, &schema); err != nil {
		return nil, err
	}
	if err := cards.Default.ValidateForm(schema); err != nil {
		return nil, err
	}
	editor := textarea.New()
	editor.ShowLineNumbers = false
	editor.Prompt = "  "
	editor.CharLimit = 0
	editor.SetWidth(max(width-10, 20))
	editor.SetHeight(1)
	removeTextareaBackground(&editor)
	f := &formState{id: id, title: schema.Title, description: schema.Description, editor: editor}
	f.editor.Focus()
	for _, field := range schema.Fields {
		state := formFieldState{schema: field, multi: make(map[int]bool)}
		state.setDefault(field.Default)
		f.fields = append(f.fields, state)
	}
	if len(f.fields) > 0 {
		f.loadEditor()
	}
	return f, nil
}

func (f *formFieldState) setDefault(value any) {
	if value == nil {
		return
	}
	switch f.schema.Type {
	case cards.FieldBoolean:
		f.boolean, _ = value.(bool)
	case cards.FieldSelect:
		for i, option := range f.schema.Options {
			if reflect.DeepEqual(option.Value, value) {
				f.option = i
				break
			}
		}
	case cards.FieldMultiSelect:
		values, _ := value.([]any)
		for i, option := range f.schema.Options {
			for _, selected := range values {
				if reflect.DeepEqual(option.Value, selected) {
					f.multi[i] = true
				}
			}
		}
	case cards.FieldList, cards.FieldObject:
		b, _ := json.MarshalIndent(value, "", "  ")
		f.text = string(b)
	default:
		f.text = terminalSafe(fmt.Sprint(value))
	}
}

func (f *formState) current() *formFieldState {
	if f.active < 0 || f.active >= len(f.fields) {
		return nil
	}
	return &f.fields[f.active]
}

func (f *formState) commitEditor() {
	if field := f.current(); field != nil && isTextField(field.schema.Type) {
		field.text = f.editor.Value()
	}
}

func (f *formState) loadEditor() {
	field := f.current()
	if field == nil {
		return
	}
	f.editor.SetValue(terminalSafe(field.text))
	f.editor.CursorEnd()
	if field.schema.Type == cards.FieldTextarea || field.schema.Type == cards.FieldList || field.schema.Type == cards.FieldObject {
		f.editor.SetHeight(4)
	} else {
		f.editor.SetHeight(1)
	}
}

func isTextField(kind cards.FieldType) bool {
	switch kind {
	case cards.FieldText, cards.FieldTextarea, cards.FieldNumber, cards.FieldDate,
		cards.FieldFile, cards.FieldList, cards.FieldObject:
		return true
	default:
		return false
	}
}

func (f *formState) move(delta int) {
	if len(f.fields) == 0 {
		return
	}
	f.commitEditor()
	f.active = (f.active + delta + len(f.fields)) % len(f.fields)
	f.loadEditor()
	f.err = ""
}

func (m *model) updateForm(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.form
	if f == nil {
		return m, nil
	}
	if f.confirmCancel {
		switch strings.ToLower(key.String()) {
		case "y":
			return m.cancelForm()
		case "n", "esc":
			f.confirmCancel = false
		}
		return m, nil
	}
	if f.submitting {
		return m, nil
	}
	switch key.String() {
	case "esc":
		if f.dirty {
			f.confirmCancel = true
			return m, nil
		}
		return m.cancelForm()
	case "ctrl+s":
		return m.submitForm()
	case "tab":
		f.move(1)
		return m, nil
	case "shift+tab":
		f.move(-1)
		return m, nil
	}
	field := f.current()
	if field == nil {
		return m, nil
	}
	switch field.schema.Type {
	case cards.FieldBoolean:
		if key.Code == tea.KeySpace || key.Code == tea.KeyEnter || key.Code == tea.KeyLeft || key.Code == tea.KeyRight {
			field.boolean = !field.boolean
			f.dirty = true
		}
		return m, nil
	case cards.FieldSelect:
		if len(field.schema.Options) > 0 && (key.Code == tea.KeyLeft || key.Code == tea.KeyRight || key.Code == tea.KeySpace) {
			delta := 1
			if key.Code == tea.KeyLeft {
				delta = -1
			}
			field.option = (field.option + delta + len(field.schema.Options)) % len(field.schema.Options)
			f.dirty = true
		}
		if key.Code == tea.KeyEnter {
			f.move(1)
		}
		return m, nil
	case cards.FieldMultiSelect:
		if len(field.schema.Options) > 0 && (key.Code == tea.KeyLeft || key.Code == tea.KeyRight) {
			delta := 1
			if key.Code == tea.KeyLeft {
				delta = -1
			}
			field.option = (field.option + delta + len(field.schema.Options)) % len(field.schema.Options)
		} else if key.Code == tea.KeySpace || key.Code == tea.KeyEnter {
			field.multi[field.option] = !field.multi[field.option]
			f.dirty = true
		}
		return m, nil
	default:
		if key.String() == "ctrl+j" {
			key = tea.KeyPressMsg{Code: tea.KeyEnter}
		}
		if key.Code == tea.KeyEnter && field.schema.Type != cards.FieldTextarea && field.schema.Type != cards.FieldList && field.schema.Type != cards.FieldObject {
			f.move(1)
			return m, nil
		}
		var cmd tea.Cmd
		f.editor, cmd = f.editor.Update(key)
		f.dirty = true
		return m, cmd
	}
}

func (m *model) cancelForm() (tea.Model, tea.Cmd) {
	id := m.form.id
	m.registry.Bus().Publish(bus.TopicInbox(m.sessionID), bus.NewFormCancel(m.sessionID, "user.local", bus.FormCancelInput{FormID: id}))
	m.form = nil
	m.layout()
	return m, nil
}

func (m *model) submitForm() (tea.Model, tea.Cmd) {
	f := m.form
	f.commitEditor()
	values := make(map[string]any, len(f.fields))
	for i := range f.fields {
		value, err := m.formValue(&f.fields[i])
		if err != nil {
			f.active = i
			f.loadEditor()
			f.err = err.Error()
			return m, nil
		}
		values[f.fields[i].schema.Name] = value
	}
	m.registry.Bus().Publish(bus.TopicInbox(m.sessionID), bus.NewFormSubmit(m.sessionID, "user.local", bus.FormSubmitInput{FormID: f.id, Values: values}))
	f.submitting = true
	f.err = ""
	return m, nil
}

func (m *model) formValue(field *formFieldState) (any, error) {
	label := field.schema.Label
	if label == "" {
		label = field.schema.Name
	}
	required := func(empty bool) error {
		if field.schema.Required && empty {
			return fmt.Errorf("%s is required", label)
		}
		return nil
	}
	switch field.schema.Type {
	case cards.FieldText, cards.FieldTextarea:
		value := strings.TrimSpace(field.text)
		return field.text, required(value == "")
	case cards.FieldNumber:
		value := strings.TrimSpace(field.text)
		if err := required(value == ""); err != nil {
			return nil, err
		}
		if value == "" {
			return nil, nil
		}
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a number", label)
		}
		if field.schema.Min != nil && n < *field.schema.Min {
			return nil, fmt.Errorf("%s must be at least %v", label, *field.schema.Min)
		}
		if field.schema.Max != nil && n > *field.schema.Max {
			return nil, fmt.Errorf("%s must be at most %v", label, *field.schema.Max)
		}
		return n, nil
	case cards.FieldBoolean:
		return field.boolean, nil
	case cards.FieldSelect:
		if len(field.schema.Options) == 0 {
			return nil, fmt.Errorf("%s has no options", label)
		}
		return field.schema.Options[field.option].Value, nil
	case cards.FieldMultiSelect:
		var values []any
		for i, option := range field.schema.Options {
			if field.multi[i] {
				values = append(values, option.Value)
			}
		}
		return values, required(len(values) == 0)
	case cards.FieldDate:
		value := strings.TrimSpace(field.text)
		if err := required(value == ""); err != nil {
			return nil, err
		}
		if value != "" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return nil, fmt.Errorf("%s must use YYYY-MM-DD", label)
			}
		}
		return value, nil
	case cards.FieldFile:
		value := strings.TrimSpace(field.text)
		if err := required(value == ""); err != nil {
			return nil, err
		}
		if value != "" {
			if _, err := m.safePath(value); err != nil {
				return nil, fmt.Errorf("%s: %v", label, err)
			}
		}
		return value, nil
	case cards.FieldList, cards.FieldObject:
		value := strings.TrimSpace(field.text)
		if err := required(value == ""); err != nil {
			return nil, err
		}
		if value == "" {
			return nil, nil
		}
		var decoded any
		if err := json.Unmarshal([]byte(value), &decoded); err != nil {
			return nil, fmt.Errorf("%s contains invalid JSON", label)
		}
		if field.schema.Type == cards.FieldList {
			items, ok := decoded.([]any)
			if !ok {
				return nil, fmt.Errorf("%s must be a JSON array", label)
			}
			if len(field.schema.Fields) > 0 {
				for i, item := range items {
					object, ok := item.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("%s[%d] must be a JSON object", label, i)
					}
					if err := m.validateNestedObject(field.schema.Fields, object, fmt.Sprintf("%s[%d]", label, i)); err != nil {
						return nil, err
					}
				}
			}
		}
		if field.schema.Type == cards.FieldObject {
			object, ok := decoded.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s must be a JSON object", label)
			}
			if err := m.validateNestedObject(field.schema.Fields, object, label); err != nil {
				return nil, err
			}
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported field type %s", field.schema.Type)
	}
}

func (m *model) validateNestedObject(fields []cards.Field, object map[string]any, path string) error {
	for _, schema := range fields {
		value, exists := object[schema.Name]
		fieldPath := path + "." + schema.Name
		if !exists || value == nil {
			if schema.Required {
				return fmt.Errorf("%s is required", fieldPath)
			}
			continue
		}
		if schema.Required {
			empty := false
			switch typed := value.(type) {
			case string:
				empty = strings.TrimSpace(typed) == ""
			case []any:
				empty = len(typed) == 0
			}
			if empty {
				return fmt.Errorf("%s is required", fieldPath)
			}
		}
		if err := m.validateNestedValue(schema, value, fieldPath); err != nil {
			return err
		}
	}
	return nil
}

func (m *model) validateNestedValue(schema cards.Field, value any, path string) error {
	switch schema.Type {
	case cards.FieldText, cards.FieldTextarea:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be text", path)
		}
	case cards.FieldNumber:
		number, ok := value.(float64)
		if !ok {
			return fmt.Errorf("%s must be a number", path)
		}
		if schema.Min != nil && number < *schema.Min {
			return fmt.Errorf("%s must be at least %v", path, *schema.Min)
		}
		if schema.Max != nil && number > *schema.Max {
			return fmt.Errorf("%s must be at most %v", path, *schema.Max)
		}
	case cards.FieldBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	case cards.FieldSelect:
		for _, option := range schema.Options {
			if reflect.DeepEqual(option.Value, value) {
				return nil
			}
		}
		return fmt.Errorf("%s is not a valid option", path)
	case cards.FieldMultiSelect:
		values, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		for _, selected := range values {
			valid := false
			for _, option := range schema.Options {
				if reflect.DeepEqual(option.Value, selected) {
					valid = true
					break
				}
			}
			if !valid {
				return fmt.Errorf("%s contains an invalid option", path)
			}
		}
	case cards.FieldDate:
		date, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must use YYYY-MM-DD", path)
		}
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return fmt.Errorf("%s must use YYYY-MM-DD", path)
		}
	case cards.FieldFile:
		name, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must be a file path", path)
		}
		if _, err := m.safePath(name); err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
	case cards.FieldObject:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be a JSON object", path)
		}
		return m.validateNestedObject(schema.Fields, object, path)
	case cards.FieldList:
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be a JSON array", path)
		}
		for i, item := range items {
			if len(schema.Fields) == 0 {
				continue
			}
			object, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("%s[%d] must be a JSON object", path, i)
			}
			if err := m.validateNestedObject(schema.Fields, object, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *formState) Height(width int) int {
	height := min(len(f.fields), 8) + 5
	if field := f.current(); field != nil && isTextField(field.schema.Type) {
		height += f.editor.Height()
	}
	if f.description != "" {
		height++
	}
	if f.err != "" || f.confirmCancel {
		height++
	}
	return min(height, 18)
}

func (f *formState) View(width int) string {
	title := f.title
	if title == "" {
		title = "Input required"
	}
	lines := []string{accentStyle.Copy().Bold(true).Render("? " + terminalSafe(title))}
	if f.description != "" {
		lines = append(lines, mutedStyle.Render(terminalSafe(f.description)))
	}
	start := 0
	if f.active >= 8 {
		start = f.active - 7
	}
	end := min(start+8, len(f.fields))
	if start > 0 {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("↑ %d earlier fields", start)))
	}
	for i := start; i < end; i++ {
		field := &f.fields[i]
		label := field.schema.Label
		if label == "" {
			label = field.schema.Name
		}
		prefix := "  "
		if i == f.active {
			prefix = "› "
		}
		value := terminalSafe(field.displayValue())
		line := prefix + terminalSafe(label)
		if field.schema.Required {
			line += " *"
		}
		if i == f.active && isTextField(field.schema.Type) {
			lines = append(lines, accentStyle.Render(line), f.editor.View())
		} else {
			lines = append(lines, line+"  "+mutedStyle.Render(value))
		}
	}
	if end < len(f.fields) {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("↓ %d more fields", len(f.fields)-end)))
	}
	if f.err != "" {
		lines = append(lines, errorStyle.Render(terminalSafe(f.err)))
	}
	if f.confirmCancel {
		lines = append(lines, errorStyle.Render("Discard form changes? y/n"))
	} else if f.submitting {
		lines = append(lines, mutedStyle.Render("Submitting…"))
	} else {
		lines = append(lines, mutedStyle.Render("Tab next · Space/←/→ choose · Ctrl+S submit · Esc cancel"))
	}
	return menuStyle.Copy().BorderForeground(themeAccent).Width(max(width-4, 20)).Render(strings.Join(lines, "\n"))
}

func (f *formFieldState) displayValue() string {
	switch f.schema.Type {
	case cards.FieldBoolean:
		if f.boolean {
			return "[x] yes"
		}
		return "[ ] no"
	case cards.FieldSelect:
		if len(f.schema.Options) > f.option {
			return "‹ " + f.schema.Options[f.option].Label + " ›"
		}
	case cards.FieldMultiSelect:
		var selected []string
		for i, option := range f.schema.Options {
			if f.multi[i] {
				selected = append(selected, option.Label)
			}
		}
		cursor := ""
		if len(f.schema.Options) > f.option {
			cursor = f.schema.Options[f.option].Label
		}
		return "‹ " + cursor + " › selected: " + strings.Join(selected, ", ")
	}
	if f.text == "" {
		return f.schema.Placeholder
	}
	return firstLine(f.text)
}
