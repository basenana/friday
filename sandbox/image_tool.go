package sandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
	_ "image/gif"
)

const (
	toolImage              = "image"
	defaultImagePrompt     = "Describe the image."
	defaultImageMaxBytesMB = 5.0
	defaultImageMaxEdge    = 2048
	minImageMaxEdge        = 1024
	maxLocalImageBytes     = 50 * 1024 * 1024
	minImageJPEGQuality    = 55
)

var imageJPEGQualities = []int{85, 75, 65, minImageJPEGQuality}

type ImageAnalyzer interface {
	Analyze(ctx context.Context, prompt, modelOverride string, image *types.ImageContent) (string, error)
}

// NewImageTool creates an image understanding tool that auto-resizes large images before analysis.
func NewImageTool(exec *Executor, workdir string, analyzer ImageAnalyzer) *tools.Tool {
	desc := fmt.Sprintf(`Analyze an image using the configured vision-capable model.

Current working directory: %s

Parameters:
- image: image path or http/https URL
- prompt: optional analysis prompt. Defaults to "Describe the image."
- model: optional model name override for this call
- maxBytesMb: optional maximum processed image size in megabytes. Defaults to 5

Usage notes:
- The tool automatically resizes and compresses large images before sending them to the model
- Prefer absolute paths when possible
- Use this tool when the user asks about screenshots, photos, charts, or any visual content
- When the user mentions a screenshot, flowchart, chart, diagram, or image file, prefer locating the image and then calling this tool instead of guessing from the filename or surrounding text
- If the user gives only a vague location, use the minimum necessary file search to identify the best candidate image, then call this tool immediately
- If you discover a new image during exploration and understanding that image could materially help complete the task, you may proactively call this tool even if the user did not explicitly ask for image analysis
- Once a likely target image is found, do not keep exploring with repeated ls/find calls unless this tool fails or multiple candidates remain ambiguous`, workdir)

	return tools.NewTool(toolImage,
		tools.WithDescription(desc),
		tools.WithString("image", tools.Required(), tools.Description("Image path or URL to analyze")),
		tools.WithString("prompt", tools.Description("Question or instruction for the image analysis")),
		tools.WithString("model", tools.Description("Optional model name override for this one image analysis")),
		tools.WithNumber("maxBytesMb", tools.Description("Maximum processed image size in megabytes before upload")),
		tools.WithToolHandler(imageToolHandler(exec, workdir, analyzer)),
	)
}

func imageToolHandler(exec *Executor, workdir string, analyzer ImageAnalyzer) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		imageRef, ok := req.Arguments["image"].(string)
		if !ok || strings.TrimSpace(imageRef) == "" {
			return tools.NewToolResultError("image is required"), nil
		}

		prompt, _ := req.Arguments["prompt"].(string)
		if strings.TrimSpace(prompt) == "" {
			prompt = defaultImagePrompt
		}

		modelOverride, _ := req.Arguments["model"].(string)
		maxBytes, err := imageMaxBytesFromRequest(req.Arguments["maxBytesMb"])
		if err != nil {
			return tools.NewToolResultError(err.Error()), nil
		}

		imageContent, err := prepareImageContent(ctx, exec, workdir, imageRef, maxBytes)
		if err != nil {
			return tools.NewToolResultError(err.Error()), nil
		}

		result, err := analyzer.Analyze(ctx, prompt, strings.TrimSpace(modelOverride), imageContent)
		if err != nil {
			return tools.NewToolResultError(fmt.Sprintf("image analysis failed: %v", err)), nil
		}
		return tools.NewToolResultText(strings.TrimSpace(result)), nil
	}
}

func imageMaxBytesFromRequest(raw any) (int64, error) {
	maxBytesMB := defaultImageMaxBytesMB
	if raw == nil {
		return int64(maxBytesMB * 1024 * 1024), nil
	}

	value, ok := raw.(float64)
	if !ok {
		return 0, fmt.Errorf("maxBytesMb must be a number")
	}
	if value <= 0 {
		return 0, fmt.Errorf("maxBytesMb must be greater than 0")
	}
	return int64(value * 1024 * 1024), nil
}

func prepareImageContent(ctx context.Context, exec *Executor, workdir, imageRef string, maxBytes int64) (*types.ImageContent, error) {
	data, mediaType, err := readImageBytes(ctx, exec, workdir, imageRef, maxBytes)
	if err != nil {
		return nil, err
	}

	return optimizeImageForModel(data, mediaType, maxBytes)
}

func readImageBytes(ctx context.Context, exec *Executor, workdir, imageRef string, maxBytes int64) ([]byte, string, error) {
	if isRemoteImageRef(imageRef) {
		return downloadImage(ctx, imageRef, maxBytes)
	}

	absPath, err := resolveLocalImagePath(workdir, imageRef)
	if err != nil {
		return nil, "", err
	}
	if err := validateImagePathAccess(exec.config, workdir, absPath); err != nil {
		return nil, "", err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to stat image: %w", err)
	}
	if info.IsDir() {
		return nil, "", fmt.Errorf("image path is a directory: %s", absPath)
	}
	if info.Size() > maxLocalImageBytes {
		return nil, "", fmt.Errorf("image file too large: %d bytes exceeds %d byte limit", info.Size(), maxLocalImageBytes)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read image: %w", err)
	}
	mediaType := detectMediaType(data, filepath.Ext(absPath))
	if err := validateSupportedImageType(mediaType); err != nil {
		return nil, "", err
	}
	return data, mediaType, nil
}

