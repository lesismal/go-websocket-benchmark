package taskpool

import (
	"os"
	"regexp"
	"testing"
)

// The uwebsockets server is C++ and runs none of these pools, so it reads
// -taskpool for what the name says about where a server answers from: Inline
// and Default install no pool, and every other mode hands the callback to a
// goroutine off the event loop, which its own thread pool stands in for. It
// exits on a name it does not know, which would take a whole framework out of
// a benchmark run, so the two lists have to stay in step: this holds them to
// it.
//
// See frameworks/uwebsockets/server.cpp (kPoolModes) and the Ordering section
// of the package doc.
func TestUwebsocketsServerKnowsEveryPoolName(t *testing.T) {
	const source = "../frameworks/uwebsockets/server.cpp"
	cpp, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("reading %s: %v", source, err)
	}

	// kPoolModes entries look like {"fib_adaptive", true, "..."}.
	entry := regexp.MustCompile(`\{"([a-z_]+)", (true|false),`)
	offLoop := map[string]bool{}
	for _, match := range entry.FindAllStringSubmatch(string(cpp), -1) {
		offLoop[match[1]] = match[2] == "true"
	}
	if len(offLoop) == 0 {
		t.Fatalf("found no kPoolModes entries in %s; has the table moved?", source)
	}

	for _, name := range Names() {
		mode, known := offLoop[name]
		if !known {
			t.Errorf("%s does not know -taskpool=%s and would exit on it", source, name)
			continue
		}
		// Inline answers on the goroutine that read the frame, and Default
		// installs nothing at all; either way the echo stays on the event
		// loop there. The rest run off it.
		if want := name != Inline && name != Default; mode != want {
			t.Errorf("%s reads -taskpool=%s as offLoop=%v, want %v", source, name, mode, want)
		}
	}
}
