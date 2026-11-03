package main

import (
	"flag"

	"github.com/Mesteriis/veda-wiki/internal/store"
)

func sqliteMigrateCommand(args []string) error {
	flags := flag.NewFlagSet("migrate-sqlite", flag.ContinueOnError)
	dir := flags.String("data", ".veda", "Private VEDA data directory; server must be stopped")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return ErrInvalid
	}
	return store.MigrateSQLite(*dir)
}
