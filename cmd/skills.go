package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/skills"
	"github.com/basenana/friday/workspace"
)

const (
	// maxDownloadSize limits skill archive downloads to 100MB
	maxDownloadSize = 100 * 1024 * 1024
	// maxExtractedSize limits total extracted size to 500MB (zip bomb protection)
	maxExtractedSize = 500 * 1024 * 1024
	// downloadTimeout is the maximum time for downloading a skill archive
	downloadTimeout = 60 * time.Second
)

// skillsCmd represents the skills command
var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage skills",
	Long:  `Manage skills for the Friday AI assistant.`,
}

// skillsListCmd represents the skills list command
var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed skills",
	Long:  `List all installed skills with their descriptions.`,
	Run: func(cmd *cobra.Command, args []string) {
		ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
		loader := skills.NewLoader(ws.SkillsPath())
		if err := loader.Load(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to load skills: %v\n", err)
			os.Exit(1)
		}

		skillList := loader.List()
		if len(skillList) == 0 {
			fmt.Println("No skills installed")
			fmt.Println("\nUse follow command to install a skill")
			fmt.Println("\n  friday skills install --url <url>")
			fmt.Println("\n  friday skills install --file <path>")
			return
		}

		fmt.Println("Installed skills:")
		for _, skill := range skillList {
			fmt.Printf("  %s\n", skill.Name)
			fmt.Printf("    %s\n", skill.Description)
		}
	},
}

// skillsDeleteCmd represents the skills delete command
var skillsDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a skill",
	Long:  `Delete an installed skill by name.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		skillName := args[0]

		ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
		loader := skills.NewLoader(ws.SkillsPath())
		if err := loader.Load(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to load skills: %v\n", err)
			os.Exit(1)
		}

		if loader.Get(skillName) == nil {
			fmt.Printf("Skill not found: %s\n", skillName)
			os.Exit(1)
		}

		registry := skills.NewRegistry(loader)
		if err := registry.Delete(skillName); err != nil {
			fmt.Fprintf(os.Stderr, "failed to delete skill: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Deleted skill: %s\n", skillName)
	},
}

var skillsInstallURL string
var skillsInstallFile string

// skillsInstallCmd represents the skills install command
var skillsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install a skill",
	Long:  `Install a skill from a URL or local file (zip or tar.gz format).`,
	Run: func(cmd *cobra.Command, args []string) {
		if skillsInstallURL == "" && skillsInstallFile == "" {
			fmt.Fprintln(os.Stderr, "Error: must specify either --url or --file")
			if err := cmd.Help(); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to display help: %v\n", err)
			}
			os.Exit(1)
		}

		ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
		skillsPath := ws.SkillsPath()

		// Ensure skills directory exists
		if err := os.MkdirAll(skillsPath, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create skills directory: %v\n", err)
			os.Exit(1)
		}

		var err error
		if skillsInstallURL != "" {
			err = installSkillFromURL(skillsPath, skillsInstallURL)
		} else {
			err = installSkillFromFile(skillsPath, skillsInstallFile)
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to install skill: %v\n", err)
			os.Exit(1)
		}
	},
}

func installSkillFromURL(skillsPath, skillURL string) error {
	fmt.Printf("Downloading from %s...\n", skillURL)

	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, skillURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// Create temp directory for extraction
	tmpDir, err := os.MkdirTemp(os.TempDir(), "skill-install-")
	if err != nil {
		return fmt.Errorf("create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Determine filename from Content-Disposition header or URL
	filename := getFilenameFromResponse(resp, skillURL)

	// Download to temp file
	tmpArchivePath := filepath.Join(tmpDir, filename)
	tmpFile, err := os.Create(tmpArchivePath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	// Limit download size to prevent DoS
	limitedReader := io.LimitReader(resp.Body, maxDownloadSize+1)
	_, err = io.Copy(tmpFile, limitedReader)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	tmpFile.Close()

	// Check if we exceeded the limit
	stat, err := os.Stat(tmpArchivePath)
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	if stat.Size() > maxDownloadSize {
		return fmt.Errorf("download exceeds maximum size (%d bytes)", maxDownloadSize)
	}

	return installValidatedSkill(skillsPath, tmpArchivePath, filename)
}

// getFilenameFromResponse extracts filename from Content-Disposition header or URL
func getFilenameFromResponse(resp *http.Response, skillURL string) string {
	// Try Content-Disposition header first
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		filename := parseContentDispositionFilename(cd)
		if filename != "" {
			// Sanitize filename to prevent path traversal
			filename = sanitizeFilename(filename)
			if filename != "" {
				return filename
			}
		}
	}

	// Try URL path
	parsedURL, err := url.Parse(skillURL)
	if err == nil {
		filename := filepath.Base(parsedURL.Path)
		// Check if filename has a valid archive extension
		if isValidArchiveFilename(filename) {
			return filename
		}
		// Try to use query parameter (e.g., ?slug=baidu-search)
		if slug := parsedURL.Query().Get("slug"); slug != "" {
			slug = sanitizeFilename(slug)
			if slug != "" {
				return slug + ".zip"
			}
		}
	}

	// Default to skill.zip
	return "skill.zip"
}

// parseContentDispositionFilename extracts filename from Content-Disposition header
// Supports both filename="xxx" and filename*=UTF-8”xxx formats (RFC 6266)
func parseContentDispositionFilename(cd string) string {
	// Try filename*= (RFC 5987 encoded) first
	for _, part := range strings.Split(cd, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "filename*=") {
			// Format: filename*=UTF-8''encoded_filename
			value := strings.TrimPrefix(part, "filename*=")
			value = strings.TrimPrefix(strings.TrimPrefix(value, "filename*="), "FILENAME*=")
			// Decode UTF-8'' prefix
			if idx := strings.Index(value, "''"); idx != -1 {
				encoded := value[idx+2:]
				if decoded, err := url.QueryUnescape(encoded); err == nil {
					return decoded
				}
			}
		}
	}

	// Fall back to filename=
	for _, part := range strings.Split(cd, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "filename=") {
			value := strings.TrimPrefix(part, "filename=")
			value = strings.TrimPrefix(strings.TrimPrefix(value, "FILENAME="), "filename=")
			filename := strings.Trim(value, `"`)
			filename = strings.TrimSpace(filename)
			if filename != "" {
				return filename
			}
		}
	}

	return ""
}

