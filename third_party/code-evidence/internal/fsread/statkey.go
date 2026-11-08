package fsread

import (
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sync"
)

// StatKey identifies one version of a file for cache validation: size,
// modification and status-change times in nanoseconds, inode and device.
// A file whose key is unchanged is assumed (not proven) to hold the same
// bytes; callers must never certify bytes on the strength of a StatKey
// alone (SPEC §3: cited files are re-hashed after capture).
type StatKey struct {
	Size    int64
	MtimeNS int64
	CtimeNS int64
	Ino     uint64
	Dev     uint64
}

// keyOf derives a StatKey. ok is false on platforms without inode and
// ctime information, where stat-based caching is disabled.
func keyOf(fi fs.FileInfo) (StatKey, bool) {
	k, ok := sysKey(fi)
	if !ok {
		return StatKey{}, false
	}
	k.Size = fi.Size()
	k.MtimeNS = fi.ModTime().UnixNano()
	return k, true
}

// StatKey stats rel (without following a final symlink) and returns its
// key. regular is false for symlinks and non-regular files.
func (r *Repo) StatKey(rel string) (key StatKey, regular, ok bool, err error) {
	fi, err := r.Lstat(rel)
	if err != nil {
		return StatKey{}, false, false, err
	}
	if !fi.Mode().IsRegular() {
		return StatKey{}, false, false, nil
	}
	k, ok := keyOf(fi)
	return k, true, ok, nil
}

// ReadFileKeyed is ReadFile that also returns the StatKey of the opened
// file. stable is true only when the key is available and identical before
// and after the read (the file did not visibly change while being read).
func (r *Repo) ReadFileKeyed(rel string, max int64) (b []byte, key StatKey, stable bool, err error) {
	rel, err = CleanRel(rel)
	if err != nil {
		return nil, StatKey{}, false, err
	}
	native := filepath.FromSlash(rel)
	st, err := r.root.Lstat(native)
	if err != nil {
		return nil, StatKey{}, false, err
	}
	if !st.Mode().IsRegular() {
		return nil, StatKey{}, false, ErrNotRegular
	}
	if st.Size() > max {
		return nil, StatKey{}, false, ErrTooLarge
	}
	f, err := r.root.OpenFile(native, os.O_RDONLY|oNoFollow|oNonBlock, 0)
	if err != nil {
		return nil, StatKey{}, false, err
	}
	defer f.Close()
	fst, err := f.Stat()
	if err != nil {
		return nil, StatKey{}, false, err
	}
	if !fst.Mode().IsRegular() {
		return nil, StatKey{}, false, ErrNotRegular
	}
	k1, ok1 := keyOf(fst)
	b, err = io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, StatKey{}, false, err
	}
	if int64(len(b)) > max {
		return nil, StatKey{}, false, ErrTooLarge
	}
	fst2, err := f.Stat()
	if err != nil {
		return b, k1, false, nil
	}
	k2, ok2 := keyOf(fst2)
	return b, k1, ok1 && ok2 && k1 == k2 && k1.Size == int64(len(b)), nil
}

// StatKeys stats many files, opening each parent directory once (still
// confined by os.Root) instead of walking every path from the root.
// keys[i] and ok[i] describe rels[i]; ok is false for errors, non-regular
// files and platforms without StatKey support.
func (r *Repo) StatKeys(rels []string, workers int) (keys []StatKey, ok []bool) {
	keys = make([]StatKey, len(rels))
	ok = make([]bool, len(rels))
	byDir := map[string][]int{}
	var dirs []string
	for i, rel := range rels {
		c, err := CleanRel(rel)
		if err != nil {
			continue
		}
		d := path.Dir(c)
		if byDir[d] == nil {
			dirs = append(dirs, d)
		}
		byDir[d] = append(byDir[d], i)
	}
	next := make(chan string)
	var wg sync.WaitGroup
	for w := 0; w < max(1, workers); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range next {
				dr := r.root
				if d != "." {
					sub, err := r.root.OpenRoot(filepath.FromSlash(d))
					if err != nil {
						continue
					}
					dr = sub
				}
				for _, i := range byDir[d] {
					fi, err := dr.Lstat(path.Base(rels[i]))
					if err != nil || !fi.Mode().IsRegular() {
						continue
					}
					keys[i], ok[i] = keyOf(fi)
				}
				if dr != r.root {
					dr.Close()
				}
			}
		}()
	}
	for _, d := range dirs {
		next <- d
	}
	close(next)
	wg.Wait()
	return keys, ok
}
