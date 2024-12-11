package main

import (
	"flag"
	"fmt"
	"os"
)

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "Usage: gr [go build opts] <pkg> [arguments]:")
	flag.PrintDefaults()
}

type unsupportedFlagT struct{}

func (unsupportedFlagT) String() string {
	return ""
}

func (unsupportedFlagT) Set(string) error {
	return fmt.Errorf("this compilation flag is not (yet) supported by gr")
}

var unsupportedFlag unsupportedFlagT

type boolFlag struct {
	Flag  string
	Value bool
}

type stringFlag struct {
	Flag  string
	Value string
}

type parsedCLI struct {
	compilerFlags []string
	compilerEnv   map[string]string

	packagePath string
	runArgs     []string
	debug       bool
}

func parseCLI() (parsedCLI, bool) {
	boolFlags := []*boolFlag{
		{Flag: "race"},
		{Flag: "msan"},
		{Flag: "asan"},
		{Flag: "cover"},
		{Flag: "v"},
		{Flag: "work"},
		{Flag: "x"},
	}
	stringFlags := []*stringFlag{
		{Flag: "covermode"},
		{Flag: "coverpkg"},
		{Flag: "asmflags"},
		{Flag: "gcflags"},
		{Flag: "ldflags"},
	}

	// These options are either useless for 'go run' replacement, or not trivial to implement.
	// Instead of producing silent hard-to-debug mistakes, reject them.
	for _, f := range []string{
		"a", "C", "n", "p", "buildmode", "buildvcs", "compiler", "gccgoflags", "installsuffix", "linkshared",
		"mod", "modcacherw", "modfile", "overlay", "pgo", "pkgdir", "tags", "trimpath", "toolexec",
	} {
		flag.Var(unsupportedFlag, f, "(not yet) supported")
	}

	for _, f := range boolFlags {
		flag.BoolVar(&f.Value, f.Flag, false, "as in 'go build'")
	}
	for _, f := range stringFlags {
		flag.StringVar(&f.Value, f.Flag, "", "as in 'go build'")
	}

	var debug bool
	flag.BoolVar(&debug, "debug", false, "enable debug output")

	flag.Usage = usage
	flag.Parse()

	if flag.NArg() == 0 {
		usage()
		return parsedCLI{}, false
	}

	out := parsedCLI{
		packagePath: flag.Arg(0),
		runArgs:     flag.Args()[1:],
		debug:       debug,
		compilerEnv: map[string]string{},
	}
	for _, f := range boolFlags {
		if f.Value {
			out.compilerFlags = append(out.compilerFlags, "-"+f.Flag)
		}
	}
	for _, f := range stringFlags {
		if f.Value != "" {
			out.compilerFlags = append(out.compilerFlags, "-"+f.Flag, f.Value)
		}
	}

	// These variables influence the compiler, so they should influence the cache key too
	for _, env := range []string{
		"AR",
		"CC",
		"CGO_CFLAGS",
		"CGO_CPPFLAGS",
		"CGO_CXXFLAGS",
		"CGO_ENABLED",
		"CGO_FFLAGS",
		"CGO_LDFLAGS",
		"CXX",
		"GCCGO",
		"GO111MODULE",
		"GOARCH",
		"GOARM64",
		"GODEBUG",
		"GOEXE",
		"GOEXPERIMENT",
		"GOFLAGS",
		"GOHOSTARCH",
		"GOHOSTOS",
		"GOMOD",
		"GOOS",
		"GOPATH",
		"GOROOT",
		"GOTOOLCHAIN",
		"GOTOOLDIR",
		"GOVERSION",
	} {
		if val, exists := os.LookupEnv(env); exists {
			out.compilerEnv[env] = val
		}
	}

	return out, true
}
