package readiness

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
		case "/redirect":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	p := New()
	for path, wantReady := range map[string]bool{"/ok": true, "/redirect": true, "/down": false} {
		err := p.Probe(t.Context(), app.Probe{Kind: domain.ReadyHTTP, Value: srv.URL + path})
		if (err == nil) != wantReady {
			t.Errorf("%s: err = %v, want ready %v", path, err, wantReady)
		}
	}
}

func TestTCP(t *testing.T) {
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected address %v", l.Addr())
	}
	port := addr.Port
	p := New()
	if err := p.Probe(t.Context(), app.Probe{Kind: domain.ReadyDefault, Port: port}); err != nil {
		t.Fatalf("listening port: %v", err)
	}
	_ = l.Close()
	if err := p.Probe(t.Context(), app.Probe{Kind: domain.ReadyDefault, Port: port}); err == nil {
		t.Fatal("closed port reported ready")
	}
}

func TestCmdUsesDirAndEnv(t *testing.T) {
	dir := t.TempDir()
	p := New()
	probe := app.Probe{Kind: domain.ReadyCmd, Value: `test "$(pwd -P)" = "$WANT"`, Dir: dir}
	resolved, _ := filepath.EvalSymlinks(dir)
	probe.Env = []string{"WANT=" + resolved}
	if err := p.Probe(t.Context(), probe); err != nil {
		t.Fatal(err)
	}
	probe.Value = "exit 1"
	if err := p.Probe(t.Context(), probe); err == nil {
		t.Fatal("failing command reported ready")
	}
}

func TestOutputMatchesCurrentRun(t *testing.T) {
	p := New()
	probe := app.Probe{Kind: domain.ReadyOutput, Value: `listening on \d+`, Output: "booting"}
	if err := p.Probe(t.Context(), probe); err == nil {
		t.Fatal("matched before the line was printed")
	}
	probe.Output = "booting\nlistening on 8090"
	if err := p.Probe(t.Context(), probe); err != nil {
		t.Fatalf("output not matched: %v", err)
	}
}
