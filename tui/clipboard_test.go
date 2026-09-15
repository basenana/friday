package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestReadClipboardImageFromMacOSPasteboard(t *testing.T) {
	runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "osascript" || len(args) < 4 {
			t.Fatalf("command = %q %v, want osascript JavaScript", name, args)
		}
		return []byte(`{"mimeType":"image/png","filename":"clipboard.png","data":"cG5n"}`), nil
	}

	image, err := readClipboardImageWithRunner(context.Background(), "darwin", runner)
	if err != nil {
		t.Fatal(err)
	}
	if image.MediaType != "image/png" || image.Filename != "clipboard.png" || image.Data != "cG5n" {
		t.Fatalf("image = %#v", image)
	}
}

func TestReadClipboardImageFallsBackAcrossLinuxBackends(t *testing.T) {
	var calls []string
	runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		calls = append(calls, call)
		if call == "xclip -selection clipboard -t image/jpeg -o" {
			return []byte("jpeg"), nil
		}
		return nil, errors.New("unavailable")
	}

	image, err := readClipboardImageWithRunner(context.Background(), "linux", runner)
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"wl-paste --no-newline --type image/png",
		"wl-paste --no-newline --type image/jpeg",
		"xclip -selection clipboard -t image/png -o",
		"xclip -selection clipboard -t image/jpeg -o",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	if image.MediaType != "image/jpeg" || image.Filename != "clipboard.jpg" || image.Data != "anBlZw==" {
		t.Fatalf("image = %#v", image)
	}
}

func TestReadClipboardImageReportsLinuxBackendFailures(t *testing.T) {
	runner := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		return nil, errors.New(name + " unavailable")
	}

	_, err := readClipboardImageWithRunner(context.Background(), "linux", runner)
	if err == nil || !strings.Contains(err.Error(), "wl-paste unavailable") || !strings.Contains(err.Error(), "xclip unavailable") {
		t.Fatalf("error = %v, want named backend failures", err)
	}
}
