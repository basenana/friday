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
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	variant                string
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
	f := &formState{id: id, title: schema.Title, description: schema.Description, variant: schema.Variant, editor: editor}
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

func (f *formState) isQuestionForm() bool {
	// plan_questions was emitted by an earlier implementation. Keep accepting
	// it so an in-flight or restored form still gets the focused question UI.
	return f.variant == "questions" || f.variant == "plan_questions"
}

func (f *formState) questionFieldIndexes() []int {
	indexes := make([]int, 0, len(f.fields))
	for i := range f.fields {
		if i > 0 && strings.HasSuffix(f.fields[i].schema.Name, "_other") &&
			strings.TrimSuffix(f.fields[i].schema.Name, "_other") == f.fields[i-1].schema.Name {
			continue
		}
		indexes = append(indexes, i)
	}
	return indexes
}

func (f *formState) questionOtherIndex(index int) int {
	other := index + 1
	if index >= 0 && index < len(f.fields) && other < len(f.fields) &&
		f.fields[other].schema.Name == f.fields[index].schema.Name+"_other" {
		return other
	}
	return -1
}

func (f *formState) commitEditor() {
	if f.isQuestionForm() {
		if index := f.questionEditorIndex(); index >= 0 {
			f.fields[index].text = f.editor.Value()
		}
		return
	}
	if field := f.current(); field != nil && isTextField(field.schema.Type) {
		field.text = f.editor.Value()
	}
}

func (f *formState) loadEditor() {
	index := f.active
	if f.isQuestionForm() {
		index = f.questionEditorIndex()
	}
	if index < 0 || index >= len(f.fields) {
		f.editor.SetValue("")
		f.editor.Placeholder = ""
		f.editor.SetHeight(1)
		return
	}
	field := &f.fields[index]
	f.editor.Placeholder = terminalSafe(field.schema.Placeholder)
	if f.isQuestionForm() && f.editor.Placeholder == "" {
		f.editor.Placeholder = "Type your answer…"
	}
	f.editor.SetValue(terminalSafe(field.text))
	f.editor.CursorEnd()
	if field.schema.Type == cards.FieldTextarea || field.schema.Type == cards.FieldList || field.schema.Type == cards.FieldObject {
		f.editor.SetHeight(4)
	} else {
		f.editor.SetHeight(1)
	}
}

func (f *formState) questionEditorIndex() int {
	field := f.current()
	if field == nil {
		return -1
	}
	if isTextField(field.schema.Type) {
		return f.active
	}
	if field.schema.Type == cards.FieldSelect && len(field.schema.Options) > field.option &&
		strings.EqualFold(fmt.Sprint(field.schema.Options[field.option].Value), "Other") {
		return f.questionOtherIndex(f.active)
	}
	return -1
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
	visible := f.visibleFieldIndexes()
	if len(visible) == 0 {
		return
	}
	f.commitEditor()
	position := 0
	for i, index := range visible {
		if index == f.active {
			position = i
			break
		}
	}
	position = (position + delta + len(visible)) % len(visible)
	f.active = visible[position]
	f.loadEditor()
	f.err = ""
}

func (f *formState) moveQuestion(delta int) {
	indexes := f.questionFieldIndexes()
	if len(indexes) == 0 {
		return
	}
	f.commitEditor()
	position := 0
	for i, index := range indexes {
		if index == f.active || index+1 == f.active && f.questionOtherIndex(index) == f.active {
			position = i
			break
		}
	}
	position = max(0, min(position+delta, len(indexes)-1))
	f.active = indexes[position]
	f.loadEditor()
	f.err = ""
}

// visibleFieldIndexes implements the request_user_input convention where a
// select named "foo" may be followed by "foo_other". Generic forms still use
// this list view; question forms have a dedicated one-question-at-a-time view.
func (f *formState) visibleFieldIndexes() []int {
	visible := make([]int, 0, len(f.fields))
	for i := range f.fields {
		if f.isQuestionForm() && strings.HasSuffix(f.fields[i].schema.Name, "_other") {
			base := strings.TrimSuffix(f.fields[i].schema.Name, "_other")
			if i > 0 && f.fields[i-1].schema.Name == base && f.fields[i-1].schema.Type == cards.FieldSelect {
				selectField := &f.fields[i-1]
				if len(selectField.schema.Options) <= selectField.option || !strings.EqualFold(fmt.Sprint(selectField.schema.Options[selectField.option].Value), "Other") {
					continue
				}
			}
		}
		visible = append(visible, i)
	}
	return visible
}

