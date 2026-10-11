package system

import (
	"os"
	"slices"
	"testing"

	"github.com/hay-kot/svx/internal/app"
)

func TestParsePS(t *testing.T) {
	out := `
  101     1   101 -zsh
  102   101   102 mise run desktop:dev
  bad   row   x   skipped
  103   102   103
  104   102   102 wails3 dev -config ./build/config.yml
`
	want := []app.Process{
		{PID: 101, PPID: 1, PGID: 101, Command: "-zsh"},
		{PID: 102, PPID: 101, PGID: 102, Command: "mise run desktop:dev"},
		{PID: 104, PPID: 102, PGID: 102, Command: "wails3 dev -config ./build/config.yml"},
	}
	if got := parsePS(out); !slices.Equal(got, want) {
		t.Fatalf("parsePS = %+v, want %+v", got, want)
	}
}

func TestSnapshotFindsSelf(t *testing.T) {
	table, err := Processes{}.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(table, func(p app.Process) bool { return p.PID == os.Getpid() })
	if i < 0 {
		t.Fatal("snapshot lacks the test process")
	}
	if self := table[i]; self.PPID != os.Getppid() || self.Command == "" {
		t.Fatalf("self = %+v", self)
	}
}
