//nolint:revive,errorlint,gocritic // This file contains code copy-pasted from Go source
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/mod/module"
)

// Taken "as is" from go/src/cmd/compile/internal/noder/noder.go

// parseGoEmbed parses the text following "//go:embed" to extract the glob patterns.
// It accepts unquoted space-separated patterns as well as double-quoted and back-quoted Go strings.
// go/build/read.go also processes these strings and contains similar logic.
func parseGoEmbed(args string) ([]string, error) {
	var list []string
	for args = strings.TrimSpace(args); args != ""; args = strings.TrimSpace(args) {
		var path string
	Switch:
		switch args[0] {
		default:
			i := len(args)
			for j, c := range args {
				if unicode.IsSpace(c) {
					i = j
					break
				}
			}
			path = args[:i]
			args = args[i:]

		case '`':
			i := strings.Index(args[1:], "`")
			if i < 0 {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
			path = args[1 : 1+i]
			args = args[1+i+1:]

		case '"':
			i := 1
			for ; i < len(args); i++ {
				if args[i] == '\\' {
					i++
					continue
				}
				if args[i] == '"' {
					q, err := strconv.Unquote(args[:i+1])
					if err != nil {
						return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args[:i+1])
					}
					path = q
					args = args[i+1:]
					break Switch
				}
			}
			if i >= len(args) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
		}

		if args != "" {
			r, _ := utf8.DecodeRuneInString(args)
			if !unicode.IsSpace(r) {
				return nil, fmt.Errorf("invalid quoted string in //go:embed: %s", args)
			}
		}
		list = append(list, path)
	}
	return list, nil
}

// Taken "as is" from src/cmd/go/internal/load/pkg.go, internal functions' references renamed

func resolveEmbed(pkgdir string, patterns []string) (files []string, pmap map[string][]string, err error) {
	var pattern string

	// TODO(rsc): All these messages need position information for better error reports.
	pmap = make(map[string][]string)
	have := make(map[string]int)
	dirOK := make(map[string]bool)
	pid := 0 // pattern ID, to allow reuse of have map
	for _, pattern = range patterns {
		pid++

		glob, all := strings.CutPrefix(pattern, "all:")
		// Check pattern is valid for //go:embed.
		if _, err := path.Match(glob, ""); err != nil || !validEmbedPattern(glob) {
			return nil, nil, fmt.Errorf("invalid pattern syntax")
		}

		// Glob to find matches.
		match, err := filepath.Glob(strQuoteGlob(strWithFilePathSeparator(pkgdir)) + filepath.FromSlash(glob))
		if err != nil {
			return nil, nil, err
		}

		// Filter list of matches down to the ones that will still exist when
		// the directory is packaged up as a module. (If p.Dir is in the module cache,
		// only those files exist already, but if p.Dir is in the current module,
		// then there may be other things lying around, like symbolic links or .git directories.)
		var list []string
		for _, file := range match {
			// relative path to p.Dir which begins without prefix slash
			rel := filepath.ToSlash(strTrimFilePathPrefix(file, pkgdir))

			what := "file"
			info, err := os.Lstat(file)
			if err != nil {
				return nil, nil, err
			}
			if info.IsDir() {
				what = "directory"
			}

			// Check that directories along path do not begin a new module
			// (do not contain a go.mod).
			for dir := file; len(dir) > len(pkgdir)+1 && !dirOK[dir]; dir = filepath.Dir(dir) {
				if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
					return nil, nil, fmt.Errorf("cannot embed %s %s: in different module", what, rel)
				}
				if dir != file {
					if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
						return nil, nil, fmt.Errorf("cannot embed %s %s: in non-directory %s", what, rel, dir[len(pkgdir)+1:])
					}
				}
				dirOK[dir] = true
				if elem := filepath.Base(dir); isBadEmbedName(elem) {
					if dir == file {
						return nil, nil, fmt.Errorf("cannot embed %s %s: invalid name %s", what, rel, elem)
					} else {
						return nil, nil, fmt.Errorf("cannot embed %s %s: in invalid directory %s", what, rel, elem)
					}
				}
			}

			switch {
			default:
				return nil, nil, fmt.Errorf("cannot embed irregular file %s", rel)

			case info.Mode().IsRegular():
				if have[rel] != pid {
					have[rel] = pid
					list = append(list, rel)
				}

			case info.IsDir():
				// Gather all files in the named directory, stopping at module boundaries
				// and ignoring files that wouldn't be packaged into a module.
				count := 0
				err := fsysWalk(file, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					rel := filepath.ToSlash(strTrimFilePathPrefix(path, pkgdir))
					name := info.Name()
					if path != file && (isBadEmbedName(name) || ((name[0] == '.' || name[0] == '_') && !all)) {
						// Ignore bad names, assuming they won't go into modules.
						// Also avoid hidden files that user may not know about.
						// See golang.org/issue/42328.
						if info.IsDir() {
							return fs.SkipDir
						}
						return nil
					}
					if info.IsDir() {
						if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
							return filepath.SkipDir
						}
						return nil
					}
					if !info.Mode().IsRegular() {
						return nil
					}
					count++
					if have[rel] != pid {
						have[rel] = pid
						list = append(list, rel)
					}
					return nil
				})
				if err != nil {
					return nil, nil, err
				}
				if count == 0 {
					return nil, nil, fmt.Errorf("cannot embed directory %s: contains no embeddable files", rel)
				}
			}
		}

		if len(list) == 0 {
			return nil, nil, fmt.Errorf("no matching files found")
		}
		sort.Strings(list)
		pmap[pattern] = list
	}

	for file := range have {
		files = append(files, file)
	}
	sort.Strings(files)
	return files, pmap, nil
}

