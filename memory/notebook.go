package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/utils"
	"github.com/google/uuid"
)

type Notebook interface {
	ListNotes(ctx context.Context) ([]*Note, error)
	GetNote(ctx context.Context, id string) (*Note, error)
	SaveOrUpdate(ctx context.Context, note *Note) (*Note, error)
	ReadTools() []*tools.Tool
	WriteTools() []*tools.Tool
}

type Note struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content,omitempty"`
}

type inMemoryNotebook struct {
	records map[string]*Note
	mutex   sync.RWMutex
}

func NewInMemoryNotebook() Notebook {
	return &inMemoryNotebook{
		records: make(map[string]*Note),
		mutex:   sync.RWMutex{},
	}
}

var _ Notebook = &inMemoryNotebook{}

func (m *inMemoryNotebook) ListNotes(ctx context.Context) ([]*Note, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	result := make([]*Note, 0, len(m.records))
	for _, note := range m.records {
		result = append(result, note)
	}
	return result, nil
}

func (m *inMemoryNotebook) GetNote(ctx context.Context, id string) (*Note, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	note, ok := m.records[id]
	if !ok {
		return nil, fmt.Errorf("note does not exist")
	}
	return note, nil
}

func (m *inMemoryNotebook) SaveOrUpdate(ctx context.Context, note *Note) (*Note, error) {
	if note.ID == "" {
		note.ID = uuid.New().String()
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.records[note.ID] = note
	return note, nil
}

func (m *inMemoryNotebook) ReadTools() []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool("list_all_notes",
			tools.WithDescription("List all notes that have been saved in notebook."),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				notes, err := m.ListNotes(ctx)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				for i := range notes {
					notes[i].Content = "" // hide content to save tokens
				}

				return tools.NewToolResultText(utils.Res2Str(notes)), nil
			}),
		),
		tools.NewTool("retrieve_from_notebook",
			tools.WithDescription("This tool reads saved note content."),
			tools.WithString("id",
				tools.Required(),
				tools.Description("The id of note. If you don't know the id, you need to use `list_all_notes` to find it."),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				nid, ok := request.Arguments["id"].(string)
				if !ok || nid == "" {
					return nil, fmt.Errorf("missing required parameter: id")
				}
				note, err := m.GetNote(ctx, nid)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				return tools.NewToolResultText(utils.Res2Str(note)), nil
			}),
		),
	}
}

func (m *inMemoryNotebook) WriteTools() []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool("create_note_to_notebook",
			tools.WithDescription("Save the data to the notebook for future access."),
			tools.WithString("title",
				tools.Required(),
				tools.Description("The title of the note, convenient for subsequent quick lookup, DO NOT exceed 10 words."),
			),
			tools.WithString("content",
				tools.Required(),
				tools.Description("Note content that needs to be saved"),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				title, ok := request.Arguments["title"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: title")
				}
				content, ok := request.Arguments["content"].(string)
				if !ok {
					return nil, fmt.Errorf("missing required parameter: content")
				}

				n := &Note{ID: "", Title: title, Content: content}
				n, err := m.SaveOrUpdate(ctx, n)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				return tools.NewToolResultText(fmt.Sprintf("note %s saved", n.ID)), nil
			}),
		),
	}
}
