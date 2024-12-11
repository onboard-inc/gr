package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
)

//
// Calculate a checksum of source code of a given package and its dependencies.
//
// Walk source code tree, reading relevant source code, looking up imports,
// and collecting all the sources the package depends on.
//
// Checksums are used as caching keys, so they do not need to be perfect: returning random
// value every time would be correct, if inefficient; the only meaningful failure mode is
// returning identical values for different source code.
//

//
// This code does not use `go list` as it is insanely slow.
//

type moduleInfo struct {
	path     string
	packages map[string]string // remote imports are marked by empty strings
}

type parseContext struct {
	// Parsing populates this field with the checksums of files that comprise source code
	checksums map[string]string

	packages map[string]bool
	modules  map[string]*moduleInfo
}

func addChecksum(pc *parseContext, filename string) error {
	if _, exists := pc.checksums[filename]; exists {
		panic(fmt.Errorf("internal error: a checksum has been requested twice for file %q", filename))
	}

	fh, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer fh.Close()

	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return err
	}

	pc.checksums[filename] = hex.EncodeToString(h.Sum(nil))
	return nil
}

func findModule(pc *parseContext, dir string) (*moduleInfo, error) {
	origDir := dir

	var info *moduleInfo
	var uncachedDirs []string
	for {
		// Check the cache first
		if info = pc.modules[dir]; info != nil {
			break
		}

		// The module information is not found in the cache.
		// Climb the directories until we have found a go.mod or a cached value.
		// Store the result in the cache for all directories traversed.

		uncachedDirs = append(uncachedDirs, dir)

		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read %s/go.mod: %w", dir, err)
		}

		if err == nil {
			// There is go.mod. Continue to parsing and filling in the cache
			if info, err = parseModule(pc, dir); err != nil { // NB: assigns to 'info' declared outside of the loop
				return nil, fmt.Errorf("failed to parse enclosing go.mod for directory %q: %w", origDir, err)
			}
			break
		}

		// There is no go.mod. We continue our ascent.
		dir = filepath.Dir(dir)
		if dir == "/" {
			return nil, fmt.Errorf("failed to find go.mod anywhere upwards of %q", origDir)
		}
	}

	for _, d := range uncachedDirs {
		pc.modules[d] = info
	}
	return info, nil
}

func parseModule(pc *parseContext, dir string) (*moduleInfo, error) {
	goModFileName := filepath.Join(dir, "go.mod")
	contents, err := os.ReadFile(goModFileName)
	if err != nil {
		return nil, fmt.Errorf("failed to parse module: %w", err)
	}

	f, err := modfile.Parse(goModFileName, contents, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to parse module: %w", err)
	}

	out := &moduleInfo{
		path: f.Module.Mod.Path,
		packages: map[string]string{
			f.Module.Mod.Path: dir,
		},
	}

	for _, r := range f.Require {
		out.packages[r.Mod.Path] = ""
	}

	for _, r := range f.Replace {
		if r.New.Version != "" {
			continue
		}

		// It's a local directory replacement
		if strings.HasPrefix(r.New.Path, "/") {
			out.packages[stripPackageQuotes(r.Old.Path)] = r.New.Path
		} else {
			out.packages[stripPackageQuotes(r.Old.Path)] = filepath.Join(dir, r.New.Path)
		}
	}

	if err := addChecksum(pc, filepath.Join(dir, "go.mod")); err != nil {
		return nil, err
	}
	if err := addChecksum(pc, filepath.Join(dir, "go.sum")); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return out, nil
}

func stripPackageQuotes(p string) string {
	return strings.TrimPrefix(strings.TrimSuffix(p, `"`), `"`)
}

// Go compiler cares about .go
// CGo cares about the rest of the extensions: https://pkg.go.dev/cmd/cgo
var srcRE = regexp.MustCompile(`\.(go|s|S|c|cc|cpp|cxx|m|h|hh|hpp|hxx|f|F|for|f90)$`)

func packageFile(name string) bool {
	// We don't care about tests
	if strings.HasSuffix(name, "_test.go") {
		return false
	}

	// Ignored files
	if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
		return false
	}

	return srcRE.MatchString(name)
}

var stdlibPackageRE = regexp.MustCompile(`^\"[a-z]+(/|")`)

