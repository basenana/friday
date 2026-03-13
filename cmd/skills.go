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
			fmt.Println("\nUse 'friday skills install --url <url>' to install a skill")
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
	// Parse URL to get filename
	parsedURL, err := url.Parse(skillURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	filename := filepath.Base(parsedURL.Path)
	if filename == "" || filename == "." {
		return fmt.Errorf("cannot determine filename from URL")
	}

	// Download to temp file
	tmpFile, err := os.CreateTemp("", "skill-*.download")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	fmt.Printf("Downloading from %s...\n", skillURL)

	// Use client with timeout
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, skillURL, nil)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("create request: %w", err)
	}

	client := &http.Client{
		Timeout: time.Minute, // 1 minute timeout
	}
	resp, err := client.Do(req)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		tmpFile.Close()
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// Limit download size to prevent DoS
	limitedReader := io.LimitReader(resp.Body, maxDownloadSize+1)
	_, err = io.Copy(tmpFile, limitedReader)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("download failed: %w", err)
	}

	// Check if we exceeded the limit
	if _, err := tmpFile.Seek(0, 2); err != nil {
		tmpFile.Close()
		return fmt.Errorf("check file size: %w", err)
	}
	stat, err := tmpFile.Stat()
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("stat file: %w", err)
	}
	if stat.Size() > maxDownloadSize {
		tmpFile.Close()
		return fmt.Errorf("download exceeds maximum size (%d bytes)", maxDownloadSize)
	}

	tmpFile.Close()

	return installSkillFromArchive(skillsPath, tmpPath, filename)
}

func installSkillFromFile(skillsPath, filePath string) error {
	// Check file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", filePath)
	}

	filename := filepath.Base(filePath)
	return installSkillFromArchive(skillsPath, filePath, filename)
}

func installSkillFromArchive(skillsPath, archivePath, filename string) error {
	var skillName string
	var err error

	// Determine archive type and extract
	if strings.HasSuffix(filename, ".zip") {
		skillName, err = extractZip(skillsPath, archivePath)
	} else if strings.HasSuffix(filename, ".tar.gz") || strings.HasSuffix(filename, ".tgz") {
		skillName, err = extractTarGz(skillsPath, archivePath)
	} else if strings.HasSuffix(filename, ".tar") {
		skillName, err = extractTar(skillsPath, archivePath)
	} else {
		return fmt.Errorf("unsupported archive format: %s (supported: .zip, .tar.gz, .tgz, .tar)", filename)
	}

	if err != nil {
		return err
	}

	// Verify SKILL.md exists
	skillDir := filepath.Join(skillsPath, skillName)
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		_ = os.RemoveAll(skillDir)
		return fmt.Errorf("invalid skill: SKILL.md not found in archive")
	}

	// Validate skill by loading it directly from the extracted directory
	loader := skills.NewLoader(skillsPath)
	skill, err := loader.LoadSkillFromDir(skillName)
	if err != nil {
		_ = os.RemoveAll(skillDir)
		return fmt.Errorf("invalid skill: %w", err)
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

	var skillName string

	for _, f := range r.File {
		parts := strings.Split(f.Name, "/")
		if len(parts) > 0 && parts[0] != "" {
			if skillName == "" {
				skillName = parts[0]
			}
		}

		if f.FileInfo().IsDir() {
			continue
		}

		// Reject symlinks for security
		if f.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlinks not allowed in skill archives: %s", f.Name)
		}

		relPath := f.Name
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

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return "", err
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

	return extractTarFromReader(destDir, gzr)
}

func extractTar(destDir, archivePath string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	return extractTarFromReader(destDir, file)
}

func extractTarFromReader(destDir string, r io.Reader) (string, error) {
	cleanDestDir := filepath.Clean(destDir)
	tr := tar.NewReader(r)

	var skillName string

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar: %w", err)
		}

		parts := strings.Split(header.Name, "/")
		if len(parts) > 0 && parts[0] != "" {
			if skillName == "" {
				skillName = parts[0]
			}
		}

		if header.Typeflag == tar.TypeDir {
			continue
		}

		// Reject symlinks for security
		if header.Typeflag == tar.TypeSymlink {
			return "", fmt.Errorf("symlinks not allowed in skill archives: %s", header.Name)
		}

		targetPath := filepath.Join(destDir, header.Name)

		// zip slip protection
		cleanTarget := filepath.Clean(targetPath)
		if !strings.HasPrefix(cleanTarget, cleanDestDir+string(filepath.Separator)) {
			return "", fmt.Errorf("invalid file path (potential zip slip): %s", header.Name)
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return "", err
		}

		outFile, err := os.Create(targetPath)
		if err != nil {
			return "", err
		}

		_, err = io.Copy(outFile, tr)
		outFile.Close()
		if err != nil {
			return "", err
		}

		// Set file permissions with safe mask (remove setuid/setgid/sticky bits)
		safeMode := os.FileMode(header.Mode) & 0755
		_ = os.Chmod(targetPath, safeMode)
	}

	if skillName == "" {
		return "", fmt.Errorf("could not determine skill name from archive")
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
