package menubar

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// AgentLabel names the launchd agent that starts the menu bar at login.
const AgentLabel = "com.github.hay-kot.svx.menubar"

// AgentPath is where the agent's plist lives.
func AgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", AgentLabel+".plist"), nil
}

// AgentEnv picks the environment the agent runs with. launchd starts
// agents with a bare PATH that has no Homebrew, so the installing shell's
// PATH goes in, along with svx's own overrides.
func AgentEnv(getenv func(string) string) map[string]string {
	env := map[string]string{}
	for _, k := range forwardedEnv {
		if v := getenv(k); v != "" {
			env[k] = v
		}
	}
	return env
}

// Plist is the launchd agent definition. KeepAlive restarts the menu bar
// after a crash but not after a clean exit, so Quit and menubar.enabled:
// false both keep it stopped until the next login.
func Plist(svx, logPath string, env map[string]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	key := func(k string) { fmt.Fprintf(&b, "\t<key>%s</key>\n", k) }
	str := func(indent, v string) { fmt.Fprintf(&b, "%s<string>%s</string>\n", indent, escape(v)) }

	key("Label")
	str("\t", AgentLabel)
	key("ProgramArguments")
	b.WriteString("\t<array>\n")
	str("\t\t", svx)
	str("\t\t", "bar")
	b.WriteString("\t</array>\n")
	key("RunAtLoad")
	b.WriteString("\t<true/>\n")
	key("KeepAlive")
	b.WriteString("\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n")
	key("ProcessType")
	str("\t", "Interactive")
	key("LimitLoadToSessionType")
	str("\t", "Aqua")
	if len(env) > 0 {
		key("EnvironmentVariables")
		b.WriteString("\t<dict>\n")
		for _, k := range slices.Sorted(maps.Keys(env)) {
			fmt.Fprintf(&b, "\t\t<key>%s</key>\n", escape(k))
			str("\t\t", env[k])
		}
		b.WriteString("\t</dict>\n")
	}
	key("StandardOutPath")
	str("\t", logPath)
	key("StandardErrorPath")
	str("\t", logPath)
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func domainTarget() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// InstallAgent writes the plist and (re)loads it, which starts the menu
// bar now as well as at every login.
func InstallAgent(ctx context.Context, plist string) (string, error) {
	path, err := AgentPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(plist), 0o600); err != nil {
		return "", err
	}
	bootout(ctx)
	// bootout returns before launchd has finished unloading, and an early
	// bootstrap fails with "Input/output error".
	var out []byte
	for range 10 {
		out, err = exec.CommandContext(ctx, "launchctl", "bootstrap", domainTarget(), path).CombinedOutput() //nolint:gosec // fixed arguments
		if err == nil {
			return path, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return path, fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(out)))
}

// UninstallAgent stops the menu bar and removes the plist. It returns the
// removed path, or "" when nothing was installed.
func UninstallAgent(ctx context.Context) (string, error) {
	path, err := AgentPath()
	if err != nil {
		return "", err
	}
	bootout(ctx)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return path, nil
}

func bootout(ctx context.Context) {
	_ = exec.CommandContext(ctx, "launchctl", "bootout", domainTarget()+"/"+AgentLabel).Run() //nolint:gosec // fixed arguments
}

// AgentLoaded reports whether launchd has the agent loaded.
func AgentLoaded(ctx context.Context) bool {
	return exec.CommandContext(ctx, "launchctl", "print", domainTarget()+"/"+AgentLabel).Run() == nil //nolint:gosec // fixed arguments
}
