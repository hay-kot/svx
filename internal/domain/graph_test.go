package domain

import (
	"reflect"
	"strings"
	"testing"
)

func svcs(pairs ...[]string) []ServiceDef {
	var out []ServiceDef
	for _, p := range pairs {
		out = append(out, ServiceDef{Name: p[0], Depends: p[1:], Autostart: true})
	}
	return out
}

func TestLevels(t *testing.T) {
	got, err := Levels(svcs(
		[]string{"web", "api"},
		[]string{redisSvc},
		[]string{"api", "migrate"},
		[]string{"migrate", redisSvc},
		[]string{"worker", redisSvc},
	))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{redisSvc}, {"migrate", "worker"}, {"api"}, {"web"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Levels = %v, want %v", got, want)
	}
}

func TestLevelsErrors(t *testing.T) {
	_, err := Levels(svcs([]string{"a", "b"}, []string{"b", "c"}, []string{"c", "a"}))
	if err == nil || !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Fatalf("cycle error = %v", err)
	}
	_, err = Levels(svcs([]string{"a", "ghost"}))
	if err == nil || !strings.Contains(err.Error(), `unknown service "ghost"`) {
		t.Fatalf("unknown dep error = %v", err)
	}
}

func TestTargets(t *testing.T) {
	services := svcs([]string{redisSvc}, []string{"api", redisSvc}, []string{"worker", redisSvc})
	services[2].Autostart = false

	got, err := Targets(services, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got[redisSvc] || !got["api"] || got["worker"] {
		t.Fatalf("default targets = %v", got)
	}

	got, err = Targets(services, []string{"worker"})
	if err != nil {
		t.Fatal(err)
	}
	if !got["worker"] || !got[redisSvc] || got["api"] {
		t.Fatalf("named targets = %v", got)
	}

	if _, err := Targets(services, []string{"nope"}); err == nil {
		t.Fatal("expected error for unknown service")
	}
}
