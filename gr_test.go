package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/dottedmag/must"
)

type sut struct {
	dir         string
	exe         string
	coverageDir string
}

func mustBuildSUT(t *testing.T) sut {
	dir := t.TempDir()
	sut := sut{
		dir:         dir,
		exe:         filepath.Join(dir, "exe"),
		coverageDir: filepath.Join(dir, "coverage"),
	}

	compileCmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", sut.exe)
	if testing.CoverMode() != "" {
		compileCmd.Args = append(compileCmd.Args, "-cover")
		must.OK(os.MkdirAll(sut.coverageDir, 0o755))
	}
	compileCmd.Args = append(compileCmd.Args, ".")
	compileCmd.Stdout = os.Stdout
	compileCmd.Stderr = os.Stderr
	must.OK(compileCmd.Run())
	return sut
}

func (sut sut) done() {
	if testing.CoverMode() != "" {
		must.OK(os.MkdirAll(sut.coverageDir+"-all", 0o755))
		mergeCoverageCmd := exec.Command("go", "tool", "covdata", "merge", "-i="+sut.coverageDir, "-o="+sut.coverageDir+"-all")
		mergeCoverageCmd.Stdout = os.Stdout
		mergeCoverageCmd.Stderr = os.Stderr
		must.OK(mergeCoverageCmd.Run())

		convertCoverageCmd := exec.Command("go", "tool", "covdata", "textfmt", "-i="+sut.coverageDir+"-all", "-o=coverage.txt")
		convertCoverageCmd.Stdout = os.Stdout
		convertCoverageCmd.Stderr = os.Stderr
		must.OK(convertCoverageCmd.Run())
	}
}

func (sut sut) run(t *testing.T, args []string, env []string) (retStdout string, retStderr string, retExitCode int, _ error) {
	exe := filepath.Join(sut.dir, "exe")

	runCmd := exec.Command(exe, args...)
	runCmd.Env = append(os.Environ(), "HOME="+sut.dir) // Make sure every test case gets a separate cache
	runCmd.Env = append(runCmd.Env, env...)
	if testing.CoverMode() != "" {
		coverageDir := filepath.Join(sut.dir, "coverage")
		must.OK(os.MkdirAll(coverageDir, 0o755))
		runCmd.Env = append(runCmd.Env, "GOCOVERDIR="+coverageDir)
	}

	// Make sure module cache does not affect the output
	modCacheDir := t.TempDir()
	runCmd.Env = append(runCmd.Env, "GOMODCACHE="+modCacheDir)
	// Cache directories are created with mode 555, so their permissions need to be adjusted before cleanup
	defer func() {
		must.OK(filepath.WalkDir(modCacheDir, func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return err //nolint:revive // Bug https://github.com/mgechev/revive/issues/1029, remove when golangci-lint picks up new version of revive
			}
			if de.IsDir() {
				must.OK(os.Chmod(path, 0o755))
			}
			return nil //nolint:revive // Bug https://github.com/mgechev/revive/issues/1029, remove when golangci-lint picks up new version of revive
		}))
	}()

	stdoutPipe := must.OK1(runCmd.StdoutPipe())
	stderrPipe := must.OK1(runCmd.StderrPipe())

	must.OK(runCmd.Start())

	stdout := string(must.OK1(io.ReadAll(stdoutPipe)))
	stderr := string(must.OK1(io.ReadAll(stderrPipe)))

	if err := runCmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout, stderr, exitErr.ExitCode(), nil
		}
		t.Fatalf("Failed to run command: %v", err)
	}

	return stdout, stderr, 0, nil
}

var anything = regexp.MustCompile(``)

type cliTestCase struct {
	args []string
	env  []string

	exitCode int

	stdout   string
	stdoutRx *regexp.Regexp // has priority over stdout

	stderr   string
	stderrRx *regexp.Regexp // has priority over stderr
}

func cliTestCaseName(tc cliTestCase) string {
	s := "gr " + strings.Join(tc.args, " ")
	if len(tc.env) > 0 {
		s = strings.Join(tc.env, " ") + " " + s
	}
	return s
}

func TestCLI(t *testing.T) {
	sut := mustBuildSUT(t)
	defer sut.done()

	for _, tc := range []cliTestCase{
		{exitCode: 2, stderrRx: anything}, // no args -> usage
		{args: []string{"./testdata/basic"}, stdout: "Hello world!\n"},
		{args: []string{"./testdata/basic"}, stdout: "Hello world!\n"}, // run twice
		{args: []string{"./testdata/ext"}, stdout: "Hello world!\n", stderr: "go: downloading github.com/dottedmag/must v1.0.0\n"},
		{args: []string{"./testdata/exit3"}, exitCode: 3},

		// Run even if required module is erroneously marked as indirect
		{args: []string{"./testdata/wrong-module-indirect"}, stdout: "Hello world!\n", stderr: "go: downloading golang.org/x/crypto v0.27.0\n"},

		// Compilation failures
		{args: []string{"./testdata/syntax-error"}, exitCode: 255, stderrRx: regexp.MustCompile(`undefined: fmt\.Printz`)},
		// Weird things
		{args: []string{"./testdata/basic"}, env: []string{"HOME="}, exitCode: 255, stderrRx: regexp.MustCompile(`gr: can't run:`)},
	} {
		t.Run(cliTestCaseName(tc), func(t *testing.T) {
			stdout, stderr, exitCode := must.OK3(sut.run(t, tc.args, tc.env))

			if tc.exitCode != exitCode {
				t.Errorf("Expected exit code %d, got %d", tc.exitCode, exitCode)
			}

			if tc.stdoutRx != nil {
				if !tc.stdoutRx.MatchString(stdout) {
					t.Fatalf("failed to match %#q regexp against %q", tc.stdoutRx, stdout)
				}
			} else {
				assert.Equal(t, tc.stdout, stdout)
			}

			if tc.stderrRx != nil {
				if !tc.stderrRx.MatchString(stderr) {
					t.Fatalf("failed to match %#q regexp against %q", tc.stderrRx, stderr)
				}
			} else {
				assert.Equal(t, tc.stderr, stderr)
			}
		})
	}
}
