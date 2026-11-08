package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

func (c command) handleMCPServeStdio(ctx context.Context, args []string) (bool, error) {
	hasStdio := false
	for _, a := range args {
		if a == "--stdio" || a == "-stdio" {
			hasStdio = true
			break
		}
	}
	if !hasStdio {
		return false, nil
	}

	flags := flag.NewFlagSet("mcp serve", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	flags.Bool("stdio", false, "run MCP server over standard I/O")
	project := flags.String("project", "", "project root directory")
	role := flags.String("role", "marshal", "role authorization (marshal|worker)")
	task := flags.String("task", "", "task ID for worker role")
	lease := flags.String("lease", "", "immutable worker lease ID")

	if err := flags.Parse(args); err != nil {
		return true, fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}

	if flags.NArg() != 0 || (*role != "marshal" && *role != "worker") || (*role == "worker" && (*task == "" || (stdioWorkerLeaseRequired() && *lease == ""))) || (*role == "marshal" && *task != "") {
		return true, fmt.Errorf("%w: invalid MCP role/task scope", model.ErrInvalid)
	}
	root := *project
	if root == "" {
		root = c.root
	}

	runtime, err := app.OpenWithOptions(ctx, root, app.Options{KnowledgeReadOnly: *role == "worker"})
	if err != nil {
		return true, err
	}
	defer runtime.Close()

	srv, err := newStdioMCPServer(runtime, *role, *task)
	if err != nil {
		return true, err
	}
	if *role == "worker" && *lease != "" {
		if err := srv.RequireWorkerLease(ctx, *lease); err != nil {
			return true, err
		}
	}
	err = srv.ServeStdio(ctx, c.stdin, c.stdout)
	return true, err
}

func (c command) handleMCPRelay(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("mcp relay", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	requests := flags.String("requests", "", "request FIFO")
	responses := flags.String("responses", "", "response FIFO")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *requests == "" || *responses == "" {
		return fmt.Errorf("%w: MCP relay endpoints required", model.ErrInvalid)
	}
	return sessionmcp.Relay(ctx, *requests, *responses, c.stdin, c.stdout)
}
