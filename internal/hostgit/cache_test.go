package hostgit

import (
	"reflect"
	"testing"
)

func TestSourceStatusChangesSkipsOnlyUntrackedCaches(t *testing.T) {
	status := " M README\n" +
		"?? tools/tests/__pycache__/test_a.cpython-313.pyc\n" +
		"?? __pycache__/\n" +
		"?? .pytest_cache/v/cache/lastfailed\n" +
		"?? src/new.py\n" +
		" M pkg/__pycache__/tracked.pyc\n" +
		"?? \"odd name/__pycache__/x.pyc\"\n" +
		"?? notacache__pycache__.py\n"
	want := []string{" M README", "?? src/new.py", " M pkg/__pycache__/tracked.pyc", "?? \"odd name/__pycache__/x.pyc\"", "?? notacache__pycache__.py"}
	if got := SourceStatusChanges(status); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := SourceStatusChanges("?? a/__pycache__/b.pyc\n\n"); len(got) != 0 {
		t.Fatalf("cache-only status reported changes: %q", got)
	}
	for _, name := range []string{"__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache"} {
		if !DisposableCacheDir(name) {
			t.Fatalf("%s is not a disposable cache", name)
		}
	}
	if DisposableCacheDir("build") || DisposableCacheDir("src") {
		t.Fatal("source directory treated as cache")
	}
}