func (f *formState) conditionalOtherVisible(index int) bool {
	if !f.isQuestionForm() || index <= 0 || index >= len(f.fields) || !strings.HasSuffix(f.fields[index].schema.Name, "_other") {
		return false
	}
	base := strings.TrimSuffix(f.fields[index].schema.Name, "_other")
	selectField := &f.fields[index-1]
	return selectField.schema.Name == base && selectField.schema.Type == cards.FieldSelect &&
		len(selectField.schema.Options) > selectField.option && strings.EqualFold(fmt.Sprint(selectField.schema.Options[selectField.option].Value), "Other")
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
	}
	if f.isQuestionForm() {
		return m.updateQuestionForm(key)
	}
	switch key.String() {
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

func (m *model) updateQuestionForm(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.form
	field := f.current()
	if field == nil {
		return m, nil
	}
	switch key.Code {
	case tea.KeyLeft:
		f.moveQuestion(-1)
		return m, nil
	case tea.KeyRight:
		f.moveQuestion(1)
		return m, nil
	case tea.KeyUp, tea.KeyDown:
		if field.schema.Type == cards.FieldSelect && len(field.schema.Options) > 0 {
			f.commitEditor()
			delta := 1
			if key.Code == tea.KeyUp {
				delta = -1
			}
			field.option = (field.option + delta + len(field.schema.Options)) % len(field.schema.Options)
			f.loadEditor()
			f.dirty = true
			f.err = ""
		}
		return m, nil
	case tea.KeyEnter:
		f.dirty = true
		f.commitEditor()
		if err := m.validateCurrentQuestion(); err != nil {
			f.err = err.Error()
			return m, nil
		}
		indexes := f.questionFieldIndexes()
		if len(indexes) > 0 && f.active == indexes[len(indexes)-1] {
			return m.submitForm()
		}
		f.moveQuestion(1)
		return m, nil
	case tea.KeyTab:
		f.moveQuestion(1)
		return m, nil
	}
	if key.String() == "shift+tab" {
		f.moveQuestion(-1)
		return m, nil
	}
	if f.questionEditorIndex() < 0 {
		return m, nil
	}
	if key.String() == "ctrl+j" {
		key = tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	var cmd tea.Cmd
	f.editor, cmd = f.editor.Update(key)
	f.dirty = true
	f.err = ""
	return m, cmd
}

func (m *model) validateCurrentQuestion() error {
	f := m.form
	field := f.current()
	if field == nil {
		return nil
	}
	if field.schema.Type == cards.FieldSelect && len(field.schema.Options) > field.option &&
		strings.EqualFold(fmt.Sprint(field.schema.Options[field.option].Value), "Other") {
		other := f.questionOtherIndex(f.active)
		if other < 0 || strings.TrimSpace(f.fields[other].text) == "" {
			return fmt.Errorf("Please write your answer")
		}
	}
	_, err := m.formValue(field)
	return err
}

func (m *model) cancelForm() (tea.Model, tea.Cmd) {
	id := m.form.id
	if err := m.registry.DispatchInput(bus.NewFormCancel(m.sessionID, "user.local", bus.FormCancelInput{FormID: id})); err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "cancel form: " + err.Error()})
	}
	m.form = nil
	m.layout()
	return m, nil
}