func optimizeImageForModel(data []byte, mediaType string, maxBytes int64) (*types.ImageContent, error) {
	if maxBytes <= 0 {
		maxBytes = int64(defaultImageMaxBytesMB * 1024 * 1024)
	}

	mediaType = normalizeMediaType(mediaType)
	if err := validateSupportedImageType(mediaType); err != nil {
		return nil, err
	}

	if mediaType == "image/webp" {
		if int64(len(data)) > maxBytes {
			return nil, fmt.Errorf("webp image exceeds maxBytesMb and cannot be resized without additional decoder support")
		}
		return bytesToImageContent(mediaType, data), nil
	}

	cfg, cfgFormat, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to inspect image: %w", err)
	}

	if int64(len(data)) <= maxBytes && maxInt(cfg.Width, cfg.Height) <= defaultImageMaxEdge {
		return bytesToImageContent(mediaTypeFromFormat(cfgFormat, mediaType), data), nil
	}

	src, srcFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	current := resizeToMaxEdge(src, defaultImageMaxEdge)
	sourceMediaType := mediaTypeFromFormat(srcFormat, mediaType)
	for {
		if sourceMediaType == "image/png" {
			pngBytes, err := encodePNG(current)
			if err == nil && int64(len(pngBytes)) <= maxBytes {
				return bytesToImageContent("image/png", pngBytes), nil
			}
		}

		flattened := flattenToOpaque(current, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		for _, quality := range imageJPEGQualities {
			jpegBytes, err := encodeJPEG(flattened, quality)
			if err != nil {
				return nil, fmt.Errorf("failed to encode jpeg: %w", err)
			}
			if int64(len(jpegBytes)) <= maxBytes {
				return bytesToImageContent("image/jpeg", jpegBytes), nil
			}
		}

		nextEdge := int(math.Round(float64(maxInt(current.Bounds().Dx(), current.Bounds().Dy())) * 0.85))
		if nextEdge < minImageMaxEdge {
			break
		}
		current = resizeToMaxEdge(current, nextEdge)
	}

	return nil, fmt.Errorf("image is still too large after resizing and compression")
}

func downloadImage(ctx context.Context, imageURL string, maxBytes int64) ([]byte, string, error) {
	parsed, err := neturl.Parse(imageURL)
	if err != nil {
		return nil, "", fmt.Errorf("invalid image URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, "", fmt.Errorf("unsupported image URL scheme: %s", parsed.Scheme)
	}

	downloadLimit := maxBytes * 6
	if downloadLimit < 30*1024*1024 {
		downloadLimit = 30 * 1024 * 1024
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create image request: %w", err)
	}
	req.Header.Set("User-Agent", "friday-image-tool/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("failed to download image: unexpected status %s", resp.Status)
	}
	if resp.ContentLength > downloadLimit {
		return nil, "", fmt.Errorf("image download too large: %d bytes exceeds %d byte limit", resp.ContentLength, downloadLimit)
	}

	limited := io.LimitReader(resp.Body, downloadLimit+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read image response: %w", err)
	}
	if int64(len(data)) > downloadLimit {
		return nil, "", fmt.Errorf("image download exceeded %d byte limit", downloadLimit)
	}

	mediaType := detectMediaType(data, "")
	if headerType := normalizeMediaType(resp.Header.Get("Content-Type")); headerType != "" && strings.HasPrefix(headerType, "image/") {
		mediaType = headerType
	}
	if err := validateSupportedImageType(mediaType); err != nil {
		return nil, "", err
	}
	return data, mediaType, nil
}

func resolveLocalImagePath(workdir, imageRef string) (string, error) {
	path := expandPath(strings.TrimSpace(imageRef), workdir)
	if !filepath.IsAbs(path) {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("failed to resolve image path: %w", err)
		}
		path = absPath
	}
	return filepath.Clean(path), nil
}

func validateImagePathAccess(cfg *Config, workdir, absPath string) error {
	for _, deny := range cfg.Sandbox.Filesystem.Deny {
		pattern := expandPath(deny, workdir)
		if matchesDeniedPath(pattern, absPath) {
			return fmt.Errorf("image path is denied by sandbox rules: %s", absPath)
		}
	}
	return nil
}

func matchesDeniedPath(pattern, path string) bool {
	if pattern == "" {
		return false
	}
	if strings.ContainsAny(pattern, "*?[]") {
		if ok, _ := filepath.Match(pattern, path); ok {
			return true
		}
		if ok, _ := filepath.Match(pattern, filepath.Base(path)); ok {
			return true
		}
		return false
	}

	pattern = filepath.Clean(pattern)
	path = filepath.Clean(path)
	if pattern == path {
		return true
	}
	return strings.HasPrefix(path, pattern+string(os.PathSeparator))
}

