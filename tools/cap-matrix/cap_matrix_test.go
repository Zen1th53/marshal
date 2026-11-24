// Package capmatrix checks that every byte cap a run can reach is listed
// with its recovery, and that each covered cap names boundary tests that
// exist in the package that owns it.
package capmatrix

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type capRow struct {
	producer, capBytes, status, owner, recovery string
	fixtures                                    []fixture
}

type fixture struct{ pkg, test string }

func readMatrix(t *testing.T) []capRow {
	t.Helper()
	f, err := os.Open("caps.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) < 2 || strings.Join(records[0], ",") != "producer,cap_bytes,status,owner,fixtures,recovery" {
		t.Fatalf("caps.csv header = %v", records[0])
	}
	var rows []capRow
	for _, r := range records[1:] {
		row := capRow{producer: r[0], capBytes: r[1], status: r[2], owner: r[3], recovery: r[5]}
		for _, item := range strings.Split(r[4], ";") {
			pkg, test, ok := strings.Cut(strings.TrimSpace(item), ":")
			if !ok || pkg == "" || test == "" {
				t.Fatalf("%s: fixture %q is not package:name", row.producer, item)
			}
			row.fixtures = append(row.fixtures, fixture{pkg, test})
		}
		rows = append(rows, row)
	}
	return rows
}

// TestAllRunCapsHaveRecovery requires every listed cap to state its
// recovery and every covered cap to name tests that exist in their package.
// When MARSHAL_CAP_REPORTS lists `go test -json` report files, each covered
// test must also have passed in them.
func TestAllRunCapsHaveRecovery(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	passed := reportedPasses(t)
	seen := map[string]bool{}
	for _, row := range readMatrix(t) {
		if seen[row.producer] {
			t.Errorf("%s listed twice", row.producer)
		}
		seen[row.producer] = true
		if strings.TrimSpace(row.recovery) == "" || strings.TrimSpace(row.capBytes) == "" {
			t.Errorf("%s has no cap or recovery", row.producer)
		}
		switch row.status {
		case "covered":
			for _, fx := range row.fixtures {
				if !testDeclared(t, filepath.Join(root, fx.pkg), fx.test) {
					t.Errorf("%s: %s does not declare %s", row.producer, fx.pkg, fx.test)
				}
				if passed != nil && !passed[fx.pkg+":"+fx.test] {
					t.Errorf("%s: %s %s did not pass in the reports", row.producer, fx.pkg, fx.test)
				}
			}
		case "pending":
			if strings.TrimSpace(row.owner) == "" {
				t.Errorf("%s is pending with no owner", row.producer)
			}
		default:
			t.Errorf("%s: unknown status %q", row.producer, row.status)
		}
	}
}

func testDeclared(t *testing.T, dir, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(t \*testing\.T\)`)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if decl.Match(data) {
			return true
		}
	}
	return false
}

// reportedPasses reads `go test -json` reports, keyed by package directory
// and test name, or nil when none are given.
func reportedPasses(t *testing.T) map[string]bool {
	t.Helper()
	list := strings.TrimSpace(os.Getenv("MARSHAL_CAP_REPORTS"))
	if list == "" {
		return nil
	}
	const module = "github.com/Zen1th53/marshal/"
	passed := map[string]bool{}
	for _, path := range filepath.SplitList(list) {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for scanner.Scan() {
			var event struct{ Action, Package, Test string }
			if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Action == "pass" && event.Test != "" {
				passed[strings.TrimPrefix(event.Package, module)+":"+event.Test] = true
			}
		}
		f.Close()
	}
	return passed
}
