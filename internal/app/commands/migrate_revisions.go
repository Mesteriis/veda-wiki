package commands

import (
	"flag"
	"fmt"
	"io"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// MigrateRevisions converts a stopped data directory to store format 21, or
// with --verify checks a converted store against its migration backup. It
// prints only counts: no paths, titles, texts or digests.
func MigrateRevisions(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("migrate-revisions", flag.ContinueOnError)
	dir := flags.String("data", ".veda", "Private VEDA data directory; server must be stopped")
	verify := flags.Bool("verify", false, "Check the converted store against state.before-revisions.* without writing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return domain.ErrInvalid
	}
	if *verify {
		report, err := store.VerifyRevisions(*dir)
		if report != (store.MigrationReport{}) {
			if _, e := printCounts(out, report); e != nil && err == nil {
				err = e
			}
		}
		if err != nil {
			fmt.Fprintln(out, "verify: FAIL")
			return err
		}
		_, err = fmt.Fprintln(out, "verify: OK")
		return err
	}
	report, err := store.MigrateRevisions(*dir)
	if err != nil {
		return err
	}
	if _, err = printCounts(out, report); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "blob cleanup paused until state.before-revisions.* is removed")
	return err
}

func printCounts(out io.Writer, r store.MigrationReport) (int, error) {
	return fmt.Fprintf(out, "articles: %d\nkept revisions: %d\nremoved revisions: %d\n", r.Articles, r.KeptRevisions, r.RemovedRevisions)
}
