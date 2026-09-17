package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/workspace"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Friday in the current directory",
	Long:  `Initialize a .friday directory in the current directory. Project initialization inherits missing workspace files and skills from HOME.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current directory: %w", err)
		}
		return runInit(cwd)
	},
}

func runInit(cwd string) error {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	fridayDir := filepath.Join(cwd, ".friday")
	if err := os.MkdirAll(fridayDir, 0o755); err != nil {
		return fmt.Errorf("create Friday directory: %w", err)
	}

	configPath, err := existingConfigPath(fridayDir)
	if err != nil {
		return err
	}
	if configPath == "" {
		configPath = filepath.Join(fridayDir, "config.json")
		seed := config.DefaultConfig()
		if !isHomeFridayDir(fridayDir) {
			seed.Workspace = "workspace"
		}
		created, writeErr := config.WriteConfig(configPath, seed)
		if writeErr != nil {
			return fmt.Errorf("write config: %w", writeErr)
		}
		if created {
			fmt.Println("Config file created:", configPath)
		}
	}
	if err := os.MkdirAll(filepath.Join(fridayDir, "agents"), 0o755); err != nil {
		return fmt.Errorf("create agents directory: %w", err)
	}

	initializedCfg, err := config.LoadForDir(configPath, cwd)
	if err != nil {
		return fmt.Errorf("load initialized config: %w", err)
	}
	ws := workspace.NewFromConfig(initializedCfg)

	if !isHomeFridayDir(fridayDir) {
		if err := ws.EnsureDir(""); err != nil {
			return fmt.Errorf("create project workspace: %w", err)
		}
		if err := ws.MkdirAll("skills"); err != nil {
			return fmt.Errorf("create project skills directory: %w", err)
		}
		if err := ws.MkdirAll("mcp"); err != nil {
			return fmt.Errorf("create project MCP directory: %w", err)
		}
		fmt.Println("Project workspace initialized at:", ws.BasePath())
		fmt.Println("Missing workspace files and skills will be inherited from HOME.")
		return nil
	}

	hostname, _ := os.Hostname()
	params := &workspace.TemplateParams{
		Paths: &workspace.Paths{
			DataDir:   initializedCfg.DataDirPath(),
			Workspace: initializedCfg.WorkspacePath(),
			Sessions:  initializedCfg.SessionsPath(),
			Memory:    initializedCfg.MemoryPath(),
			State:     initializedCfg.StatePath(),
		},
		System: &workspace.SystemInfo{
			OS:       runtime.GOOS,
			Arch:     runtime.GOARCH,
			Hostname: hostname,
		},
	}

	created, err := ws.InitWithParams(params)
	if err != nil {
		return fmt.Errorf("initialize HOME workspace: %w", err)
	}
	if err := ws.MkdirAll("mcp"); err != nil {
		return fmt.Errorf("create MCP directory: %w", err)
	}
	if len(created) == 0 {
		fmt.Println("Workspace already initialized at:", ws.BasePath())
		fmt.Println("All files already exist.")
		return nil
	}

	fmt.Println("Workspace initialized at:", ws.BasePath())
	fmt.Println("Created files:")
	for _, filename := range created {
		fmt.Printf("  %s\n", filename)
	}
	fmt.Println("Memory directory:", initializedCfg.MemoryPath())
	fmt.Println("Edit these files to customize your AI assistant's behavior.")
	return nil
}

func existingConfigPath(fridayDir string) (string, error) {
	for _, name := range []string{"config.json", "friday.yaml"} {
		path := filepath.Join(fridayDir, name)
		info, err := os.Stat(path)
		if err == nil {
			if info.IsDir() {
				return "", fmt.Errorf("config path is a directory: %s", path)
			}
			return path, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}

func isHomeFridayDir(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	if home == "" {
		return false
	}
	homeFriday, err := filepath.Abs(filepath.Join(home, ".friday"))
	if err != nil {
		return false
	}
	return filepath.Clean(path) == filepath.Clean(homeFriday)
}

func init() {
	rootCmd.AddCommand(initCmd)
}