// sanitizeFilename removes path traversal characters and ensures a safe filename
func sanitizeFilename(filename string) string {
	// Use filepath.Base to remove any directory components
	filename = filepath.Base(filename)
	// Remove null bytes and other problematic characters
	filename = strings.ReplaceAll(filename, "\x00", "")
	// Trim whitespace
	filename = strings.TrimSpace(filename)
	// Reject empty or dot-only filenames
	if filename == "" || filename == "." || filename == ".." {
		return ""
	}
	return filename
}

// isValidArchiveFilename checks if the filename has a valid archive extension
func isValidArchiveFilename(filename string) bool {
	if filename == "" || filename == "." || filename == "/" {
		return false
	}
	lower := strings.ToLower(filename)
	return strings.HasSuffix(lower, ".zip") ||
		strings.HasSuffix(lower, ".tar.gz") ||
		strings.HasSuffix(lower, ".tgz") ||
		strings.HasSuffix(lower, ".tar")
}

// extractAndValidateSkill extracts archive and validates the skill
func extractAndValidateSkill(destDir, archivePath, filename string) (string, error) {
	var skillName string
	var err error

	// Determine archive type and extract
	if strings.HasSuffix(filename, ".zip") {
		skillName, err = extractZip(destDir, archivePath)
	} else if strings.HasSuffix(filename, ".tar.gz") || strings.HasSuffix(filename, ".tgz") {
		skillName, err = extractTarGz(destDir, archivePath)
	} else if strings.HasSuffix(filename, ".tar") {
		skillName, err = extractTar(destDir, archivePath)
	} else {
		return "", fmt.Errorf("unsupported archive format: %s (supported: .zip, .tar.gz, .tgz, .tar)", filename)
	}

	if err != nil {
		return "", err
	}

	// Verify SKILL.md exists
	skillDir := filepath.Join(destDir, skillName)
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		return "", fmt.Errorf("invalid skill: SKILL.md not found in archive")
	}

	// Validate skill by loading it
	loader := skills.NewLoader(destDir)
	if _, err := loader.LoadSkillFromDir(skillName); err != nil {
		return "", fmt.Errorf("invalid skill: %w", err)
	}

	return skillName, nil
}

