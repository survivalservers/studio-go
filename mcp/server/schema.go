package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	api "github.com/survivalservers/studio-go"
)

// schemaFor makes the input schema for T from its fields (json names, `jsonschema` descriptions, omitempty for
// optional) and adds the allowed values for the fields named in enums ("field" or "field.sub" for nested
// objects and array items).
func schemaFor[T any](enums map[string][]string) *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("schema for %T: %v", *new(T), err))
	}
	for path, values := range enums {
		target := s
		for _, part := range strings.Split(path, ".") {
			if target.Items != nil && target.Properties == nil {
				target = target.Items
			}
			next := target.Properties[part]
			if next == nil {
				panic(fmt.Sprintf("schema for %T: no field %q", *new(T), path))
			}
			target = next
		}
		if target.Items != nil {
			target = target.Items
		}
		target.Enum = make([]any, len(values))
		for i, v := range values {
			target.Enum[i] = v
		}
	}
	return s
}

// The hosted server makes a server per request, so schemas are made once per tool and shared.
var (
	schemas    sync.Map // tool name -> *jsonschema.Schema
	sdkSchemas = mcp.NewSchemaCache()
)

// kind says what a tool does to the studio's account, for the client's hints and for read-only mode.
type kind int

const (
	reads   kind = iota // reads only
	changes             // makes or changes something
	removes             // stops, restarts, deletes or overwrites something: marked destructive for the client
)

// tool adds one typed tool unless the server is read-only and the tool changes something.
func tool[In any](s *srv, server *mcp.Server, name, title, description string, k kind, enums map[string][]string, h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, error)) {
	if s.cfg.ReadOnly && k != reads {
		return
	}
	t := &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: description,
		InputSchema: cachedSchema[In](name, enums),
		Annotations: &mcp.ToolAnnotations{
			Title:           title,
			ReadOnlyHint:    k == reads,
			DestructiveHint: boolPtr(k == removes),
			IdempotentHint:  k == reads,
			OpenWorldHint:   boolPtr(true),
		},
	}
	mcp.AddTool(server, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		res, err := h(ctx, req, in)
		return res, nil, forTheAI(err, s.cfg.Local)
	})
}

// forTheAI adds what only an AI tool needs to an error from the client, whose words are written for any caller: the
// tool to call next, and that a key never goes in the chat.
func forTheAI(err error, local bool) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, api.ErrNoKey) && local:
		return errNoKeyLocal
	case errors.Is(err, api.ErrNoKey):
		return errNoKeyHosted
	}
	var p *api.Problem
	if !errors.As(err, &p) {
		return err
	}
	code := p.Code
	if code == "" && p.Status == http.StatusUnauthorized {
		code = "unauthenticated"
	}
	if line := aiNextStep[code]; line != "" {
		return fmt.Errorf("%w\nFor the AI tool: %s", err, line)
	}
	return err
}

// aiNextStep is the AI tool's own line after the client's next step (api.NextStep).
var aiNextStep = map[string]string{
	"unauthenticated":   "ask the developer for a current key in the MCP server's settings; never ask them to paste it into the chat.",
	"missing_scope":     "ask the developer for a key with that scope in the MCP server's settings; never ask them to paste it into the chat.",
	"key_restricted":    "use an app the key allows; never ask the developer to paste another key into the chat.",
	"version_not_ready": "wait_for_version waits for it.",
	"unknown_region":    "list_regions lists them.",
	"approval_required": "tell the developer what you asked and that a member says yes on the panel's AI agents page; once they have, call the same tool again with the same arguments and approval set to the request's ID (apr_...).",
}

// errNoKeyLocal is the local server's answer to a call without a key: it reads GS_API_KEY from its own environment.
var errNoKeyLocal = errors.New("no API key: set GS_API_KEY in this server's environment (a key from the panel, with only the scopes the agent needs); never pass it as an argument or paste it into the chat")

// errNoKeyHosted is the hosted server's answer to a call without a key: it holds none and reads no GS_API_KEY, so the
// key has to come with the request.
var errNoKeyHosted = errors.New("no API key: this hosted server holds no key of its own; the AI tool sends the API key with every request as the header Authorization: Bearer <key>, set in its MCP server settings (a key from the panel, with only the scopes the agent needs). Never paste it into the chat")

func cachedSchema[In any](name string, enums map[string][]string) *jsonschema.Schema {
	if s, ok := schemas.Load(name); ok {
		return s.(*jsonschema.Schema)
	}
	s, _ := schemas.LoadOrStore(name, schemaFor[In](enums))
	return s.(*jsonschema.Schema)
}

// noInput is the input of tools that take nothing.
type noInput struct{}
