package tmux

import (
	"reflect"
	"testing"

	"github.com/hay-kot/svx/internal/app"
)

func TestParseWindows(t *testing.T) {
	out := "@1|shop--a|web|123|0|\n" +
		"@2|shop--a|api|456|1|3\n" +
		"@3|shop--b|web|789|0|\n" + // another stack
		"@4|shop--a||790|0|\n" + // session-level tag only: the user's window
		"@5|||791|0|\n\n"
	want := []app.Window{
		{ID: "@1", Name: "web", PanePID: 123, Dead: false, ExitCode: 0},
		{ID: "@2", Name: "api", PanePID: 456, Dead: true, ExitCode: 3},
	}
	if got := parseWindows(out, "shop--a"); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWindows = %+v", got)
	}
}

func TestPipeCommandQuotes(t *testing.T) {
	if got := pipeCommand("/tmp/it's here.log"); got != `cat >> '/tmp/it'\''s here.log'` {
		t.Fatalf("pipeCommand = %s", got)
	}
}
