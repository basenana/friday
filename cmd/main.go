package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/basenana/friday/config"
)

var (
	configPath    string
	workspacePath string
)

func main() {
	flag.StringVar(&configPath, "c", "", "config file path")
	flag.StringVar(&workspacePath, "w", "", "workspace directory")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	if workspacePath != "" {
		cfg.Workspace = workspacePath
	}

	args := flag.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "chat":
		runChat(cfg, args[1:])
	case "session":
		runSession(cfg, args[1:])
	default:
		fmt.Printf("unknown command: %s\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: friday [options] <command> [arguments]")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  chat                  start interactive chat")
	fmt.Println("  chat --session <id>  start chat with specified session")
	fmt.Println("  session list          list active sessions")
	fmt.Println("  session new           create new session")
	fmt.Println("  session use <id>      switch to session")
	fmt.Println("  session current       show current session")
	fmt.Println("  session show <id>     show session details")
	fmt.Println("  session alias <id> <name>  set session alias")
	fmt.Println("  session archive <id>  archive session")
	fmt.Println("  session unarchive <id>  unarchive session")
	fmt.Println("  session archived      list archived sessions")
	fmt.Println("  session delete <id>   delete session")
	fmt.Println("")
	fmt.Println("Options:")
	fmt.Println("  -c <path>    config file path")
	fmt.Println("  -w <path>    workspace directory")
}
