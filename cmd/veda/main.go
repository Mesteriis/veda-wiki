// Command veda runs the VEDA service or one of its subcommands.
package main

import (
	"context"
	"log"
	"os"

	"github.com/Mesteriis/veda-wiki/internal/app"
	"github.com/Mesteriis/veda-wiki/internal/app/commands"
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
	case "github":
		err = commands.GitHub(os.Args[2:])
	case "access-key":
		err = commands.AccessKey(os.Args[2:], os.Stdout)
	case "import":
		err = commands.Import(os.Args[2:])
	default:
		err = app.Run(context.Background(), os.Args[1:])
	}
	if err != nil {
		log.Fatal(err)
	}
}
