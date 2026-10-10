package domain

import (
	"fmt"
	"slices"
	"strings"
)

// Levels groups services into dependency levels: every service in a level
// depends only on services in earlier levels. Within a level, declaration
// order is kept. It fails on a cycle or an unknown dependency.
func Levels(services []ServiceDef) ([][]string, error) {
	known := make(map[string]ServiceDef, len(services))
	for _, s := range services {
		known[s.Name] = s
	}
	for _, s := range services {
		for _, dep := range s.Depends {
			if _, ok := known[dep]; !ok {
				return nil, fmt.Errorf("services.%s.depends: unknown service %q", s.Name, dep)
			}
		}
	}

	placed := make(map[string]bool, len(services))
	var levels [][]string
	for len(placed) < len(services) {
		var level []string
		for _, s := range services {
			if placed[s.Name] {
				continue
			}
			ready := true
			for _, dep := range s.Depends {
				if !placed[dep] {
					ready = false
					break
				}
			}
			if ready {
				level = append(level, s.Name)
			}
		}
		if len(level) == 0 {
			return nil, fmt.Errorf("dependency cycle: %s", describeCycle(services, placed))
		}
		for _, name := range level {
			placed[name] = true
		}
		levels = append(levels, level)
	}
	return levels, nil
}

// describeCycle walks unplaced services until one repeats, so the error shows
// the actual loop rather than every service left over.
func describeCycle(services []ServiceDef, placed map[string]bool) string {
	deps := make(map[string][]string, len(services))
	var start string
	for _, s := range services {
		deps[s.Name] = s.Depends
		if start == "" && !placed[s.Name] {
			start = s.Name
		}
	}

	seen := map[string]int{}
	var path []string
	for cur := start; ; {
		if i, ok := seen[cur]; ok {
			return strings.Join(append(path[i:], cur), " -> ")
		}
		seen[cur] = len(path)
		path = append(path, cur)
		next := ""
		for _, d := range deps[cur] {
			if !placed[d] {
				next = d
				break
			}
		}
		if next == "" {
			return strings.Join(path, " -> ")
		}
		cur = next
	}
}

// Closure returns the named services plus everything they depend on,
// transitively.
func Closure(services []ServiceDef, names []string) (map[string]bool, error) {
	byName := make(map[string]ServiceDef, len(services))
	for _, s := range services {
		byName[s.Name] = s
	}

	out := make(map[string]bool)
	queue := slices.Clone(names)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if out[name] {
			continue
		}
		s, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown service %q, known services: %s", name, strings.Join(serviceNames(services), ", "))
		}
		out[name] = true
		queue = append(queue, s.Depends...)
	}
	return out, nil
}

// Targets picks the services `up` acts on: the named ones, or every autostart
// service when none are named, plus their dependencies.
func Targets(services []ServiceDef, requested []string) (map[string]bool, error) {
	if len(requested) == 0 {
		for _, s := range services {
			if s.Autostart {
				requested = append(requested, s.Name)
			}
		}
	}
	return Closure(services, requested)
}

func serviceNames(services []ServiceDef) []string {
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = s.Name
	}
	return names
}
