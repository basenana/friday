package fallback

import (
	"fmt"
	"strings"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

// AnySupportsImage returns true if at least one entry supports image input.
func AnySupportsImage(entries []ModelEntry) bool {
	for _, entry := range entries {
		if entry.Capabilities.SupportsImage {
			return true
		}
	}
	return false
}

// RequestHasImage reports whether any request history message contains an image.
func RequestHasImage(req providers.Request) bool {
	if req == nil {
		return false
	}
	for _, msg := range req.History() {
		if len(msg.ImageContents()) > 0 {
			return true
		}
	}
	return false
}

// StripImagesFromRequest returns a new request with Image fields replaced by
// text placeholders. Used when falling back to a text-only model.
func StripImagesFromRequest(req providers.Request) providers.Request {
	history := req.History()
	stripped := make([]types.Message, len(history))

	strippedCount := 0
	for i, msg := range history {
		images := msg.ImageContents()
		if len(images) > 0 {
			strippedCount += len(images)
			placeholders := make([]string, 0, len(images))
			for i := range images {
				placeholders = append(placeholders, imagePlaceholder(&images[i]))
			}
			placeholder := strings.Join(placeholders, "\n")
			stripped[i] = msg
			stripped[i].Image = nil
			stripped[i].Images = nil
			if stripped[i].Content == "" {
				stripped[i].Content = placeholder
			} else {
				stripped[i].Content = placeholder + "\n" + stripped[i].Content
			}
		} else {
			stripped[i] = msg
		}
	}

	if strippedCount == 0 {
		return req
	}

	// Create a new request with stripped history.
	newReq := providers.NewRequest(req.SystemPrompt())
	newReq.SetHistory(stripped)
	newReq.SetToolDefines(req.ToolDefines())
	newReq.SetPromptCacheKey(req.PromptCacheKey())

	return newReq
}

// imagePlaceholder generates a text description for a stripped image.
func imagePlaceholder(img *types.ImageContent) string {
	switch img.Type {
	case types.ImageTypeURL:
		return fmt.Sprintf("[image: %s]", img.URL)
	}
	if img.Filename != "" {
		return fmt.Sprintf("[image: %s]", img.Filename)
	}
	if img.Type == types.ImageTypeBase64 {
		return "[image attached (base64)]"
	}
	return "[image attached]"
}
