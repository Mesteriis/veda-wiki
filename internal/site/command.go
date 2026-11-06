package site

import (
	"flag"
	"fmt"
	"io"
	"sort"
)

// Command runs `veda-site` (or `veda export-site`): it exports a checkout and
// prints a summary and every formatting warning as path:line: code: message.
func Command(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("export-site", flag.ContinueOnError)
	flags.SetOutput(stderr)
	title := flags.String("title", "", "site title (default: veda.yaml title or the directory name)")
	strict := flags.Bool("strict", false, "fail when any page has formatting warnings")
	lang := flags.String("lang", "en", "interface and alert language: en or ru")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: veda-site [--title T] [--lang en|ru] [--strict] <checkout> <output-directory>")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		flags.Usage()
		return fmt.Errorf("expected a checkout and an output directory")
	}
	report, err := Export(Options{Dir: flags.Arg(0), Out: flags.Arg(1), Title: *title, Strict: *strict, Lang: *lang})
	names := make([]string, 0, len(report.Warnings))
	for name := range report.Warnings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, w := range report.Warnings[name] {
			fmt.Fprintf(stderr, "%s:%d: %s: %s\n", name, w.Line, w.Code, w.Message)
		}
	}
	for _, name := range report.Skipped {
		fmt.Fprintf(stderr, "%s: skipped, README.md in the same directory is the index\n", name)
	}
	if report.Pages > 0 {
		fmt.Fprintf(stdout, "%d pages, %d images, %d withheld files, %d pages with warnings\n", report.Pages, report.Assets, report.Withheld, len(report.Warnings))
	}
	return err
}
