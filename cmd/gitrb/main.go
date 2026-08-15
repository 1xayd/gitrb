package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gitrb/internal/format"
	"gitrb/internal/project"
	"gitrb/internal/protocol"
	"gitrb/internal/server"
	"gitrb/internal/vcs"
)

const version = "1.0.1"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = commandInit(os.Args[2:])
	case "serve":
		err = commandServe(os.Args[2:])
	case "status":
		err = commandStatus(os.Args[2:])
	case "import":
		err = commandImport(os.Args[2:])
	case "export", "build":
		err = commandExport(os.Args[2:])
	case "plugin":
		err = commandPlugin(os.Args[2:])
	case "commit":
		err = commandCommit(os.Args[2:])
	case "push":
		err = commandPush(os.Args[2:])
	case "github":
		err = commandGitHub(os.Args[2:])
	case "version", "--version", "-version":
		fmt.Println("gitrb", version)
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitrb:", err)
		os.Exit(1)
	}
}

func commandInit(args []string) error {
	name := ""
	positionals := make([]string, 0, 1)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--name" || arg == "-name":
			if index+1 >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			index++
			name = args[index]
		case strings.HasPrefix(arg, "--name="):
			name = strings.TrimPrefix(arg, "--name=")
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown init option %q", arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	dir := "."
	if len(positionals) > 1 {
		return fmt.Errorf("usage: gitrb init [directory] [--name NAME]")
	}
	if len(positionals) == 1 {
		dir = positionals[0]
	}
	if name == "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		name = filepath.Base(abs)
	}
	cfg, err := project.Init(dir, name)
	if err != nil {
		return err
	}
	fmt.Printf("initialized %s in %s\n", cfg.Name, absPath(dir))
	fmt.Println("start the bridge with: gitrb serve --project", absPath(dir))
	return nil
}

func commandServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("project", ".", "gitrb project directory")
	listen := fs.String("listen", "127.0.0.1:1648", "HTTP listen address")
	token := fs.String("token", "", "optional local authentication token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: gitrb serve --project DIR [--listen 127.0.0.1:1648]")
	}
	if _, err := project.LoadConfig(*dir); err != nil {
		return fmt.Errorf("load project: %w; run gitrb init first", err)
	}
	addr := *listen
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	fmt.Printf("serving %s at http://%s\n", absPath(*dir), addr)
	fmt.Println("Roblox Studio plugin endpoint: /v1/sync/push and /v1/sync/pull")
	return server.New(absPath(*dir), *token).ListenAndServe(addr)
}

func commandStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("project", ".", "gitrb project directory")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	revision, _, err := project.Revision(*dir)
	if err != nil {
		return err
	}
	cfg, err := project.LoadConfig(*dir)
	if err != nil {
		return err
	}
	status := map[string]any{"project": cfg.Name, "revision": revision, "git": vcs.Status(absPath(*dir))}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(status)
	}
	fmt.Println("project:", cfg.Name)
	fmt.Println("revision:", revision)
	gs := vcs.Status(absPath(*dir))
	if !gs.IsRepository {
		fmt.Println("git:     not a repository")
	} else if gs.Clean {
		fmt.Printf("git:     clean (%s)\n", gs.Branch)
	} else {
		fmt.Printf("git:     changes pending (%s)\n%s", gs.Branch, gs.Porcelain)
	}
	return nil
}

func commandImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("project", ".", "gitrb project directory")
	input := fs.String("input", "", "Roblox file (.rbxl, .rbxm, or .rbxmx)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *input == "" && fs.NArg() == 1 {
		*input = fs.Arg(0)
	}
	if *input == "" || fs.NArg() > 1 {
		return fmt.Errorf("usage: gitrb import --project DIR --input game.rbxl")
	}
	read, err := format.ReadModel(*input)
	if err != nil {
		return err
	}
	result, err := project.WriteSnapshot(absPath(*dir), read.Snapshot)
	if err != nil {
		return err
	}
	fmt.Printf("imported %s into %s at revision %s\n", absPath(*input), absPath(*dir), result.Revision)
	printWarnings(read.Warnings)
	return nil
}

func commandExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("project", ".", "gitrb project directory")
	output := fs.String("output", "", "output Roblox file (.rbxl, .rbxm, or .rbxmx)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *output == "" && fs.NArg() == 1 {
		*output = fs.Arg(0)
	}
	if *output == "" || fs.NArg() > 1 {
		return fmt.Errorf("usage: gitrb export --project DIR --output game.rbxl")
	}
	_, snapshot, err := project.Revision(absPath(*dir))
	if err != nil {
		return err
	}
	result, err := format.WriteModel(absPath(*output), snapshot)
	if err != nil {
		return err
	}
	fmt.Printf("built %s\n", absPath(*output))
	printWarnings(result.Warnings)
	return nil
}

func commandPlugin(args []string) error {
	fs := flag.NewFlagSet("plugin", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	source := fs.String("source", "plugin/GitRB.plugin.lua", "plugin Luau source")
	output := fs.String("output", "GitRB.rbxm", "output .rbxm or .rbxmx plugin model")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: gitrb plugin --source plugin/GitRB.plugin.lua --output GitRB.rbxm")
	}
	script, err := os.ReadFile(*source)
	if err != nil {
		return err
	}
	snapshot := &protocol.Snapshot{
		SchemaVersion: protocol.SchemaVersion,
		Project:       "GitRBPlugin",
		Roots: []*protocol.Node{{
			ID: "GitRBPlugin", Name: "GitRBPlugin", ClassName: "Script", Order: 0,
			Script: &protocol.Script{Source: string(script)},
		}},
	}
	result, err := format.WriteModel(absPath(*output), snapshot)
	if err != nil {
		return err
	}
	fmt.Println("built plugin", absPath(*output))
	printWarnings(result.Warnings)
	return nil
}

func printWarnings(warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", warning)
	}
}

func commandCommit(args []string) error {
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("project", ".", "gitrb project directory")
	message := fs.String("message", "", "commit message")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *message == "" && fs.NArg() > 0 {
		*message = strings.Join(fs.Args(), " ")
	}
	result, err := vcs.Commit(absPath(*dir), *message)
	if err != nil {
		return err
	}
	fmt.Println(result.Output)
	return nil
}

func commandPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("project", ".", "gitrb project directory")
	remote := fs.String("remote", "origin", "Git remote")
	branch := fs.String("branch", "", "branch (defaults to Git's configured upstream)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	out, err := vcs.Push(absPath(*dir), *remote, *branch)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func commandGitHub(args []string) error {
	fs := flag.NewFlagSet("github", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("project", ".", "gitrb project directory")
	repo := fs.String("repo", "", "GitHub repository, for example owner/game")
	visibility := fs.String("visibility", "private", "private, public, or internal")
	description := fs.String("description", "", "repository description")
	push := fs.Bool("push", true, "push the initial branch")
	if err := fs.Parse(args); err != nil {
		return err
	}
	out, err := vcs.GitHubCreate(absPath(*dir), *repo, *visibility, *description, *push)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func usage() {
	fmt.Println(`gitrb - Git-friendly Roblox Studio project bridge

Usage:
	gitrb version
	gitrb init [--name NAME] [directory]
  gitrb serve --project DIR [--listen 127.0.0.1:1648] [--token TOKEN]
  gitrb status --project DIR [--json]
  gitrb import --project DIR --input game.rbxl
  gitrb export --project DIR --output game.rbxl
  gitrb build --project DIR --output model.rbxm
  gitrb plugin --source plugin/GitRB.plugin.lua --output GitRB.rbxm
  gitrb commit --project DIR --message "message"
  gitrb push --project DIR [--remote origin] [--branch BRANCH]
  gitrb github --project DIR --repo OWNER/NAME [--visibility private|public|internal]

The Studio plugin sends snapshots to the local bridge. The bridge writes src/
as readable instance metadata and standalone .luau files, with revision checks
to prevent stale Studio or Git changes from overwriting one another.`)
}