func isRemoteImageRef(ref string) bool {
	return strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")
}

func detectMediaType(data []byte, fallbackExt string) string {
	mediaType := normalizeMediaType(http.DetectContentType(data))
	if mediaType != "" && mediaType != "application/octet-stream" {
		return mediaType
	}

	switch strings.ToLower(strings.TrimSpace(fallbackExt)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return mediaType
	}
}

func normalizeMediaType(mediaType string) string {
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	if mediaType == "" {
		return ""
	}
	if idx := strings.Index(mediaType, ";"); idx >= 0 {
		mediaType = strings.TrimSpace(mediaType[:idx])
	}
	if mediaType == "image/jpg" {
		return "image/jpeg"
	}
	return mediaType
}

func validateSupportedImageType(mediaType string) error {
	switch normalizeMediaType(mediaType) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return nil
	default:
		return fmt.Errorf("unsupported image type: %s", mediaType)
	}
}

func bytesToImageContent(mediaType string, data []byte) *types.ImageContent {
	return &types.ImageContent{
		Type:      types.ImageTypeBase64,
		MediaType: normalizeMediaType(mediaType),
		Data:      base64.StdEncoding.EncodeToString(data),
	}
}

func resizeToMaxEdge(src image.Image, maxEdge int) image.Image {
	bounds := src.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	currentMax := maxInt(width, height)
	if currentMax <= maxEdge {
		return src
	}

	scale := float64(maxEdge) / float64(currentMax)
	targetW := int(math.Round(float64(width) * scale))
	targetH := int(math.Round(float64(height) * scale))
	if targetW < 1 {
		targetW = 1
	}
	if targetH < 1 {
		targetH = 1
	}
	return resizeBilinear(src, targetW, targetH)
}

func resizeBilinear(src image.Image, width, height int) image.Image {
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW == width && srcH == height {
		return src
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	scaleX := float64(srcW) / float64(width)
	scaleY := float64(srcH) / float64(height)

	for y := 0; y < height; y++ {
		sy := (float64(y)+0.5)*scaleY - 0.5
		y0 := clampInt(int(math.Floor(sy)), 0, srcH-1)
		y1 := clampInt(y0+1, 0, srcH-1)
		wy := sy - float64(y0)
		if wy < 0 {
			wy = 0
		}

		for x := 0; x < width; x++ {
			sx := (float64(x)+0.5)*scaleX - 0.5
			x0 := clampInt(int(math.Floor(sx)), 0, srcW-1)
			x1 := clampInt(x0+1, 0, srcW-1)
			wx := sx - float64(x0)
			if wx < 0 {
				wx = 0
			}

			c00 := color.NRGBAModel.Convert(src.At(srcBounds.Min.X+x0, srcBounds.Min.Y+y0)).(color.NRGBA)
			c10 := color.NRGBAModel.Convert(src.At(srcBounds.Min.X+x1, srcBounds.Min.Y+y0)).(color.NRGBA)
			c01 := color.NRGBAModel.Convert(src.At(srcBounds.Min.X+x0, srcBounds.Min.Y+y1)).(color.NRGBA)
			c11 := color.NRGBAModel.Convert(src.At(srcBounds.Min.X+x1, srcBounds.Min.Y+y1)).(color.NRGBA)

			dst.Set(x, y, interpolateNRGBA(c00, c10, c01, c11, wx, wy))
		}
	}
	return dst
}

func interpolateNRGBA(c00, c10, c01, c11 color.NRGBA, wx, wy float64) color.NRGBA {
	top := lerpColor(c00, c10, wx)
	bottom := lerpColor(c01, c11, wx)
	return lerpColor(top, bottom, wy)
}

func lerpColor(a, b color.NRGBA, t float64) color.NRGBA {
	return color.NRGBA{
		R: uint8(lerp(float64(a.R), float64(b.R), t)),
		G: uint8(lerp(float64(a.G), float64(b.G), t)),
		B: uint8(lerp(float64(a.B), float64(b.B), t)),
		A: uint8(lerp(float64(a.A), float64(b.A), t)),
	}
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}

func flattenToOpaque(src image.Image, bg color.NRGBA) *image.RGBA {
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			alpha := float64(c.A) / 255.0
			dst.SetRGBA(x-bounds.Min.X, y-bounds.Min.Y, color.RGBA{
				R: uint8(float64(c.R)*alpha + float64(bg.R)*(1-alpha)),
				G: uint8(float64(c.G)*alpha + float64(bg.G)*(1-alpha)),
				B: uint8(float64(c.B)*alpha + float64(bg.B)*(1-alpha)),
				A: 255,
			})
		}
	}
	return dst
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mediaTypeFromFormat(format, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	default:
		return normalizeMediaType(fallback)
	}
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
