package setup

import (
	"strings"
	"testing"
)

func TestAppendImageRefsToMessage(t *testing.T) {
	message := appendImageRefsToMessage("What is in this image?", []string{"/tmp/example.png"})

	if !strings.Contains(message, "/tmp/example.png") {
		t.Fatalf("expected image reference to be included, got %q", message)
	}
	if !strings.Contains(message, "use the image tool") {
		t.Fatalf("expected image tool hint to be included, got %q", message)
	}
}
