package client

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Bridge serves the gateway's tools over another MCP transport (usually stdio), so
// any agent harness that can start a command can use the gateway. Tool calls are
// forwarded unchanged; the gateway enforces its policy as usual.
type Bridge struct {
	Endpoint Endpoint
	Version  string

	mu       sync.Mutex
	upstream *mcp.ClientSession
}

func (b *Bridge) impl() *mcp.Implementation {
	return &mcp.Implementation{Name: "sailwright-gateway-connect", Version: b.Version}
}

// session returns the upstream session, reconnecting after a transport failure.
func (b *Bridge) session(ctx context.Context) (*mcp.ClientSession, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.upstream != nil {
		return b.upstream, nil
	}
	session, err := Connect(ctx, b.Endpoint, b.impl())
	if err != nil {
		return nil, err
	}
	b.upstream = session
	return session, nil
}

func (b *Bridge) drop(session *mcp.ClientSession) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.upstream == session {
		_ = session.Close()
		b.upstream = nil
	}
}

// Run connects upstream, mirrors its tools and serves them on transport until the
// downstream client disconnects or ctx is cancelled.
func (b *Bridge) Run(ctx context.Context, transport mcp.Transport) error {
	upstream, err := b.session(ctx)
	if err != nil {
		return err
	}
	defer func() {
		b.mu.Lock()
		if b.upstream != nil {
			_ = b.upstream.Close()
		}
		b.mu.Unlock()
	}()

	options := &mcp.ServerOptions{}
	if init := upstream.InitializeResult(); init != nil {
		options.Instructions = init.Instructions
	}
	server := mcp.NewServer(b.impl(), options)

	count := 0
	for tool, err := range upstream.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("list gateway tools: %w", err)
		}
		server.AddTool(tool, b.forward(tool.Name))
		count++
	}
	if count == 0 {
		return errors.New("the gateway offers no tools")
	}
	return server.Run(ctx, transport)
}

func (b *Bridge) forward(name string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		session, err := b.session(ctx)
		if err != nil {
			return nil, err
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: req.Params.Arguments})
		if err != nil {
			// The call may or may not have reached the gateway, so it is not retried.
			// The next call reconnects, e.g. after a gateway restart.
			b.drop(session)
			return nil, fmt.Errorf("gateway call %s failed: %w", name, err)
		}
		return result, nil
	}
}
