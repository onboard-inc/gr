package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func execProgram(path string, argv0 string, args []string) error {
	return syscall.Exec(path, append([]string{argv0}, args...), os.Environ())
}

func realMain() int {
	cli, ok := parseCLI()
	if !ok {
		return 2
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gr: can't run: %v\n", err)
		return 255
	}
	cacheDir, err = filepath.Abs(cacheDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gr: can't run: %v\n", err)
		return 255
	}

	absPackagePath, err := filepath.Abs(cli.packagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gr: can't find absolute path for package %q: %v\n", cli.packagePath, err)
		return 255
	}

	sum, err := checksum(cli.packagePath, cli.compilerFlags, cli.compilerEnv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gr: internal error: can't calculate checksum for package %q: %v\n", cli.packagePath, err)
		return 255
	}

	p := packageCacheFile(cacheDir, absPackagePath, sum)

	err = execProgram(p, filepath.Base(absPackagePath), cli.runArgs)
	if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "gr: failed to run program: %v\n", err)
		return 255
	}

	// The executable didn't exist. Let's build it and try to run again.

	updated, err := updateCache(cacheDir, absPackagePath, sum, cli.compilerFlags, cli.compilerEnv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gr: failed to build program: %v\n", err)
		return 255
	}

	if !updated {
		return 255
	}

	err = execProgram(p, filepath.Base(absPackagePath), cli.runArgs)
	fmt.Fprintf(os.Stderr, "gr: failed to run program: %v\n", err)
	return 255
}

func main() {
	os.Exit(realMain())
}