func installSkillFromFile(skillsPath, filePath string) error {
	// Check file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", filePath)
	}

	filename := filepath.Base(filePath)
	return installValidatedSkill(skillsPath, filePath, filename)
}

// installValidatedSkill extracts, validates and installs a skill from an archive
func installValidatedSkill(skillsPath, archivePath, filename string) error {
	// Create temp directory for extraction and validation
	tmpDir, err := os.MkdirTemp(os.TempDir(), "skill-install-")
	if err != nil {
		return fmt.Errorf("create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Extract to temp directory first for validation
	extractDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("create extraction directory: %w", err)
	}

	skillName, err := extractAndValidateSkill(extractDir, archivePath, filename)
	if err != nil {
		return err
	}

	// Move validated skill to skills directory
	srcSkillDir := filepath.Join(extractDir, skillName)
	dstSkillDir := filepath.Join(skillsPath, skillName)

	// Check if skill already exists
	if _, err := os.Stat(dstSkillDir); err == nil {
		if err := os.RemoveAll(dstSkillDir); err != nil {
			return fmt.Errorf("remove existing skill: %w", err)
		}
	}

	// Move the skill directory
	if err := os.Rename(srcSkillDir, dstSkillDir); err != nil {
		return fmt.Errorf("move skill to skills directory: %w", err)
	}

	// Load and display skill info
	loader := skills.NewLoader(skillsPath)
	skill, err := loader.LoadSkillFromDir(skillName)
	if err != nil {
		return fmt.Errorf("load installed skill: %w", err)
	}

	fmt.Printf("Installed skill: %s\n", skill.Name)
	fmt.Printf("  %s\n", skill.Description)

	return nil
}

func extractZip(destDir, archivePath string) (string, error) {
	cleanDestDir := filepath.Clean(destDir)

	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	// Check if all files share a common root directory
	// A proper skill archive has structure: skill-name/SKILL.md, skill-name/scripts/...
	// If files are at top level (SKILL.md, scripts/...), we need to create a directory
	var firstPart string
	hasRootDir := true
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(f.Name, "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		if firstPart == "" {
			firstPart = parts[0]
		} else if firstPart != parts[0] {
			// Different first parts means no common root directory
			hasRootDir = false
			break
		}
	}

	// Also check if the first part looks like a directory (contains SKILL.md or other skill files)
	// If the first part itself is SKILL.md, then there's no root directory
	if firstPart == "SKILL.md" || firstPart == "_meta.json" {
		hasRootDir = false
	}

	// If no root directory, use archive filename (without extension) as skill name
	var skillName string
	if !hasRootDir {
		skillName = strings.TrimSuffix(filepath.Base(archivePath), ".zip")
	} else {
		skillName = firstPart
	}

	var totalExtractedSize int64
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		// Reject symlinks for security
		if f.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlinks not allowed in skill archives: %s", f.Name)
		}

		var relPath string
		if hasRootDir {
			relPath = f.Name
		} else {
			// Prepend skill name as root directory
			relPath = filepath.Join(skillName, f.Name)
		}
		targetPath := filepath.Join(destDir, relPath)

		// zip slip protection
		cleanTarget := filepath.Clean(targetPath)
		if !strings.HasPrefix(cleanTarget, cleanDestDir+string(filepath.Separator)) {
			return "", fmt.Errorf("invalid file path (potential zip slip): %s", relPath)
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return "", err
		}

		outFile, err := os.Create(targetPath)
		if err != nil {
			return "", err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return "", err
		}

		// Zip bomb protection: limit extracted size
		limitedRc := io.LimitReader(rc, maxExtractedSize-totalExtractedSize+1)
		n, err := io.Copy(outFile, limitedRc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return "", err
		}

		totalExtractedSize += n
		if totalExtractedSize > maxExtractedSize {
			return "", fmt.Errorf("archive exceeds maximum extracted size (%d bytes)", maxExtractedSize)
		}

		// Set file permissions with safe mask (remove setuid/setgid/sticky bits)
		safeMode := f.Mode() & 0755
		_ = os.Chmod(targetPath, safeMode)
	}

	if skillName == "" {
		return "", fmt.Errorf("could not determine skill name from archive")
	}

	return skillName, nil
}

func extractTarGz(destDir, archivePath string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("open gzip: %w", err)
	}
	defer gzr.Close()

	return extractTarFromReader(destDir, gzr, archivePath)
}

