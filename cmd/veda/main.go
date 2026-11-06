// Command veda runs the VEDA service or one of its subcommands.
package main

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/Mesteriis/veda-wiki/internal/app"
	"github.com/Mesteriis/veda-wiki/internal/app/commands"
	"github.com/Mesteriis/veda-wiki/internal/site"
)

func main() {
	name := ""
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	var err error
	switch name {
	case "createsuperuser":
		err = commands.CreateSuperuser(os.Args[2:])
	case "migrate-sqlite":
		err = commands.MigrateSQLite(os.Args[2:])
	case "migrate-revisions":
		err = commands.MigrateRevisions(os.Args[2:], os.Stdout)
	case "github":
		err = commands.GitHub(os.Args[2:])
	case "access-key":
		err = commands.AccessKey(os.Args[2:], os.Stdout)
	case "import":
		err = commands.Import(os.Args[2:])
	case "mcp":
		err = commands.MCP(os.Args[2:], os.Stdin, os.Stdout)
	case "export-site":
		err = site.Command(os.Args[2:], os.Stdout, os.Stderr)
	case "mcp-relay":
		err = commands.MCPRelay(os.Args[2:], os.Stdin, os.Stdout, os.Stderr)
	default:
		err = app.Run(context.Background(), os.Args[1:])
	}
	var exit *commands.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.Code)
	}
	if err != nil {
		log.Fatal(err)
	}
}
