package domain

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
)

// Identity names one stack: a project plus a stack name.
type Identity struct {
	Project string
	Stack   string
}

// NewIdentity slugifies both names, so callers can pass raw input.
func NewIdentity(project, stack string) (Identity, error) {
	id := Identity{Project: Slug(project), Stack: Slug(stack)}
	if id.Project == "" {
		return id, fmt.Errorf("project name %q has no usable characters, set `project` in svx.yaml", project)
	}
	if id.Stack == "" {
		return id, fmt.Errorf("stack name %q has no usable characters, pass --name with letters or digits", stack)
	}
	return id, nil
}

// Key is the file-safe key used for state records: <project>--<stack>.
func (id Identity) Key() string { return id.Project + "--" + id.Stack }

// Session is the name of the stack's own tmux session, used when svx up
// runs outside tmux: svx-<project>-<stack>.
func (id Identity) Session() string { return "svx-" + id.Project + "-" + id.Stack }

func (id Identity) String() string { return id.Project + "/" + id.Stack }

// ParseKey reverses Key.
func ParseKey(key string) (Identity, error) {
	project, stack, ok := strings.Cut(key, "--")
	if !ok || project == "" || stack == "" {
		return Identity{}, fmt.Errorf("stack key %q is not <project>--<stack>", key)
	}
	return Identity{Project: project, Stack: stack}, nil
}

// Slug lowercases s and replaces every run of characters outside [a-z0-9]
// with a single "-". tmux rewrites "." and ":" in session names, so names are
// kept to a set it leaves alone.
func Slug(s string) string { return squash(s, '-') }

// Snake is Slug with "_" as the separator, for identifiers such as database
// names.
func Snake(s string) string { return squash(s, '_') }

func squash(s string, sep rune) string {
	var b strings.Builder
	pending := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pending && b.Len() > 0 {
				b.WriteRune(sep)
			}
			pending = false
			b.WriteRune(r)
			continue
		}
		pending = true
	}
	return b.String()
}

// EnvName upper-cases a service or port name and turns "-" into "_", the form
// used in injected variables such as SVX_PORT_<SERVICE>.
func EnvName(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
}

// DefaultStackName derives a stable two-word name from a checkout path, so the
// same checkout always gets the same name and different worktrees differ.
func DefaultStackName(checkoutPath string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(checkoutPath))
	sum := h.Sum64()
	adj := adjectives[sum%uint64(len(adjectives))]
	noun := nouns[(sum>>32)%uint64(len(nouns))]
	return adj + "-" + noun
}

var adjectives = []string{
	"amber", "ancient", "autumn", "bold", "brave", "bright", "brisk", "calm",
	"clever", "cobalt", "coral", "crimson", "crisp", "daring", "dawn", "deep",
	"dusky", "eager", "early", "fancy", "fern", "fleet", "frosty", "gentle",
	"gilded", "glad", "golden", "hazel", "hidden", "humble", "icy", "ivory",
	"jade", "jolly", "keen", "kind", "lively", "lucky", "lunar", "mellow",
	"misty", "modest", "noble", "olive", "pale", "plucky", "polar", "proud",
	"quick", "quiet", "rapid", "rosy", "rustic", "sandy", "silent", "silver",
	"sly", "snowy", "solar", "steady", "stormy", "sunny", "swift", "tidy",
	"velvet", "vivid", "warm", "wild", "windy", "witty", "young", "zesty",
}

var nouns = []string{
	"badger", "beacon", "bison", "brook", "canyon", "cedar", "comet", "condor",
	"coyote", "crane", "creek", "dune", "eagle", "ember", "falcon", "finch",
	"fjord", "fox", "gecko", "glacier", "grove", "harbor", "hawk", "heron",
	"ibis", "island", "jackal", "jay", "kestrel", "lagoon", "lark", "lemur",
	"lynx", "maple", "marsh", "meadow", "mesa", "moose", "newt", "oak",
	"orca", "osprey", "otter", "owl", "panda", "pine", "plover", "prairie",
	"quail", "raven", "reef", "ridge", "river", "robin", "sparrow", "spruce",
	"stork", "summit", "swan", "thrush", "tiger", "trout", "tundra", "valley",
	"viper", "walrus", "willow", "wolf", "wren", "yak", "zebra", "aspen",
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// StripANSI removes terminal escape sequences, for log output read by
// people and regexes rather than terminals.
func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }
