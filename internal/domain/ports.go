package domain

import (
	"fmt"
	"maps"
	"slices"
)

// MaxPort is the highest valid TCP port.
const MaxPort = 65535

// PortRef names one declared port of one service. Name is MainPort for the
// primary port.
type PortRef struct {
	Service string
	Name    string
}

func (r PortRef) String() string {
	if r.Name == MainPort {
		return r.Service
	}
	return r.Service + "." + r.Name
}

// PortTable maps service -> port name -> port number. The primary port is
// stored under MainPort.
type PortTable map[string]map[string]int

// Get returns a port and whether it exists.
func (t PortTable) Get(service, name string) (int, bool) {
	p, ok := t[service][name]
	return p, ok
}

// Set records a port.
func (t PortTable) Set(service, name string, port int) {
	if t[service] == nil {
		t[service] = map[string]int{}
	}
	t[service][name] = port
}

// All lists every port with its ref, sorted by service then name.
func (t PortTable) All() []PortAssignment {
	var out []PortAssignment
	for _, svc := range slices.Sorted(maps.Keys(t)) {
		for _, name := range slices.Sorted(maps.Keys(t[svc])) {
			out = append(out, PortAssignment{Ref: PortRef{Service: svc, Name: name}, Port: t[svc][name]})
		}
	}
	return out
}

// PortAssignment is one resolved port.
type PortAssignment struct {
	Ref  PortRef
	Port int
}

// DeclaredPorts lists every port a definition declares, in declaration order
// with named ports sorted after the primary.
func DeclaredPorts(def Definition) []DeclaredPort {
	var out []DeclaredPort
	for _, s := range def.Services {
		if s.Port.Set {
			out = append(out, DeclaredPort{Ref: PortRef{Service: s.Name, Name: MainPort}, Spec: s.Port})
		}
		for _, name := range slices.Sorted(maps.Keys(s.Ports)) {
			out = append(out, DeclaredPort{Ref: PortRef{Service: s.Name, Name: name}, Spec: s.Ports[name]})
		}
	}
	return out
}

// DeclaredPort pairs a ref with its spec.
type DeclaredPort struct {
	Ref  PortRef
	Spec PortSpec
}

// SlotPorts computes every fixed port for a slot: base + slot*stride. Auto
// ports are returned separately since the OS picks them.
func SlotPorts(def Definition, slot int) (PortTable, []PortRef, error) {
	table := PortTable{}
	var autos []PortRef
	for _, d := range DeclaredPorts(def) {
		if d.Spec.Auto {
			autos = append(autos, d.Ref)
			continue
		}
		port := d.Spec.Base + slot*def.Settings.PortStride
		if port > MaxPort {
			return nil, nil, fmt.Errorf("slot %d puts %s on port %d, above %d", slot, d.Ref, port, MaxPort)
		}
		table.Set(d.Ref.Service, d.Ref.Name, port)
	}
	return table, autos, nil
}

// StrideOverlaps finds pairs of fixed bases that collide across slots: with
// stride 10, slot 1 of base 3000 is slot 0 of base 3010. Bases further apart
// than the slot range can reach never meet. Allocation still catches the
// collision, so these are warnings.
func StrideOverlaps(def Definition) []string {
	var fixed []DeclaredPort
	for _, d := range DeclaredPorts(def) {
		if !d.Spec.Auto {
			fixed = append(fixed, d)
		}
	}
	stride := def.Settings.PortStride
	var warnings []string
	for i := range fixed {
		for j := i + 1; j < len(fixed); j++ {
			a, b := fixed[i], fixed[j]
			diff := abs(a.Spec.Base - b.Spec.Base)
			if diff != 0 && diff%stride == 0 && diff/stride < def.Settings.MaxSlots {
				warnings = append(warnings, fmt.Sprintf(
					"ports %s (%d) and %s (%d) differ by a multiple of port_stride %d, so they collide across slots",
					a.Ref, a.Spec.Base, b.Ref, b.Spec.Base, stride))
			}
		}
	}
	return warnings
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
