package commands

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hay-kot/svx/internal/domain"
)

func TestTableAlignsColouredCells(t *testing.T) {
	st := style{on: true}
	var b strings.Builder
	tw := table(&b)
	_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", st.dim("SERVICE"), st.dim("STATE"), st.dim("PORTS"))
	_, _ = fmt.Fprintf(tw, "web\t%s\t8010\n", st.green("running"))
	_, _ = fmt.Fprintf(tw, "migrate\t%s\t-\n", st.dim("done"))
	if err := tw.Flush(); err != nil {
		t.Fatal(err)
	}

	want := "SERVICE  STATE    PORTS\n" +
		"web      running  8010\n" +
		"migrate  done     -\n"
	got := domain.StripANSI(b.String())
	if got != want {
		t.Fatalf("table =\n%s\nwant\n%s", got, want)
	}
}
