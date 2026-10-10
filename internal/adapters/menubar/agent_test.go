package menubar

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hay-kot/svx/internal/domain"
)

func TestPlistIsValidXML(t *testing.T) {
	p := Plist("/opt/svx & co/svx", "/tmp/bar.log", map[string]string{"PATH": "/opt/homebrew/bin:/usr/bin", "SVX_TMUX_SOCKET": "<dev>"})
	dec := xml.NewDecoder(strings.NewReader(p))
	dec.Strict = true
	for {
		if _, err := dec.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("plist does not parse: %v\n%s", err, p)
		}
	}
	for _, want := range []string{
		"<string>/opt/svx &amp; co/svx</string>\n\t\t<string>bar</string>",
		"<key>SuccessfulExit</key>\n\t\t<false/>",
		"<key>SVX_TMUX_SOCKET</key>\n\t\t<string>&lt;dev&gt;</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q\n%s", want, p)
		}
	}
}

func TestAgentEnvKeepsOnlyForwardedVars(t *testing.T) {
	env := AgentEnv(func(k string) string {
		return map[string]string{"PATH": "/bin", "HOME": "/home/x", "SVX_STATE_DIR": "/state"}[k]
	})
	if len(env) != 2 || env["PATH"] != "/bin" || env["SVX_STATE_DIR"] != "/state" {
		t.Fatalf("AgentEnv = %v", env)
	}
}

func TestAttachScript(t *testing.T) {
	got := AttachScript("/usr/local/bin/svx", domain.Identity{Project: "shop", Stack: "it's"}, "api", func(k string) string {
		return map[string]string{"PATH": "/opt/homebrew/bin:/bin"}[k]
	})
	want := "#!/bin/sh\n" +
		"export PATH='/opt/homebrew/bin:/bin'\n" +
		`exec '/usr/local/bin/svx' attach --project 'shop' --name 'it'\''s' 'api'` + "\n"
	if got != want {
		t.Fatalf("AttachScript =\n%s\nwant\n%s", got, want)
	}
}
