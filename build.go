package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

func packageCacheFile(userCacheDir, absPackagePath, checksum string) string {
	return filepath.Join(packageCacheDir(userCacheDir, absPackagePath), checksum)
}

func packageCacheDir(userCacheDir, absPackagePath string) string {
	return filepath.Join(userCacheDir, "gr", "exe", absPackagePath)
}

const keepCacheEntriesOnCleanup = 2

// This function should be called with a package lock held
func cacheCleanup(packageCachePath string) error {
	//
	// While it might be argued that cleaning up cache should not fail, ignoring errors may cause the cache
	// to fill up, and cause problems with disk space, especially in CI.
	//
	// So this function does not ignore any filesystem errors. However it tolerates any perceived inconsistencies,
	// as filesystem is a shared resource.
	//

	des, err := os.ReadDir(packageCachePath)
	if err != nil {
		if os.IsNotExist(err) { // No cache dir -> no cleanup needed
			return nil
		}
		return fmt.Errorf("failed to clean entries from cache: %w", err)
	}

	type cacheEntry struct {
		fileName string
		mtime    time.Time
	}
	var cacheContents []cacheEntry

	for _, de := range des {
		fi, err := de.Info()
		if err != nil {
			if os.IsNotExist(err) {
				// File might have been deleted manually in meantime
				continue
			}
			return fmt.Errorf("failed to clean old entries from cache: failed to read entry %q: %w", de.Name(), err)
		}
		cacheContents = append(cacheContents, cacheEntry{
			fileName: fi.Name(),
			mtime:    fi.ModTime(),
		})
	}

	sort.Slice(cacheContents, func(i, j int) bool {
		return cacheContents[i].mtime.Before(cacheContents[j].mtime)
	})

	for i := range len(cacheContents) - keepCacheEntriesOnCleanup {
		err := os.Remove(filepath.Join(packageCachePath, cacheContents[i].fileName))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("failed to clean old entries from cache: failed to remove entry %q: %w", cacheContents[i].fileName, err)
		}
	}

	return nil
}

// This function expects
// - output path to be absolute
// - paths in compiler flags/env, if any, to be absolute
// so that it can cd to the package directory and build from there.
//
// Otherwise cross-module tool running is not going to work.
func build(packagePath string, absOutputPath string, compilerFlags []string, compilerEnv map[string]string) bool {
	goBin := "go"
	if bin, found := os.LookupEnv("GO"); found {
		goBin = bin
	}

	compileCmd := exec.Command(goBin, "build", "-trimpath", "-buildvcs=false", "-o", absOutputPath)
	compileCmd.Args = append(compileCmd.Args, compilerFlags...)
	compileCmd.Dir = packagePath
	if len(compilerEnv) > 0 {
		compileCmd.Env = os.Environ()
		for k, v := range compilerEnv {
			compileCmd.Env = append(compileCmd.Env, k+"="+v)
		}
	}
	compileCmd.Stdout = os.Stdout
	compileCmd.Stderr = os.Stderr // TODO (dottedmag): It would be nice to add color to this output

	// Instead of an error we return boolean: compiler diagnostics on stderr is good enough, no need to
	// clutter the output wit error messages
	return compileCmd.Run() == nil
}

func openPackageCacheDir(absPackageCacheDir string) (*os.File, error) {
	fh, err := os.Open(absPackageCacheDir)
	if err == nil {
		return fh, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to open cache dir %q: %w", absPackageCacheDir, err)
	}

	// The cache directory does not exist yet.

	err = os.MkdirAll(absPackageCacheDir, 0o755)
	// Ignore "already exists" error, it means another instance of 'gr' has just created it
	if err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("failed to create cache dir %q: %w", absPackageCacheDir, err)
	}

	// Now we know that the directory exists.
	fh, err = os.Open(absPackageCacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open cache dir %q %w", absPackageCacheDir, err)
	}
	return fh, nil
}

// This function is only called if optimistic exec() failed, so it's not on a fast path
func updateCache(userCacheDir, absPackagePath, sourceChecksum string, compilerFlags []string, compilerEnv map[string]string) (retUpdated bool, _ error) {
	// Lock the package directory
	p := packageCacheDir(userCacheDir, absPackagePath)

	fh, err := openPackageCacheDir(p)
	if err != nil {
		return false, fmt.Errorf("failed to update exe cache for %q: %w", absPackagePath, err)
	}
	defer fh.Close()

	if err := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX); err != nil {
		return false, fmt.Errorf("failed to update exe cache for %q: %w", absPackagePath, err)
	}
	// There is no need to explicitly remove lock, closing file descriptor in the 'defer' above removes it.

	if err := cacheCleanup(p); err != nil {
		return false, fmt.Errorf("failed to update exe cache for %q: %w", absPackagePath, err)
	}

	return build(absPackagePath, packageCacheFile(userCacheDir, absPackagePath, sourceChecksum), compilerFlags, compilerEnv), nil
}
