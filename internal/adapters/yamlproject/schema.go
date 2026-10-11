package yamlproject

import (
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// fileConfig mirrors svx.yaml. Pointers mark keys whose absence means "use
// the default" rather than the zero value.
type fileConfig struct {
	Project  string                 `yaml:"project"`
	Settings fileSettings           `yaml:"settings"`
	Vars     map[string]string      `yaml:"vars"`
	Env      map[string]string      `yaml:"env"`
	Services map[string]fileService `yaml:"services"`
}

type fileSettings struct {
	PortStride *int  `yaml:"port_stride"`
	MaxSlots   *int  `yaml:"max_slots"`
	Logs       *bool `yaml:"logs"`
	MenuBar    *bool `yaml:"menubar"`
	Retries    *int  `yaml:"retries"`
}

type fileService struct {
	Run         string               `yaml:"run"`
	Dir         string               `yaml:"dir"`
	Port        *portValue           `yaml:"port"`
	Ports       map[string]portValue `yaml:"ports"`
	Env         map[string]string    `yaml:"env"`
	Depends     []string             `yaml:"depends"`
	Ready       *fileReady           `yaml:"ready"`
	Oneshot     bool                 `yaml:"oneshot"`
	Links       map[string]string    `yaml:"links"`
	Autostart   *bool                `yaml:"autostart"`
	Retries     *int                 `yaml:"retries"`
	StopSignal  string               `yaml:"stop_signal"`
	StopTimeout string               `yaml:"stop_timeout"`
}

type fileReady struct {
	HTTP    string `yaml:"http"`
	Cmd     string `yaml:"cmd"`
	Output  string `yaml:"output"`
	Delay   string `yaml:"delay"`
	Timeout string `yaml:"timeout"`
}

// portValue is a port number or the string "auto".
type portValue struct {
	Auto bool
	Base int
}

func (p *portValue) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: a port is a number or \"auto\"", node.Line)
	}
	if node.Value == "auto" {
		p.Auto = true
		return nil
	}
	n, err := strconv.Atoi(node.Value)
	if err != nil {
		return fmt.Errorf("line %d: a port is a number or \"auto\", got %q", node.Line, node.Value)
	}
	p.Base = n
	return nil
}