func parsePackage(pc *parseContext, dir string) error {
	if pc.packages[dir] { // Don't parse the same package twice
		return nil
	}
	pc.packages[dir] = true

	// Make sure module for all packages are resolved, otherwise go.mod/go.sum may not
	// be included in checksum calculation for packages that only uses stdlib.
	if _, err := findModule(pc, dir); err != nil {
		return err
	}

	var embedPatterns []string

	fset := token.NewFileSet()

	des, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, de := range des {
		if de.Type() != 0 { // Not a regular file
			continue
		}

		if !packageFile(de.Name()) {
			continue
		}

		if err := addChecksum(pc, filepath.Join(dir, de.Name())); err != nil {
			return err
		}

		if strings.HasSuffix(de.Name(), ".go") { // Only .go files may contain imports
			node, err := parser.ParseFile(fset, filepath.Join(dir, de.Name()), nil, parser.SkipObjectResolution|parser.ParseComments)
			if err != nil {
				return fmt.Errorf("failed to parse %s/%s: %w", dir, de.Name(), err)
			}

			for _, imp := range node.Imports {
				if stdlibPackageRE.MatchString(imp.Path.Value) {
					continue
				}
				dir, local, err := resolveImport(pc, dir, stripPackageQuotes(imp.Path.Value))
				if err != nil {
					return err
				}

				// Checksumming of non-local imports is done by checksumming go.mod/go.sum
				if !local {
					continue
				}

				if err := parsePackage(pc, dir); err != nil {
					return err
				}
			}

			for _, commentGroup := range node.Comments {
				for _, comment := range commentGroup.List {
					if s, found := strings.CutPrefix(comment.Text, "//go:embed "); found {
						patterns, err := parseGoEmbed(s)
						if err != nil {
							return fmt.Errorf("failed to parse //go:embed comment in %q: %w", comment.Text, err)
						}
						embedPatterns = append(embedPatterns, patterns...)
					}
				}
			}
		}
	}

	files, _, err := resolveEmbed(dir, embedPatterns)
	if err != nil {
		return fmt.Errorf("failed to resolve //go:embed patterns: %w", err)
	}
	for _, f := range slices.Sorted(slices.Values(files)) {
		if err := addChecksum(pc, filepath.Join(dir, f)); err != nil {
			return err
		}
	}
	return nil
}

func packageInsideOf(path, base string) bool {
	return base == path || strings.HasPrefix(path, base+"/")
}

func dirForPackageInModule(modulePath string, moduleDir string, importPath string) string {
	if modulePath == importPath {
		return moduleDir
	}
	return filepath.Join(moduleDir, strings.TrimPrefix(importPath, modulePath+"/"))
}

func resolveImport(pc *parseContext, dir string, importPath string) (retDir string, retLocal bool, _ error) {
	for {
		moduleInfo, err := findModule(pc, dir)
		if err != nil {
			return "", false, err
		}

		var longestMatchedPath string
		var longestMatchedPathDir string

		for modulePackagePath, modulePackageDir := range moduleInfo.packages {
			if packageInsideOf(importPath, modulePackagePath) && len(modulePackagePath) > len(longestMatchedPath) {
				longestMatchedPath = modulePackagePath
				longestMatchedPathDir = modulePackageDir
			}
		}

		if longestMatchedPath == "" {
			return "", false, fmt.Errorf("package %q is outside of every module", importPath)
		}

		// If the longest match is a remote import then we're done here, no need to parse its source code
		if longestMatchedPathDir == "" {
			return "", false, nil
		}

		// If the longest match is in current module then we're also done
		if longestMatchedPath == moduleInfo.path {
			return dirForPackageInModule(longestMatchedPath, longestMatchedPathDir, importPath), true, nil
		}

		// The princess is in another^W^W^W^W package is in another module
		dir = longestMatchedPathDir
	}
}

func packageSourceChecksums(dir string) (map[string]string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	pc := &parseContext{
		checksums: map[string]string{},
		packages:  map[string]bool{},
		modules:   map[string]*moduleInfo{},
	}
	if err := parsePackage(pc, absDir); err != nil {
		return nil, fmt.Errorf("failed to calculate checksum for %q: %w", dir, err)
	}

	return pc.checksums, nil
}

func checksum(dir string, compilerFlags []string, compilerEnv map[string]string) (string, error) {
	filesChecksums, err := packageSourceChecksums(dir)
	if err != nil {
		return "", err
	}

	// Poor man's canonicalization
	bytes, err := json.Marshal([]any{
		filesChecksums,
		compilerFlags,
		compilerEnv,
	})
	if err != nil {
		panic(fmt.Errorf("internal error: checksum information is not marshalable: %w", err))
	}

	h := sha256.New()
	if _, err := h.Write(bytes); err != nil {
		panic(fmt.Errorf("internal error: sha256.New().Write failed: %w", err))
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
