package sandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

type stubImageAnalyzer struct {
	prompt string
	model  string
	image  *types.ImageContent
}

func (s *stubImageAnalyzer) Analyze(ctx context.Context, prompt, model string, image *types.ImageContent) (string, error) {
	s.prompt = prompt
	s.model = model
	s.image = image
	return "ok", nil
}

func TestOptimizeImageForModelResizesLargeJPEG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2600, 2200))
	for y := 0; y < 2200; y++ {
		for x := 0; x < 2600; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 120, A: 255})
		}
	}

	var source bytes.Buffer
	if err := jpeg.Encode(&source, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("jpeg.Encode() error = %v", err)
	}

	content, err := optimizeImageForModel(source.Bytes(), "image/jpeg", 300*1024)
	if err != nil {
		t.Fatalf("optimizeImageForModel() error = %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(content.Data)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if int64(len(raw)) > 300*1024 {
		t.Fatalf("optimized image size = %d, want <= %d", len(raw), 300*1024)
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if maxInt(cfg.Width, cfg.Height) > defaultImageMaxEdge {
		t.Fatalf("optimized dimensions = %dx%d, max edge should be <= %d", cfg.Width, cfg.Height, defaultImageMaxEdge)
	}
}

func TestOptimizeImageForModelKeepsSmallPNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}

	var source bytes.Buffer
	if err := png.Encode(&source, img); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}

	content, err := optimizeImageForModel(source.Bytes(), "image/png", 1024*1024)
	if err != nil {
		t.Fatalf("optimizeImageForModel() error = %v", err)
	}
	if content.MediaType != "image/png" {
		t.Fatalf("optimizeImageForModel() mediaType = %q, want image/png", content.MediaType)
	}
}

func TestImageToolUsesDefaultPromptAndModelOverride(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "sample.png")

	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}

	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatalf("os.Create() error = %v", err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatalf("png.Encode() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("file.Close() error = %v", err)
	}

	analyzer := &stubImageAnalyzer{}
	tool := NewImageTool(NewExecutor(DefaultConfig()), tmpDir, analyzer)

	result, err := tool.Handler(context.Background(), &tools.Request{
		Arguments: map[string]interface{}{
			"image": imagePath,
			"model": "vision-override",
		},
	})
	if err != nil {
		t.Fatalf("tool handler error = %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error result: %+v", result)
	}
	if analyzer.prompt != defaultImagePrompt {
		t.Fatalf("prompt = %q, want %q", analyzer.prompt, defaultImagePrompt)
	}
	if analyzer.model != "vision-override" {
		t.Fatalf("model override = %q, want %q", analyzer.model, "vision-override")
	}
	if analyzer.image == nil {
		t.Fatalf("expected analyzer to receive image content")
	}
}
