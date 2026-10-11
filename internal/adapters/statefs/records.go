package statefs

import (
	"time"

	"github.com/hay-kot/appkit/mapx"

	"github.com/hay-kot/svx/internal/domain"
)

// record is the public file, stacks/<project>--<stack>.json. Its shape is a
// versioned contract read by the menu bar and other tools, so fields may be
// added but not renamed or removed without bumping the version.
type record struct {
	Version   int                      `json:"version"`
	Project   string                   `json:"project"`
	Stack     string                   `json:"stack"`
	Root      string                   `json:"root"`
	Dir       string                   `json:"dir"`
	Session   string                   `json:"session"`
	Slot      int                      `json:"slot"`
	Status    string                   `json:"status"`
	StartedAt time.Time                `json:"started_at"`
	MenuBar   bool                     `json:"menubar"`
	Order     []string                 `json:"service_order"`
	Services  map[string]serviceRecord `json:"services"`
}

type serviceRecord struct {
	Ports map[string]int `json:"ports"`
	URL   string         `json:"url,omitempty"`
	Links []linkRecord   `json:"links"`
	// Oneshot lets readers of the public record tell a finished oneshot
	// from a crashed service.
	Oneshot bool `json:"oneshot,omitempty"`
}

type linkRecord struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// runtime is the private file, stacks/<project>--<stack>.env.json, mode
// 0600. It holds what the shim and later commands need to act without the
// config: rendered commands, environments and the shell snapshot.
type runtime struct {
	Version  int                       `json:"version"`
	Logs     bool                      `json:"logs"`
	Env      map[string]string         `json:"env"`
	Snapshot map[string]string         `json:"snapshot"`
	Services map[string]runtimeService `json:"services"`
}

type runtimeService struct {
	Run          string            `json:"run"`
	Dir          string            `json:"dir"`
	Env          map[string]string `json:"env"`
	Depends      []string          `json:"depends"`
	ReadyKind    string            `json:"ready_kind"`
	ReadyValue   string            `json:"ready_value"`
	ReadyTimeout time.Duration     `json:"ready_timeout"`
	Oneshot      bool              `json:"oneshot"`
	Autostart    bool              `json:"autostart"`
	Retries      int               `json:"retries"`
	StopSignal   string            `json:"stop_signal"`
	StopTimeout  time.Duration     `json:"stop_timeout"`
	LogFile      string            `json:"log_file"`
}

var (
	toLinkRecord   = mapx.MapFunc[domain.Link, linkRecord](func(l domain.Link) linkRecord { return linkRecord(l) })
	fromLinkRecord = mapx.MapFunc[linkRecord, domain.Link](func(l linkRecord) domain.Link { return domain.Link(l) })
)

func toRecord(s domain.Stack) record {
	r := record{
		Version:   domain.RecordVersion,
		Project:   s.ID.Project,
		Stack:     s.ID.Stack,
		Root:      s.Root,
		Dir:       s.Dir,
		Session:   s.Session,
		Slot:      s.Slot,
		Status:    string(s.Status),
		StartedAt: s.StartedAt,
		MenuBar:   s.Settings.MenuBar,
		Order:     s.ServiceNames(),
		Services:  make(map[string]serviceRecord, len(s.Services)),
	}
	for _, svc := range s.Services {
		links := toLinkRecord.Slice(svc.Links)
		if links == nil {
			links = []linkRecord{}
		}
		r.Services[svc.Name] = serviceRecord{Ports: svc.Ports, URL: svc.URL, Links: links, Oneshot: svc.Oneshot}
	}
	return r
}

func toRuntime(s domain.Stack) runtime {
	rt := runtime{
		Version:  domain.RecordVersion,
		Logs:     s.Settings.Logs,
		Env:      s.Env,
		Snapshot: s.Snapshot,
		Services: make(map[string]runtimeService, len(s.Services)),
	}
	for _, svc := range s.Services {
		rt.Services[svc.Name] = runtimeService{
			Run:          svc.Run,
			Dir:          svc.Dir,
			Env:          svc.Env,
			Depends:      svc.Depends,
			ReadyKind:    string(svc.Ready.Kind),
			ReadyValue:   svc.Ready.Value,
			ReadyTimeout: svc.Ready.Timeout,
			Oneshot:      svc.Oneshot,
			Autostart:    svc.Autostart,
			Retries:      svc.Retries,
			StopSignal:   svc.Stop.Signal,
			StopTimeout:  svc.Stop.Timeout,
			LogFile:      svc.LogFile,
		}
	}
	return rt
}

// fromFiles rebuilds a stack. rt may be nil, which leaves runtime fields
// empty, as List promises.
func fromFiles(r record, rt *runtime) domain.Stack {
	s := domain.Stack{
		ID:        domain.Identity{Project: r.Project, Stack: r.Stack},
		Root:      r.Root,
		Dir:       r.Dir,
		Slot:      r.Slot,
		Session:   r.Session,
		Status:    domain.StackStatus(r.Status),
		StartedAt: r.StartedAt,
		Settings:  domain.Settings{PortStride: 0, MaxSlots: 0, Logs: false, MenuBar: r.MenuBar},
		Services:  nil,
		Env:       nil,
		Snapshot:  nil,
	}
	if rt != nil {
		s.Env, s.Snapshot, s.Settings.Logs = rt.Env, rt.Snapshot, rt.Logs
	}

	order := r.Order
	if len(order) == 0 {
		for name := range r.Services {
			order = append(order, name)
		}
	}
	for _, name := range order {
		sr, ok := r.Services[name]
		if !ok {
			continue
		}
		svc := domain.Service{
			Name:  name,
			Ports: sr.Ports,
			URL:   sr.URL,
			Links: fromLinkRecord.Slice(sr.Links),

			Oneshot: sr.Oneshot,
		}
		if rt != nil {
			rs := rt.Services[name]
			svc.Run, svc.Dir, svc.Env, svc.Depends = rs.Run, rs.Dir, rs.Env, rs.Depends
			svc.Ready = domain.ReadySpec{Kind: domain.ReadyKind(rs.ReadyKind), Value: rs.ReadyValue, Timeout: rs.ReadyTimeout}
			svc.Oneshot, svc.Autostart, svc.Retries, svc.LogFile = rs.Oneshot, rs.Autostart, rs.Retries, rs.LogFile
			svc.Stop = domain.StopSpec{Signal: rs.StopSignal, Timeout: rs.StopTimeout}
		}
		s.Services = append(s.Services, svc)
	}
	return s
}