func extractTar(destDir, archivePath string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	return extractTarFromReader(destDir, file, archivePath)
}

func extractTarFromReader(destDir string, r io.Reader, archivePath string) (string, error) {
	// Create temp staging directory
	stagingDir := filepath.Join(destDir, ".tmp-staging")
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	tr := tar.NewReader(r)

	var firstPart string
	hasRootDir := true
	var totalExtractedSize int64

	// Single pass: extract to staging and determine structure
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar: %w", err)
		}

		if header.Typeflag == tar.TypeDir {
			continue
		}

		if header.Typeflag == tar.TypeSymlink {
			return "", fmt.Errorf("symlinks not allowed in skill archives: %s", header.Name)
		}

		// Track directory structure
		parts := strings.Split(strings.TrimSuffix(header.Name, "/"), "/")
		if len(parts) > 0 && parts[0] != "" {
			if firstPart == "" {
				firstPart = parts[0]
			} else if firstPart != parts[0] {
				hasRootDir = false
			}
		}

		// Extract to staging directory preserving original path
		targetPath := filepath.Join(stagingDir, header.Name)

		// zip slip protection
		cleanTarget := filepath.Clean(targetPath)
		if !strings.HasPrefix(cleanTarget, stagingDir+string(filepath.Separator)) {
			return "", fmt.Errorf("invalid file path (potential zip slip): %s", header.Name)
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return "", err
		}

		outFile, err := os.Create(targetPath)
		if err != nil {
			return "", err
		}

		// Zip bomb protection
		remaining := maxExtractedSize - totalExtractedSize + 1
		limitedTr := io.LimitReader(tr, remaining)
		n, err := io.Copy(outFile, limitedTr)
		outFile.Close()
		if err != nil {
			return "", err
		}

		totalExtractedSize += n
		if totalExtractedSize > maxExtractedSize {
			return "", fmt.Errorf("archive exceeds maximum extracted size (%d bytes)", maxExtractedSize)
		}

		// Set file permissions with safe mask
		safeMode := os.FileMode(header.Mode) & 0755
		_ = os.Chmod(targetPath, safeMode)
	}

	if firstPart == "SKILL.md" || firstPart == "_meta.json" {
		hasRootDir = false
	}

	var skillName string
	if !hasRootDir {
		skillName = strings.TrimSuffix(filepath.Base(archivePath), ".tar.gz")
		skillName = strings.TrimSuffix(skillName, ".tgz")
		skillName = strings.TrimSuffix(skillName, ".tar")
	} else {
		skillName = firstPart
	}

	if skillName == "" {
		return "", fmt.Errorf("could not determine skill name from archive")
	}

	// Move from staging to final location
	finalSkillDir := filepath.Join(destDir, skillName)
	if hasRootDir {
		// Files are in stagingDir/firstPart/, move to destDir/firstPart/
		srcDir := filepath.Join(stagingDir, firstPart)
		if err := os.Rename(srcDir, finalSkillDir); err != nil {
			return "", fmt.Errorf("move skill directory: %w", err)
		}
	} else {
		// Create skill directory and move all files into it
		if err := os.MkdirAll(finalSkillDir, 0755); err != nil {
			return "", fmt.Errorf("create skill directory: %w", err)
		}
		// Move all items from staging to skill directory
		entries, err := os.ReadDir(stagingDir)
		if err != nil {
			return "", fmt.Errorf("read staging directory: %w", err)
		}
		for _, entry := range entries {
			src := filepath.Join(stagingDir, entry.Name())
			dst := filepath.Join(finalSkillDir, entry.Name())
			if err := os.Rename(src, dst); err != nil {
				return "", fmt.Errorf("move file to skill directory: %w", err)
			}
		}
	}

	return skillName, nil
}

func init() {
	rootCmd.AddCommand(skillsCmd)
	skillsCmd.AddCommand(skillsListCmd)
	skillsCmd.AddCommand(skillsDeleteCmd)
	skillsCmd.AddCommand(skillsInstallCmd)

	skillsInstallCmd.Flags().StringVar(&skillsInstallURL, "url", "", "URL to download skill archive from")
	skillsInstallCmd.Flags().StringVar(&skillsInstallFile, "file", "", "Local path to skill archive file")
}
