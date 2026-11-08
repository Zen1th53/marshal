package lexical

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// RipgrepSearcher uses ripgrep as a candidate-file prefilter over the live
// files, then extracts hits from the captured bytes only. Files ripgrep
// reports but whose captured bytes do not match are counted as Rescanned
// (the live file changed after capture). Files changed after capture so
// that they *gained* a match are missed; use GoSearcher when exactness over
// the snapshot matters (the dossier default).
//
// ripgrep is invoked with --no-config (ignores RIPGREP_CONFIG_PATH), with
// explicit file arguments only (no directory traversal, no ignore files)
// and a minimal environment. No preprocessor (--pre) is ever configured.
type RipgrepSearcher struct {
	Bin string // path to rg; "" = look up on PATH
}

// Name implements Searcher.
func (RipgrepSearcher) Name() string { return "rg-prefilter/1" }

// Available reports whether rg can be found.
func (r RipgrepSearcher) Available() bool {
	_, err := r.bin()
	return err == nil
}

func (r RipgrepSearcher) bin() (string, error) {
	if r.Bin != "" {
		return r.Bin, nil
	}
	return exec.LookPath("rg")
}

type rgMsg struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text  string `json:"text"`
			Bytes string `json:"bytes"`
		} `json:"path"`
	} `json:"data"`
}

// Search implements Searcher.
func (r RipgrepSearcher) Search(ctx context.Context, root string, src Source, terms []string, lim Limits) (Result, error) {
	lim = lim.norm()
	terms = NormalizeTerms(terms)
	bin, err := r.bin()
	if err != nil {
		return Result{}, err
	}
	paths := src.Paths()
	idx := make(map[string]int, len(paths))
	for i, p := range paths {
		idx[p] = i
	}
	per := make([][]Hit, len(paths))
	if len(terms) == 0 {
		return merge(r.Name(), per, lim.Total, 0), nil
	}
	matched := map[int]bool{}
	const chunk = 1000
	for start := 0; start < len(paths); start += chunk {
		end := min(start+chunk, len(paths))
		args := []string{"--json", "--no-config", "--no-ignore", "--hidden", "--no-messages",
			"--ignore-case", "--fixed-strings", "--max-count", "1"}
		for _, t := range terms {
			args = append(args, "-e", t)
		}
		args = append(args, "--")
		for _, p := range paths[start:end] {
			// Leading "./" keeps paths starting with "-" from parsing as flags.
			args = append(args, "./"+p)
		}
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		cmd := exec.CommandContext(cctx, bin, args...)
		cmd.Dir = root
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C"}
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		cancel()
		if err != nil {
			// Exit status 1 means "no matches".
			if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
				return Result{}, fmt.Errorf("lexical: rg: %v", err)
			}
		}
		sc := bufio.NewScanner(&out)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			var m rgMsg
			if json.Unmarshal(sc.Bytes(), &m) != nil || m.Type != "begin" {
				continue
			}
			p := m.Data.Path.Text
			if p == "" && m.Data.Path.Bytes != "" {
				if b, err := base64.StdEncoding.DecodeString(m.Data.Path.Bytes); err == nil {
					p = string(b)
				}
			}
			p = strings.TrimPrefix(p, "./")
			if i, ok := idx[p]; ok {
				matched[i] = true
			}
		}
	}
	keys := make([]int, 0, len(matched))
	for i := range matched {
		keys = append(keys, i)
	}
	sort.Ints(keys)
	rescanned := 0
	for _, i := range keys {
		b, ok := src.Bytes(paths[i])
		if !ok {
			continue
		}
		per[i] = scanBlob(paths[i], b, terms, lim.PerFileTerm)
		if len(per[i]) == 0 {
			rescanned++
		}
	}
	return merge(r.Name(), per, lim.Total, rescanned), nil
}
