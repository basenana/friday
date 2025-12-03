package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/tools"
)

func ListStorageTools() []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool("list_persistence_files",
			tools.WithDescription("List all files that have been saved in the local execution environment."),
			tools.WithToolHandler(handleListPersistenceFileTools),
		),
		tools.NewTool("retrieve_from_file",
			tools.WithDescription("This tool reads locally saved files; it does NOT support any write operations or network requests."),
			tools.WithString("path",
				tools.Required(),
				tools.Description("The absolute path to the local file. If you don't know the absolute path, you need to use `list_persistence_files` to find it."),
			),
			tools.WithToolHandler(handleRetrieveFromFileTools),
		),
		tools.NewTool("write_content_to_file",
			tools.WithDescription("Save the data to the local file system for future access."),
			tools.WithString("path",
				tools.Required(),
				tools.Description("The absolute path to the file that needs to be saved must be provided. "+
					"The file needs to be retrieved multiple times, so it's important to keep the file path readable to avoid forgetting it."),
			),
			tools.WithString("content",
				tools.Required(),
				tools.Description("File content that needs to be saved to local storage"),
			),
			tools.WithToolHandler(handleWriteContentToFile),
		),
	}
}

func handleListPersistenceFileTools(ctx context.Context, request *tools.Request) (*tools.Result, error) {
	m := memoryFromContext(ctx)
	if m == nil {
		return nil, fmt.Errorf("agent memory is nil")
	}
	var files []string
	err := m.storage.List(ctx, func(record *Records) bool {
		files = append(files, record.Key)
		return false
	})

	if err != nil {
		return tools.NewToolResultError(err.Error()), nil
	}
	return tools.NewToolResultText(strings.Join(files, "\n")), nil
}

func handleRetrieveFromFileTools(ctx context.Context, request *tools.Request) (*tools.Result, error) {
	m := memoryFromContext(ctx)
	if m == nil {
		return nil, fmt.Errorf("agent memory is nil")
	}
	path, ok := request.Arguments["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("missing required parameter: path")
	}

	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("path must be absolute")
	}

	record, err := m.storage.Get(ctx, path)
	if err != nil {
		return tools.NewToolResultError(err.Error()), nil
	}

	raw, _ := json.Marshal(record)
	return tools.NewToolResultText(string(raw)), nil
}

func handleWriteContentToFile(ctx context.Context, request *tools.Request) (*tools.Result, error) {
	m := memoryFromContext(ctx)
	if m == nil {
		return nil, fmt.Errorf("agent memory is nil")
	}
	path, ok := request.Arguments["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("missing required parameter: path")
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("path must be absolute")
	}

	content, ok := request.Arguments["content"].(string)
	if !ok || content == "" {
		return nil, fmt.Errorf("missing required parameter: content")
	}

	err := m.storage.Replace(ctx, path, content)
	if err != nil {
		return tools.NewToolResultError(err.Error()), nil
	}
	return tools.NewToolResultText(fmt.Sprintf("Succeed. %s", remindMessage(path))), nil
}

func remindMessage(key string) string {
	return fmt.Sprintf("Retrieve from %s if needed", key)
}

func LLMRequest(systemMessage string, m *Memory) openai.Request {
	return openai.NewSimpleRequest(systemMessage, m.history...)
}

func WithMemory(ctx context.Context, m *Memory) context.Context {
	return context.WithValue(ctx, "agent.memory", m)
}

func memoryFromContext(ctx context.Context) *Memory {
	raw := ctx.Value("agent.memory")
	if raw == nil {
		return nil
	}
	m, ok := raw.(*Memory)
	if !ok {
		return nil
	}
	return m
}
