package sessions

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/utils"
	"github.com/google/uuid"
)

type Notebook interface {
	ListNotes(ctx context.Context) ([]*Note, error)
	GetNote(ctx context.Context, id string) (*Note, error)
	SaveOrUpdate(ctx context.Context, note *Note) (*Note, error)
}

type Note struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Content  string `json:"content,omitempty"`
	Filtered string `json:"filtered,omitempty"`
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

func NotebookReadTools(nb Notebook) []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool("list_files",
			tools.WithDescription("Your past work content has been saved in the current working directory. This tool can list all files that have been saved."),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				notes, err := nb.ListNotes(ctx)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				for i := range notes {
					notes[i].Content = "" // hide content to save tokens
				}

				return tools.NewToolResultText(utils.Res2Str(notes)), nil
			}),
		),
		tools.NewTool("read_file",
			tools.WithDescription("Use this tool to read or grep files in the current working directory. It's recommended to filter queries using combined keywords to reduce the burden on the context."),
			tools.WithString("filename",
				tools.Required(),
				tools.Description("The name of note file. If you don't know the id, you need to use `list_files` to find it."),
			),
			tools.WithArray("filter_keywords",
				tools.Items(map[string]interface{}{"type": "string", "description": "The keyword that need to be filtered should be used; only rows that match the keywords will be returned."}),
				tools.Description("Quickly search for the content you need using keywords. If no keywords are provided, the full text will be returned. Keywords are related by \"or\"."),
			),
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				nid, ok := request.Arguments["filename"].(string)
				if !ok || nid == "" {
					return nil, fmt.Errorf("missing required parameter: filename")
				}
				note, err := nb.GetNote(ctx, nid)
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				filters, ok := request.Arguments["filter_keywords"].([]any)
				if ok && len(filters) > 0 {
					keywords := make(map[string]struct{})
					for _, f := range filters {
						keyword, ok := f.(string)
						if ok {
							keywords[keyword] = struct{}{}
						}
					}

					buf := &bytes.Buffer{}
					noteLines := strings.Split(note.Content, "\n")
					for _, line := range noteLines {
						for keyword := range keywords {
							if strings.Contains(line, keyword) {
								buf.WriteString(line)
								buf.WriteString("\n")
							}
						}
					}

					note.Content = ""
					note.Filtered = buf.String()
					if note.Filtered == "" {
						note.Filtered = "no filtered content"
					}
				}

				return tools.NewToolResultText(utils.Res2Str(note)), nil
			}),
		),
	}
}
