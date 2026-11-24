// Command settingsdocs keeps the generated settings tables of a documentation
// page in step with the settings catalogue of the build it is compiled with.
//
//	go run ./tools/feature-inventory/cmd/settingsdocs -write docs/configuration.md
//	go run -tags governed ./tools/feature-inventory/cmd/settingsdocs -write docs/configuration.md
//	go run ./tools/feature-inventory/cmd/settingsdocs -check docs/configuration.md
//
// Each build replaces only the block marked for its own edition.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/settingscatalog"
)

func main() {
	write := flag.String("write", "", "rewrite the generated block in this file")
	check := flag.String("check", "", "fail when the generated block in this file is out of date")
	flag.Parse()
	if (*write == "") == (*check == "") {
		fmt.Fprintln(os.Stderr, "use exactly one of -write or -check")
		os.Exit(2)
	}
	path := *write
	if *check != "" {
		path = *check
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	next, err := replaceBlock(string(data), settingscatalog.EditionLabel, settingscatalog.DocsTable())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *check != "" {
		if next != string(data) {
			fmt.Fprintf(os.Stderr, "%s: the %s settings table is out of date; run settingsdocs -write\n", path, settingscatalog.EditionLabel)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func replaceBlock(text, edition, table string) (string, error) {
	begin := "<!-- settings-table:" + edition + ":begin -->"
	end := "<!-- settings-table:" + edition + ":end -->"
	i := strings.Index(text, begin)
	j := strings.Index(text, end)
	if i < 0 || j < i {
		return "", fmt.Errorf("markers %s and %s not found", begin, end)
	}
	return text[:i+len(begin)] + "\n\n" + table + "\n" + text[j:], nil
}