func validEmbedPattern(pattern string) bool {
	return pattern != "." && fs.ValidPath(pattern)
}

// isBadEmbedName reports whether name is the base name of a file that
// can't or won't be included in modules and therefore shouldn't be treated
// as existing for embedding.
func isBadEmbedName(name string) bool {
	if err := module.CheckFilePath(name); err != nil {
		return true
	}
	switch name {
	// Empty string should be impossible but make it bad.
	case "":
		return true
	// Version control directories won't be present in module.
	case ".bzr", ".hg", ".git", ".svn":
		return true
	}
	return false
}

// Picked up from various places in src/cmd/go/internal/* to satisfy the code above, with
// internal functions renamed.

// QuoteGlob returns s with all Glob metacharacters quoted.
// We don't try to handle backslash here, as that can appear in a
// file path on Windows.
func strQuoteGlob(s string) string {
	if !strings.ContainsAny(s, `*?[]`) {
		return s
	}
	var sb strings.Builder
	for _, c := range s {
		switch c {
		case '*', '?', '[', ']':
			sb.WriteByte('\\')
		}
		sb.WriteRune(c)
	}
	return sb.String()
}

// WithFilePathSeparator returns s with a trailing path separator, or the empty
// string if s is empty.
func strWithFilePathSeparator(s string) string {
	if s == "" || os.IsPathSeparator(s[len(s)-1]) {
		return s
	}
	return s + string(filepath.Separator)
}

// TrimFilePathPrefix returns s without the leading path elements in prefix,
// such that joining the string to prefix produces s.
//
// If s does not start with prefix (HasFilePathPrefix with the same arguments
// returns false), TrimFilePathPrefix returns s. If s equals prefix,
// TrimFilePathPrefix returns "".
func strTrimFilePathPrefix(s, prefix string) string {
	if prefix == "" {
		// Trimming the empty string from a path should join to produce that path.
		// (Trim("/tmp/foo", "") should give "/tmp/foo", not "tmp/foo".)
		return s
	}
	if !strHasFilePathPrefix(s, prefix) {
		return s
	}

	trimmed := s[len(prefix):]
	if len(trimmed) > 0 && os.IsPathSeparator(trimmed[0]) {
		if runtime.GOOS == "windows" && prefix == filepath.VolumeName(prefix) && len(prefix) == 2 && prefix[1] == ':' {
			// Joining a relative path to a bare Windows drive letter produces a path
			// relative to the working directory on that drive, but the original path
			// was absolute, not relative. Keep the leading path separator so that it
			// remains absolute when joined to prefix.
		} else {
			// Prefix ends in a regular path element, so strip the path separator that
			// follows it.
			trimmed = trimmed[1:]
		}
	}
	return trimmed
}

// HasFilePathPrefix reports whether the filesystem path s
// begins with the elements in prefix.
//
// HasFilePathPrefix is case-sensitive (except for volume names) even if the
// filesystem is not, does not apply Unicode normalization even if the
// filesystem does, and assumes that all path separators are canonicalized to
// filepath.Separator (as returned by filepath.Clean).
func strHasFilePathPrefix(s, prefix string) bool {
	sv := filepath.VolumeName(s)
	pv := filepath.VolumeName(prefix)

	// Strip the volume from both paths before canonicalizing sv and pv:
	// it's unlikely that strings.ToUpper will change the length of the string,
	// but doesn't seem impossible.
	s = s[len(sv):]
	prefix = prefix[len(pv):]

	// Always treat Windows volume names as case-insensitive, even though
	// we don't treat the rest of the path as such.
	//
	// TODO(bcmills): Why do we care about case only for the volume name? It's
	// been this way since https://go.dev/cl/11316, but I don't understand why
	// that problem doesn't apply to case differences in the entire path.
	if sv != pv {
		sv = strings.ToUpper(sv)
		pv = strings.ToUpper(pv)
	}

	switch {
	default:
		return false
	case sv != pv:
		return false
	case len(s) == len(prefix):
		return s == prefix
	case prefix == "":
		return true
	case len(s) > len(prefix):
		if prefix[len(prefix)-1] == filepath.Separator {
			return strings.HasPrefix(s, prefix)
		}
		return s[len(prefix)] == filepath.Separator && s[:len(prefix)] == prefix
	}
}

// Walk walks the file tree rooted at root, calling walkFn for each file or
// directory in the tree, including root.
func fsysWalk(root string, walkFn filepath.WalkFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		err = walkFn(root, nil, err)
	} else {
		err = fsyswalk(root, info, walkFn)
	}
	if err == filepath.SkipDir {
		return nil
	}
	return err
}

// walk recursively descends path, calling walkFn. Copied, with some
// modifications from path/filepath.walk.
func fsyswalk(path string, info fs.FileInfo, walkFn filepath.WalkFunc) error {
	if err := walkFn(path, info, nil); err != nil || !info.IsDir() {
		return err
	}

	fis, err := os.ReadDir(path)
	if err != nil {
		return walkFn(path, info, err)
	}

	for _, fi := range fis {
		info, err := fi.Info()
		if err != nil {
			return err
		}
		filename := filepath.Join(path, fi.Name())
		if err := fsyswalk(filename, info, walkFn); err != nil {
			if !fi.IsDir() || err != filepath.SkipDir {
				return err
			}
		}
	}
	return nil
}