func (m *model) submitForm() (tea.Model, tea.Cmd) {
	f := m.form
	f.commitEditor()
	values := make(map[string]any, len(f.fields))
	for i := range f.fields {
		if f.conditionalOtherVisible(i) && strings.TrimSpace(f.fields[i].text) == "" {
			f.active = i
			if f.isQuestionForm() {
				f.active = i - 1
			}
			f.loadEditor()
			f.err = "Please write your answer"
			return m, nil
		}
		value, err := m.formValue(&f.fields[i])
		if err != nil {
			f.active = i
			if f.isQuestionForm() && strings.HasSuffix(f.fields[i].schema.Name, "_other") {
				f.active = i - 1
			}
			f.loadEditor()
			f.err = err.Error()
			return m, nil
		}
		values[f.fields[i].schema.Name] = value
	}
	if err := m.registry.DispatchInput(bus.NewFormSubmit(m.sessionID, "user.local", bus.FormSubmitInput{FormID: f.id, Values: values})); err != nil {
		f.err = "Form is no longer active: " + err.Error()
		return m, nil
	}
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
	if f.isQuestionForm() {
		return lipgloss.Height(f.viewQuestions(width))
	}
	height := min(len(f.visibleFieldIndexes()), 8) + 5
	if field := f.current(); field != nil && isTextField(field.schema.Type) {
		height += f.editor.Height()
	}
	if field := f.current(); field != nil && field.schema.Help != "" {
		height++
	}
	if field := f.current(); field != nil && field.schema.Type == cards.FieldSelect && len(field.schema.Options) > field.option && field.schema.Options[field.option].Description != "" {
		height++
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
	if f.isQuestionForm() {
		return f.viewQuestions(width)
	}
	title := f.title
	if title == "" {
		title = "Input required"
	}
	lines := []string{accentStyle.Copy().Bold(true).Render("? " + terminalSafe(title))}
	if f.description != "" {
		lines = append(lines, mutedStyle.Render(terminalSafe(f.description)))
	}
	visible := f.visibleFieldIndexes()
	activePosition := 0
	for i, index := range visible {
		if index == f.active {
			activePosition = i
			break
		}
	}
	start := max(activePosition-7, 0)
	end := min(start+8, len(visible))
	if start > 0 {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("↑ %d earlier fields", start)))
	}
	for position := start; position < end; position++ {
		i := visible[position]
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
			lines = append(lines, interactiveStyle(true).Render(line), f.editor.View())
		} else {
			lines = append(lines, interactiveStyle(i == f.active).Render(line+"  "+value))
		}
		if i == f.active && field.schema.Help != "" {
			lines = append(lines, mutedStyle.Render(terminalSafe(field.schema.Help)))
		}
		if i == f.active && field.schema.Type == cards.FieldSelect && len(field.schema.Options) > field.option {
			if description := field.schema.Options[field.option].Description; description != "" {
				lines = append(lines, mutedStyle.Render(terminalSafe(description)))
			}
		}
	}
	if end < len(visible) {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("↓ %d more fields", len(visible)-end)))
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

func (f *formState) viewQuestions(width int) string {
	indexes := f.questionFieldIndexes()
	if len(indexes) == 0 {
		return ""
	}
	position := 0
	for i, index := range indexes {
		if index == f.active {
			position = i
			break
		}
	}
	field := &f.fields[indexes[position]]
	contentWidth := max(width-8, 16)
	lines := []string{accentStyle.Copy().Bold(true).Render(fmt.Sprintf("? Question %d/%d", position+1, len(indexes)))}
	question := field.schema.Help
	if question == "" {
		question = field.schema.Label
	}
	if question == "" {
		question = field.schema.Name
	}
	lines = append(lines, accentStyle.Copy().Bold(true).Render(ansi.Wrap(terminalSafe(question), contentWidth, "")), "")

	switch field.schema.Type {
	case cards.FieldSelect:
		for i, option := range field.schema.Options {
			label := option.Label
			if strings.EqualFold(fmt.Sprint(option.Value), "Other") {
				label = "Write your own answer"
			}
			if option.Description != "" && !strings.EqualFold(fmt.Sprint(option.Value), "Other") {
				label += " — " + option.Description
			}
			marker := "  ○ "
			style := interactiveStyle(false)
			if i == field.option {
				marker = "› ● "
				style = interactiveStyle(true)
			}
			wrapped := strings.Split(ansi.Wrap(terminalSafe(label), max(contentWidth-lipgloss.Width(marker), 8), ""), "\n")
			for lineIndex, line := range wrapped {
				prefix := strings.Repeat(" ", lipgloss.Width(marker))
				if lineIndex == 0 {
					prefix = marker
				}
				lines = append(lines, style.Render(prefix+line))
			}
			if i == field.option && strings.EqualFold(fmt.Sprint(option.Value), "Other") && f.questionOtherIndex(f.active) >= 0 {
				lines = append(lines, f.editor.View())
			}
		}
	default:
		lines = append(lines, f.editor.View())
	}

	if f.err != "" {
		lines = append(lines, errorStyle.Render(terminalSafe(f.err)))
	}
	if f.confirmCancel {
		lines = append(lines, errorStyle.Render("Discard answers? y/n"))
	} else if f.submitting {
		lines = append(lines, mutedStyle.Render("Submitting…"))
	} else {
		action := "Enter next"
		if position == len(indexes)-1 {
			action = "Enter submit"
		}
		lines = append(lines, mutedStyle.Render("↑/↓ choose · "+action+" · ←/→ question · Esc cancel"))
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
