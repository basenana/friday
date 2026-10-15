package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"reflect"
	"strings"
	"testing"
)

func testClipboardPNG(t *testing.T) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func testClipboardJPEG(t *testing.T) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestClipboardBase64ImageResizesLongImage(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 100, 3000))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}

	got, err := clipboardBase64Image("image/png", "clipboard.png", base64.StdEncoding.EncodeToString(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(got.Data)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > 2048 || cfg.Height > 2048 {
		t.Fatalf("clipboard image dimensions = %dx%d, want max edge <= 2048", cfg.Width, cfg.Height)
	}
	if got.Filename != "clipboard.png" {
		t.Fatalf("filename = %q, want clipboard.png", got.Filename)
	}
}

func TestReadClipboardImageFromMacOSPasteboard(t *testing.T) {
	pngData := testClipboardPNG(t)
	runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "osascript" || len(args) < 4 {
			t.Fatalf("command = %q %v, want osascript JavaScript", name, args)
		}
		return []byte(fmt.Sprintf(`{"mimeType":"image/png","filename":"clipboard.png","data":"%s"}`, base64.StdEncoding.EncodeToString(pngData))), nil
	}

	image, err := readClipboardImageWithRunner(context.Background(), "darwin", runner)
	if err != nil {
		t.Fatal(err)
	}
	if image.MediaType != "image/png" || image.Filename != "clipboard.png" || image.Data == "" {
		t.Fatalf("image = %#v", image)
	}
}

func TestReadClipboardImageFallsBackAcrossLinuxBackends(t *testing.T) {
	var calls []string
	jpegData := testClipboardJPEG(t)
	runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		calls = append(calls, call)
		if call == "xclip -selection clipboard -t image/jpeg -o" {
			return jpegData, nil
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
	if image.MediaType != "image/jpeg" || image.Filename != "clipboard.jpg" || image.Data == "" {
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
