package domain

import (
	"strings"
	"testing"
)

func TestValidateOK(t *testing.T) {
	warnings, err := Validate(sampleDef())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Definition)
		want string
	}{
		{"bad name", func(d *Definition) { d.Services[0].Name = "Redis.1" }, "service names may only use"},
		{"no run", func(d *Definition) { d.Services[0].Run = " " }, "services.redis.run: is required"},
		{"dup base", func(d *Definition) { d.Services[1].Port = FixedPort(6379) }, "already declared"},
		{"reserved main", func(d *Definition) { d.Services[1].Ports["main"] = FixedPort(1) }, "reserved"},
		{"dup link", func(d *Definition) { d.Services[0].Links = []LinkDef{{Label: "App", Target: "/"}} }, `link label "App" is already used`},
		{"bad template", func(d *Definition) { d.Services[0].Run = "{{ .port" }, "template:"},
		{"cycle", func(d *Definition) {
			d.Services[0].Depends = []string{"web"}
			d.Services[1].Depends = []string{redisSvc}
		}, "dependency cycle"},
		{"oneshot with ready", func(d *Definition) { d.Services[1].Oneshot = true }, "oneshot"},
		{"bad signal", func(d *Definition) { d.Services[0].Stop.Signal = "SIGNOPE" }, "unknown signal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := sampleDef()
			tt.edit(&def)
			_, err := Validate(def)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want mention of %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), "svx.yaml") {
				t.Fatalf("error does not name the file: %v", err)
			}
		})
	}
}

func TestStrideOverlapWarning(t *testing.T) {
	tests := []struct {
		name  string
		other int
		want  int
	}{
		{name: "within slot range", other: 3010, want: 1},
		{name: "beyond slot range", other: 8000, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := sampleDef()
			def.Services[0].Port = FixedPort(3000)
			def.Services[1].Port = FixedPort(tt.other)
			warnings, err := Validate(def)
			if err != nil {
				t.Fatal(err)
			}
			if len(warnings) != tt.want {
				t.Fatalf("warnings = %v, want %d", warnings, tt.want)
			}
			if tt.want > 0 && !strings.Contains(warnings[0], "collide across slots") {
				t.Fatalf("warnings = %v", warnings)
			}
		})
	}
}

func TestSlotPorts(t *testing.T) {
	def := sampleDef()
	def.Services[0].Ports = map[string]PortSpec{"metrics": AutoPort()}
	table, autos, err := SlotPorts(def, 2)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := table.Get("web", MainPort); p != 5193 {
		t.Errorf("web = %d", p)
	}
	if p, _ := table.Get("web", "hmr"); p != 24698 {
		t.Errorf("hmr = %d", p)
	}
	if len(autos) != 1 || autos[0].String() != "redis.metrics" {
		t.Errorf("autos = %v", autos)
	}
	if _, _, err := SlotPorts(def, 5000); err == nil {
		t.Error("expected error above port 65535")
	}
}
