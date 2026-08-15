# gitrb

gitrb is a local bridge between Roblox Studio and Git. It exports a Roblox
place or a selected group of instances into a readable project folder, and it
can pull changes from that folder back into Studio.

The project includes:

- a Go command-line tool and local HTTP server;
- a Roblox Studio plugin;
- Git and GitHub CLI workflows;
- import and export for `.rbxl` place files and `.rbxm` model files;
  `.rbxmx` model files remain supported for compatibility.

## Requirements

- Go 1.25 or newer to build from source;
- Roblox Studio;
- Git for version control;
- GitHub CLI (`gh`) for creating and publishing a GitHub repository.

## Build

```powershell
go build -o gitrb.exe ./cmd/gitrb
```

Run the tests with:

```powershell
go test ./...
```

## Create a project

Initialize a project folder and start the bridge:

```powershell
.\gitrb.exe init --name MyGame C:\path\to\my-game
.\gitrb.exe serve --project C:\path\to\my-game
```

The server listens on `127.0.0.1:1648` by default. The server can be bound to
another address with `--listen`. Use `--token` to require a local token.

## Roblox Studio plugin

Build the plugin package from the Luau source:

```powershell
.\gitrb.exe plugin --source plugin\GitRB.plugin.lua --output GitRB.rbxm
```

Copy `GitRB.rbxm` to the Roblox Studio Plugins directory, then restart Studio
if the plugin was already loaded. The plugin provides these actions:

- connect to the local bridge;
- push the entire game;
- push the current selection;
- pull the project folder into Studio;
- pull and remove children not present in a managed snapshot.

The plugin stores the last project and revision in Studio plugin settings.
Pushes include the base revision. If the project changed since that revision,
the bridge returns a conflict and does not overwrite the newer files.

## Git-friendly project format

Each instance is represented by a `.gitrb-node.json` file. Script source is
stored separately as `.luau`:

```text
gitrb.json
.gitrb/
  index.json
  meta.json
src/
  Workspace/
    .gitrb-node.json
    Baseplate/
      .gitrb-node.json
      source.server.luau
```

The generated metadata contains the Roblox class, name, order, properties,
attributes, tags, and parent path. Files removed from Studio are deleted only
when they are listed in `.gitrb/index.json`; unrelated files are left alone.

See [docs/format.md](docs/format.md) for the snapshot and HTTP protocol.

## Roblox place and model files

Import an existing place or model into a project:

```powershell
.\gitrb.exe import --project C:\path\to\my-game --input game.rbxl
.\gitrb.exe import --project C:\path\to\my-game --input library.rbxmx
.\gitrb.exe import --project C:\path\to\my-game --input library.rbxm
```

Export the project as a Roblox place or model:

```powershell
.\gitrb.exe export --project C:\path\to\my-game --output game.rbxl
.\gitrb.exe export --project C:\path\to\my-game --output library.rbxmx
.\gitrb.exe export --project C:\path\to\my-game --output library.rbxm
```

The `build` command is an alias for `export`. Unsupported native properties
are skipped with warnings rather than assigned guessed values.

## GitHub workflow

Create a local Git repository, commit the project, and push it:

```powershell
git -C C:\path\to\my-game init
.\gitrb.exe commit --project C:\path\to\my-game --message "Initial Studio sync"
.\gitrb.exe push --project C:\path\to\my-game
```

The GitHub command delegates authentication and repository creation to `gh`:

```powershell
gh auth login
gh repo create OWNER/my-game --private --source C:\path\to\my-game --remote origin --push
```

The `gitrb github` command is also available:

```powershell
.\gitrb.exe github --project C:\path\to\my-game --repo OWNER/my-game --visibility private
```

## Release 1.0.1

The `v1.0.1` release adds native `.rbxl` binary place conversion while
retaining `.rbxm` and `.rbxmx` model conversion.
