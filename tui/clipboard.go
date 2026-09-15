package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/basenana/friday/core/types"
)

const maxClipboardImageBytes = 20 * 1024 * 1024
const maxClipboardCommandOutputBytes = maxClipboardImageBytes*4/3 + 4096

type clipboardCommandRunner func(context.Context, string, ...string) ([]byte, error)

func readClipboardImage(ctx context.Context) (types.ImageContent, error) {
	return readClipboardImageWithRunner(ctx, runtime.GOOS, runClipboardCommand)
}

func runClipboardCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout := newClipboardCappedBuffer(maxClipboardCommandOutputBytes)
	stderr := newClipboardCappedBuffer(4096)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", err, detail)
		}
		return nil, err
	}
	if stdout.overflow {
		return nil, fmt.Errorf("clipboard command output exceeds size limit")
	}
	return stdout.Bytes(), nil
}

type clipboardCappedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func newClipboardCappedBuffer(limit int) *clipboardCappedBuffer {
	return &clipboardCappedBuffer{limit: limit}
}

func (b *clipboardCappedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return written, nil
	}
	if len(p) > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return written, nil
}

func readClipboardImageWithRunner(ctx context.Context, goos string, run clipboardCommandRunner) (types.ImageContent, error) {
	switch goos {
	case "darwin":
		return readMacOSClipboardImage(ctx, run)
	case "linux":
		return readLinuxClipboardImage(ctx, run)
	default:
		return types.ImageContent{}, fmt.Errorf("clipboard image reading is not supported on %s", goos)
	}
}

func readMacOSClipboardImage(ctx context.Context, run clipboardCommandRunner) (types.ImageContent, error) {
	const script = `ObjC.import('AppKit');
const pasteboard = $.NSPasteboard.generalPasteboard;
let result = null;
const maxBytes = 20971520;
const formats = [['public.png', 'image/png', 'clipboard.png'], ['public.jpeg', 'image/jpeg', 'clipboard.jpg']];
for (const format of formats) {
  const data = pasteboard.dataForType(format[0]);
  if (data && Number(data.length) > 0) {
    result = Number(data.length) > maxBytes
      ? {error: 'clipboard image exceeds 20 MiB'}
      : {mimeType: format[1], filename: format[2], data: ObjC.unwrap(data.base64EncodedStringWithOptions(0))};
    break;
  }
}
if (!result) {
  const tiff = pasteboard.dataForType('public.tiff');
  if (tiff && Number(tiff.length) > 0) {
    const rep = $.NSBitmapImageRep.imageRepWithData(tiff);
    const png = rep && rep.representationUsingTypeProperties($.NSBitmapImageFileTypePNG, $({}));
    if (png) result = Number(png.length) > maxBytes
      ? {error: 'clipboard image exceeds 20 MiB'}
      : {mimeType: 'image/png', filename: 'clipboard.png', data: ObjC.unwrap(png.base64EncodedStringWithOptions(0))};
  }
}
JSON.stringify(result);`
	out, err := run(ctx, "osascript", "-l", "JavaScript", "-e", script)
	if err != nil {
		return types.ImageContent{}, fmt.Errorf("cannot read macOS clipboard: %w", err)
	}
	var payload struct {
		MimeType string `json:"mimeType"`
		Filename string `json:"filename"`
		Data     string `json:"data"`
		Error    string `json:"error"`
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return types.ImageContent{}, fmt.Errorf("clipboard does not contain a PNG or JPEG image")
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return types.ImageContent{}, fmt.Errorf("invalid image data returned by macOS clipboard: %w", err)
	}
	if payload.Error != "" {
		return types.ImageContent{}, fmt.Errorf("%s", payload.Error)
	}
	return clipboardBase64Image(payload.MimeType, payload.Filename, payload.Data)
}

func readLinuxClipboardImage(ctx context.Context, run clipboardCommandRunner) (types.ImageContent, error) {
	type candidate struct {
		name, mime, filename string
		args                 []string
	}
	candidates := []candidate{
		{name: "wl-paste", mime: "image/png", filename: "clipboard.png", args: []string{"--no-newline", "--type", "image/png"}},
		{name: "wl-paste", mime: "image/jpeg", filename: "clipboard.jpg", args: []string{"--no-newline", "--type", "image/jpeg"}},
		{name: "xclip", mime: "image/png", filename: "clipboard.png", args: []string{"-selection", "clipboard", "-t", "image/png", "-o"}},
		{name: "xclip", mime: "image/jpeg", filename: "clipboard.jpg", args: []string{"-selection", "clipboard", "-t", "image/jpeg", "-o"}},
	}
	var failures []string
	for _, candidate := range candidates {
		data, err := run(ctx, candidate.name, candidate.args...)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s %s: %v", candidate.name, candidate.mime, err))
			continue
		}
		if len(data) == 0 {
			failures = append(failures, fmt.Sprintf("%s %s: clipboard returned no data", candidate.name, candidate.mime))
			continue
		}
		if len(data) > maxClipboardImageBytes {
			return types.ImageContent{}, fmt.Errorf("clipboard image exceeds %d MiB", maxClipboardImageBytes/(1024*1024))
		}
		return types.ImageContent{
			Type: types.ImageTypeBase64, MediaType: candidate.mime,
			Filename: candidate.filename, Data: base64.StdEncoding.EncodeToString(data),
		}, nil
	}
	return types.ImageContent{}, fmt.Errorf("clipboard image unavailable; attempted backends: %s", strings.Join(failures, "; "))
}

func clipboardBase64Image(mimeType, filename, data string) (types.ImageContent, error) {
	if mimeType != "image/png" && mimeType != "image/jpeg" {
		return types.ImageContent{}, fmt.Errorf("unsupported clipboard image type %q", mimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(decoded) == 0 {
		return types.ImageContent{}, fmt.Errorf("clipboard returned invalid image data")
	}
	if len(decoded) > maxClipboardImageBytes {
		return types.ImageContent{}, fmt.Errorf("clipboard image exceeds %d MiB", maxClipboardImageBytes/(1024*1024))
	}
	return types.ImageContent{
		Type: types.ImageTypeBase64, MediaType: mimeType, Filename: filename, Data: data,
	}, nil
}
