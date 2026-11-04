package commands

import (
	"flag"
	"fmt"
	"io"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// MigrateRevisions converts a stopped data directory to store format 21 and
// prints only counts: no paths, titles, texts or digests.
func MigrateRevisions(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("migrate-revisions", flag.ContinueOnError)
	dir := flags.String("data", ".veda", "Private VEDA data directory; server must be stopped")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return domain.ErrInvalid
	}
	report, err := store.MigrateRevisions(*dir)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "articles: %d\nkept revisions: %d\nremoved revisions: %d\nblob cleanup paused until state.before-revisions.* is removed\n", report.Articles, report.KeptRevisions, report.RemovedRevisions)
	return err
}
