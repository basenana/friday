package tools

import (
	"bytes"
	"context"
	"fmt"
	"github.com/basenana/friday/utils"
	"github.com/basenana/friday/vfs"
	"strings"
)

func ReadTools(fs vfs.VirtualFileSystem) []*Tool {
	return []*Tool{
		NewTool("list_all_files",
			WithDescription("Your previous work will be saved in the work dir. This tool can list all files that have been saved."),
			WithToolHandler(func(ctx context.Context, request *Request) (*Result, error) {
				files, err := fs.ListFiles(ctx)
				if err != nil {
					return NewToolResultError(err.Error()), nil
				}

				for i := range files {
					files[i].Content = "" // hide content to save tokens
				}

				return NewToolResultText(utils.Res2Str(files)), nil
			}),
		),
		NewTool("read_file",
			WithDescription("Use this tool to recall or grep file. It's recommended to filter queries using combined keywords to reduce the burden on the context."),
			WithString("filename",
				Required(),
				Description("The name of file. If you don't know the name, you need to use `list_all_files` to find it."),
			),
			WithArray("filter_keywords",
				Items(map[string]interface{}{"type": "string", "description": "The keyword that need to be filtered should be used; only rows that match the keywords will be returned."}),
				Description("Quickly search for the content you need using keywords. If no keywords are provided, the full text will be returned. Keywords are related by \"or\"."),
			),
			WithToolHandler(func(ctx context.Context, request *Request) (*Result, error) {
				filename, ok := request.Arguments["filename"].(string)
				if !ok || filename == "" {
					return nil, fmt.Errorf("missing required parameter: filename")
				}
				f, err := fs.ReadFile(ctx, filename)
				if err != nil {
					return NewToolResultError(err.Error()), nil
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
					contentLines := strings.Split(f.Content, "\n")
					for _, line := range contentLines {
						for keyword := range keywords {
							if strings.Contains(line, keyword) {
								buf.WriteString(line)
								buf.WriteString("\n")
							}
						}
					}

					f.Content = ""
					f.Filtered = buf.String()
					if f.Filtered == "" {
						f.Filtered = "no filtered content"
					}
				}

				return NewToolResultText(utils.Res2Str(f)), nil
			}),
		),
	}
}

func WriteTools(fs vfs.VirtualFileSystem) []*Tool {
	return []*Tool{
		NewTool("write_file",
			WithDescription("Save the data to workdir for future access."),
			WithString("filename",
				Required(),
				Description("The name of the file"),
			),
			WithString("abstract",
				Required(),
				Description("The abstract of the file, convenient for subsequent quick lookup, DO NOT exceed 10 words."),
			),
			WithString("content",
				Required(),
				Description("File content that needs to be saved"),
			),
			WithToolHandler(func(ctx context.Context, request *Request) (*Result, error) {
				filename, ok := request.Arguments["filename"].(string)
				if !ok || filename == "" {
					return nil, fmt.Errorf("missing required parameter: filename")
				}
				abstract, ok := request.Arguments["abstract"].(string)
				if !ok || abstract == "" {
					return nil, fmt.Errorf("missing required parameter: abstract")
				}
				content, ok := request.Arguments["content"].(string)
				if !ok || content == "" {
					return nil, fmt.Errorf("missing required parameter: content")
				}

				n := &vfs.VFile{Filename: filename, Abstract: abstract, Content: content}
				n, err := fs.WriteFile(ctx, n)
				if err != nil {
					return NewToolResultError(err.Error()), nil
				}

				return NewToolResultText(fmt.Sprintf("file %s saved", n.Filename)), nil
			}),
		),
	}
}
