package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/hay-kot/svx/internal/domain"
)

// allocation is the outcome of choosing a slot.
type allocation struct {
	slot  int
	ports domain.PortTable
}

// allocate picks a slot for a stack. The caller must hold the state lock.
//
// Starting from the stack's previous slot keeps its URLs stable across down
// and up. Slots held by live stacks, ports leased to them, and ports something
// else is listening on all push the candidate to the next slot.
func (a *App) allocate(ctx context.Context, def domain.Definition, id domain.Identity, prev *domain.Stack) (allocation, error) {
	records, err := a.Store.List()
	if err != nil {
		return allocation{}, err
	}

	heldSlots := map[int]domain.Identity{}
	leased := map[int]string{}
	for i := range records {
		rec := records[i]
		if rec.ID == id || rec.Status != domain.StackUp {
			continue
		}
		starting := a.starting(rec)
		alive, err := a.stackAlive(ctx, &rec)
		if err != nil {
			return allocation{}, err
		}
		if !alive && !starting {
			continue
		}
		heldSlots[rec.Slot] = rec.ID
		for _, pa := range rec.Ports().All() {
			leased[pa.Port] = fmt.Sprintf("%s (%s)", rec.ID, pa.Ref)
		}
	}

	start := 0
	var prevPorts domain.PortTable
	if prev != nil {
		start = prev.Slot
		prevPorts = prev.Ports()
	}

	maxSlots := def.Settings.MaxSlots
	var reasons []string
	for i := range maxSlots {
		slot := (start + i) % maxSlots
		if holder, ok := heldSlots[slot]; ok {
			reasons = append(reasons, fmt.Sprintf("slot %d held by %s", slot, holder))
			continue
		}

		table, autos, err := domain.SlotPorts(def, slot)
		if err != nil {
			reasons = append(reasons, err.Error())
			continue
		}

		var busy []string
		for _, pa := range table.All() {
			if holder, ok := leased[pa.Port]; ok {
				busy = append(busy, fmt.Sprintf("%d (%s) leased to %s", pa.Port, pa.Ref, holder))
				continue
			}
			if !a.Ports.Free(pa.Port) {
				busy = append(busy, fmt.Sprintf("%d (%s) in use by another process", pa.Port, pa.Ref))
			}
		}
		if len(busy) > 0 {
			reasons = append(reasons, fmt.Sprintf("slot %d: %s", slot, strings.Join(busy, ", ")))
			continue
		}

		for _, ref := range autos {
			if p, ok := prevPorts.Get(ref.Service, ref.Name); ok && a.Ports.Free(p) {
				if _, taken := leased[p]; !taken {
					table.Set(ref.Service, ref.Name, p)
					continue
				}
			}
			p, err := a.Ports.Ephemeral()
			if err != nil {
				return allocation{}, fmt.Errorf("picking a port for %s: %w", ref, err)
			}
			table.Set(ref.Service, ref.Name, p)
		}
		return allocation{slot: slot, ports: table}, nil
	}

	const shown = 5
	if len(reasons) > shown {
		reasons = append(reasons[:shown], fmt.Sprintf("and %d more", len(reasons)-shown))
	}
	return allocation{}, fmt.Errorf(
		"no free slot among %d candidates: %s; stop a stack with `svx down`, free the ports, or raise settings.max_slots",
		maxSlots, strings.Join(reasons, "; "))
}

// portMoves describes ports that changed between two tables.
func portMoves(before, after domain.PortTable) []string {
	var moves []string
	for _, pa := range after.All() {
		old, ok := before.Get(pa.Ref.Service, pa.Ref.Name)
		if ok && old != pa.Port {
			moves = append(moves, fmt.Sprintf("%s %d -> %d", pa.Ref, old, pa.Port))
		}
	}
	return moves
}
