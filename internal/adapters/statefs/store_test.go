package statefs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hay-kot/svx/internal/domain"
)

func sampleStack() domain.Stack {
	return domain.Stack{
		ID:        domain.Identity{Project: "shop", Stack: "amber-finch"},
		Root:      "/src/shop",
		Dir:       "/src/shop/.svx/amber-finch",
		Slot:      1,
		Session:   "svx-shop-amber-finch",
		Status:    domain.StackUp,
		StartedAt: time.Date(2026, 10, 10, 15, 4, 5, 0, time.UTC),
		Settings:  domain.Settings{Logs: true, MenuBar: true},
		Env:       map[string]string{"SVX_STACK": "amber-finch"},
		Snapshot:  map[string]string{"SECRET": "hunter2"},
		Services: []domain.Service{
			{
				Name: "web", Ports: map[string]int{"main": 5183, "hmr": 24688}, URL: "http://127.0.0.1:5183",
				Links: []domain.Link{{Label: "App", URL: "http://127.0.0.1:5183/"}},
				Run:   "npm run dev", Dir: "/src/shop/web", Env: map[string]string{"PORT": "5183"},
				Depends: []string{"api"}, Ready: domain.ReadySpec{Kind: domain.ReadyHTTP, Value: "http://x", Timeout: time.Second},
				Autostart: true, Stop: domain.StopSpec{Signal: "SIGTERM", Timeout: 10 * time.Second}, LogFile: "/l/web.log",
			},
			{Name: "api", Ports: map[string]int{}, Links: []domain.Link{}, Env: map[string]string{}, Autostart: true},
			{Name: "migrate", Ports: map[string]int{}, Links: []domain.Link{}, Env: map[string]string{}, Autostart: true, Oneshot: true},
		},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := sampleStack()
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.Load(want.ID)
	if err != nil || !found {
		t.Fatalf("Load = %v, %v", found, err)
	}
	want.Settings.PortStride, want.Settings.MaxSlots = 0, 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, want)
	}
}

func TestPublicRecordShapeAndModes(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	if err := s.Save(sampleStack()); err != nil {
		t.Fatal(err)
	}

	pub := filepath.Join(dir, "stacks", "shop--amber-finch.json")
	priv := filepath.Join(dir, "stacks", "shop--amber-finch.env.json")
	if info, _ := os.Stat(priv); info.Mode().Perm() != 0o600 {
		t.Fatalf("private file mode = %v", info.Mode().Perm())
	}

	data, _ := os.ReadFile(pub) //nolint:gosec // test temp file
	var rec map[string]any
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "project", "stack", "root", "session", "slot", "status", "started_at", "services"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("public record lacks %q", key)
		}
	}
	if rec["session"] != "svx-shop-amber-finch" || rec["started_at"] != "2026-10-10T15:04:05Z" {
		t.Errorf("record = %v", rec)
	}
	var shape struct {
		Services map[string]struct {
			Ports map[string]int `json:"ports"`
		} `json:"services"`
	}
	if err := json.Unmarshal(data, &shape); err != nil {
		t.Fatal(err)
	}
	if shape.Services["web"].Ports["hmr"] != 24688 {
		t.Errorf("services = %+v", shape.Services)
	}
	if strings.Contains(string(data), "hunter2") {
		t.Error("public record leaks the env snapshot")
	}
}

func TestListAndSetStatus(t *testing.T) {
	s, _ := New(t.TempDir())
	a := sampleStack()
	b := sampleStack()
	b.ID.Stack = "quiet-otter"
	for _, st := range []domain.Stack{b, a} {
		if err := s.Save(st); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID.Stack != "amber-finch" || list[1].ID.Stack != "quiet-otter" {
		t.Fatalf("List = %+v", list)
	}
	if list[0].Snapshot != nil || list[0].Services[0].Run != "" {
		t.Fatal("List should not carry runtime fields")
	}
	if list[0].Services[0].Name != "web" || list[0].Services[1].Name != "api" {
		t.Fatal("List lost service order")
	}
	if list[0].Services[0].Oneshot || !list[0].Services[2].Oneshot {
		t.Fatal("List should carry oneshot, so readers can tell a finished oneshot from a crash")
	}

	if err := s.SetStatus(a.ID, domain.StackDown); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Load(a.ID)
	if got.Status != domain.StackDown || got.Slot != 1 || got.Snapshot["SECRET"] != "hunter2" {
		t.Fatalf("after SetStatus: %+v", got)
	}
}

func TestLockExcludes(t *testing.T) {
	s, _ := New(t.TempDir())
	unlock, err := s.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	if _, err := s.Lock(ctx); err == nil {
		t.Fatal("second lock should wait and time out")
	}
	unlock()
	unlock2, err := s.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	unlock2()
}
