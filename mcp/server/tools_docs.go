package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/survivalservers/studio-go/mcp/guides"
)

type setupIn struct {
	Engine           string `json:"engine" jsonschema:"the game's engine: unreal, unity, godot, or custom for any other engine or your own"`
	OS               string `json:"os" jsonschema:"windows for a zip of a Windows server build, linux for a Linux image"`
	PersistentServer bool   `json:"persistent_server,omitempty" jsonschema:"true when the game runs as a persistent server (saves that last across restarts) instead of short matches"`
}

type searchIn struct {
	Query string `json:"query" jsonschema:"what to look for, in plain words"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many sections (default 4, at most 10)"`
}

const resourceScheme = "docs://"

func (s *srv) addDocs(server *mcp.Server) {
	tool(s, server, "setup_guide", "Setup guide for an engine", "The guide to make a game's dedicated server run here, for its engine and system, from the docs: building the server, the settings it reads, telling us it is ready, uploading, and testing on your own computer first. Read it before changing a game project.", reads,
		map[string][]string{"engine": guides.Engines, "os": osValues},
		func(ctx context.Context, _ *mcp.CallToolRequest, in setupIn) (*mcp.CallToolResult, error) {
			text, err := setupText(in.Engine, in.OS, in.PersistentServer)
			if err != nil {
				return nil, err
			}
			return plainResult(text), nil
		})

	tool(s, server, "search_docs", "Search the docs", "Finds the docs sections about a question (settings, ports, stop methods, logs, persistent servers, backups, prices, switching from another host), with links to the pages.", reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, error) {
			hits := guides.Search(in.Query, clamp(in.Limit, 4, 10))
			if len(hits) == 0 {
				return plainResult(fmt.Sprintf("Nothing in the docs matches %q. The docs: %s", in.Query, guides.DocsURL())), nil
			}
			var b strings.Builder
			for i, h := range hits {
				if i > 0 {
					b.WriteString("\n\n---\n\n")
				}
				fmt.Fprintf(&b, "## %s: %s\n%s\n\n%s", h.Page.Title, h.Heading, h.Page.URL, h.Text)
			}
			return plainResult(b.String()), nil
		})

	for _, p := range guides.All() {
		page := p
		server.AddResource(&mcp.Resource{
			URI: resourceScheme + page.Slug, Name: page.Slug, Title: page.Title, Description: page.Description, MIMEType: "text/markdown",
		}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: resourceScheme + page.Slug, MIMEType: "text/markdown", Text: page.Body}}}, nil
		})
	}

	engines := map[string]string{"unreal": "Unreal Engine", "unity": "Unity", "godot": "Godot", "custom": "any other engine"}
	for _, e := range guides.Engines {
		engine, label := e, engines[e]
		name := "setup_" + engine
		if engine == "custom" {
			name = "setup_custom_engine"
		}
		server.AddPrompt(&mcp.Prompt{
			Name:        name,
			Title:       "Set up a " + label + " dedicated server",
			Description: fmt.Sprintf("Walks through making this %s project's dedicated server run here, from the docs, and ends with a test game server.", label),
			Arguments: []*mcp.PromptArgument{
				{Name: "os", Description: "windows (a zip of a Windows server build) or linux (an image); default windows", Required: false},
			},
		}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			osName := firstOf(req.Params.Arguments["os"], "windows")
			text, err := setupText(engine, osName, false)
			if err != nil {
				return nil, err
			}
			ask := fmt.Sprintf(`Set up this project's dedicated server to run on our game server hosting, following the guide below.

1. Look at the project first: engine version, how the server is built today, its ports and launch arguments.
2. Make the smallest changes the guide asks for, and tell me each one before you make it.
3. Build the %s server and test it on this computer as the guide shows.
4. Then create the app, upload the build, create a version and start one test game server; give me its address.
5. Stop the test game server when I am done with it, and tell me what is still running.

%s`, osName, text)
			return &mcp.GetPromptResult{
				Description: "Set up a " + label + " dedicated server",
				Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: ask}}},
			}, nil
		})
	}
}

func setupText(engine, osName string, persistent bool) (string, error) {
	pages, missing, err := guides.Setup(engine, osName, persistent)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Setup guide: %s, %s", engine, osName)
	if persistent {
		b.WriteString(", persistent server")
	}
	b.WriteString(". The tools follow these steps: create_app, ")
	if osName == "windows" {
		b.WriteString("upload_windows_build (or begin_windows_upload on the hosted server)")
	} else {
		b.WriteString("create_linux_build (get_registry_login to push to our registry)")
	}
	b.WriteString(", wait_for_build, create_version (dry_run first), wait_for_version, then ")
	if persistent {
		b.WriteString("create_persistent_server.")
	} else {
		b.WriteString("start_game_server.")
	}
	for _, p := range pages {
		fmt.Fprintf(&b, "\n\n---\n\nSource: %s\n\n%s", p.URL, strings.TrimSpace(p.Body))
	}
	if len(missing) > 0 {
		fmt.Fprintf(&b, "\n\n---\n\nNot written yet in the docs: %s.", strings.Join(missing, ", "))
	}
	return b.String(), nil
}
