package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/setup"
)

var (
	chatSessionID string
	chatVerbose   bool
	chatIsolate   bool
	chatTemporary bool
	chatImage     string
)

var chatCmd = &cobra.Command{
	Use:   "chat [message]",
	Short: "Send a message to AI assistant, once the task is complete, the process will exit immediately.",
	Long: `Send a message to the AI assistant and print the response.

Message can be provided as:
  - Command argument: friday chat "hello"
  - Stdin pipe: echo "hello" | friday chat
  - Stdin pipe: cat file.txt | friday chat
  - Combined: cat error.log | friday chat "why is this error?"`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		// Get user message from args and/or stdin
		var argMessage, stdinMessage string

		// Read from args
		if len(args) > 0 {
			argMessage = strings.Join(args, "\n")
		}

		// Read from stdin if piped
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read stdin: %v\n", err)
				os.Exit(1)
			}
			stdinMessage = strings.TrimSpace(string(data))
		}

		// Combine messages
		var userMessage string
		if argMessage != "" {
			userMessage = argMessage
		}
		if stdinMessage != "" {
			if userMessage == "" {
				userMessage = stdinMessage
			} else {
				userMessage = userMessage + "\n\n" + stdinMessage
			}
		}

		userMessage = strings.TrimSpace(userMessage)
		if userMessage == "" {
			fmt.Fprintln(os.Stderr, "Error: no message provided")
			fmt.Fprintln(os.Stderr, "Usage: friday chat \"your message\"")
			fmt.Fprintln(os.Stderr, "   or: echo \"your message\" | friday chat")
			fmt.Fprintln(os.Stderr, "   or: cat file.txt | friday chat \"your question\"")
			os.Exit(1)
		}

		// Setup agent
		var opts []setup.Option
		if chatSessionID != "" {
			if chatIsolate || chatTemporary {
				fmt.Fprintln(os.Stderr, "Error: --session cannot be used with --isolate or --temporary")
				os.Exit(1)
			}
			opts = append(opts, setup.WithSessionID(chatSessionID))
		}
		if chatIsolate {
			if chatTemporary {
				fmt.Fprintln(os.Stderr, "Error: --isolate and --temporary cannot be used together")
				os.Exit(1)
			}
			opts = append(opts, setup.WithIsolate(true))
		}
		if chatTemporary {
			opts = append(opts, setup.WithTemporary(true))
		}

		if chatVerbose {
			opts = append(opts, setup.WithVerbose(true))
		}

		agentCtx, err := setup.NewAgent(sessMgr, cfg, opts...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		defer agentCtx.TaskManager.KillAll()

		// Process image if provided
		var image *types.ImageContent
		if chatImage != "" {
			image, err = processImage(chatImage)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error processing image: %v\n", err)
				os.Exit(1)
			}
		}

		// Send message and print response
		resp := agentCtx.Chat(ctx, userMessage, image)
		setup.PrintResponse(resp)
	},
}

func init() {
	chatCmd.Flags().StringVarP(&chatSessionID, "session", "s", "", "session ID to use")
	chatCmd.Flags().BoolVarP(&chatIsolate, "isolate", "i", false, "create isolated session for one-time task or subtask")
	chatCmd.Flags().BoolVarP(&chatTemporary, "temporary", "t", false, "create temporary session that won't persist messages")
	chatCmd.Flags().BoolVarP(&chatVerbose, "verbose", "v", false, "verbose output")
	chatCmd.Flags().StringVar(&chatImage, "image", "", "image URL or local file path")
	rootCmd.AddCommand(chatCmd)
}

// isURL checks if a path is a URL
func isURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")
}

// detectImageMediaType detects the MIME type of an image file
func detectImageMediaType(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Read first 512 bytes for detection
	buffer := make([]byte, 512)
	_, err = file.Read(buffer)
	if err != nil {
		return "", err
	}

	contentType := http.DetectContentType(buffer)

	// Validate supported image formats
	supportedTypes := []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
	for _, t := range supportedTypes {
		if contentType == t {
			return contentType, nil
		}
	}

	return "", fmt.Errorf("unsupported image type: %s", contentType)
}

// loadImageAsBase64 reads a file and converts it to Base64
func loadImageAsBase64(filePath string) (*types.ImageContent, error) {
	// Check file size (limit 5MB)
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if fileInfo.Size() > 5*1024*1024 {
		return nil, fmt.Errorf("file too large: %d bytes (max 5MB)", fileInfo.Size())
	}

	// Detect MIME type
	mediaType, err := detectImageMediaType(filePath)
	if err != nil {
		return nil, err
	}

	// Read file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Base64 encode
	encoded := base64.StdEncoding.EncodeToString(data)

	return &types.ImageContent{
		Type:      types.ImageTypeBase64,
		MediaType: mediaType,
		Data:      encoded,
	}, nil
}

// processImage processes an image input (URL or local file)
func processImage(imagePath string) (*types.ImageContent, error) {
	if imagePath == "" {
		return nil, nil
	}

	if isURL(imagePath) {
		// URL type
		return &types.ImageContent{
			Type: types.ImageTypeURL,
			URL:  imagePath,
		}, nil
	}

	// Local file
	return loadImageAsBase64(imagePath)
}
