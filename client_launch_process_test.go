package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClientLaunchProcessTerminalArguments(t *testing.T) {
	binary, ticket, script := "/Applications/Kilo Proxy's.app/Contents/MacOS/kilo-proxy", "/tmp/launch space/ticket.json", "/tmp/launch space/bootstrap.command"
	for name, separator := range map[string][]string{
		"x-terminal-emulator": {"-e"}, "gnome-terminal": {"--"}, "konsole": {"-e"},
		"xfce4-terminal": {"-x"}, "xterm": {"-e"}, "kitty": {}, "alacritty": {"-e"}, "wezterm": {"start", "--"},
	} {
		t.Run(name, func(t *testing.T) {
			invocation, err := clientTerminalCommandFor("linux", binary, ticket, script, func(candidate string) (string, error) {
				if candidate == name {
					return "/usr/bin/" + name, nil
				}
				return "", exec.ErrNotFound
			})
			want := append(append([]string{}, separator...), binary, clientLaunchRunnerFlag, ticket)
			if err != nil || invocation.Executable != "/usr/bin/"+name || !reflect.DeepEqual(invocation.Args, want) {
				t.Fatalf("terminal invocation = %+v, %v; want args %q", invocation, err, want)
			}
		})
	}
	got, err := clientTerminalCommandFor("darwin", binary, ticket, script, nil)
	if err != nil || got.Executable != "/usr/bin/open" || !reflect.DeepEqual(got.Args, []string{"-a", "Terminal", script}) {
		t.Fatalf("macOS terminal invocation = %+v, %v", got, err)
	}
	if _, err := clientTerminalCommandFor("linux", binary, ticket, script, func(string) (string, error) { return "", exec.ErrNotFound }); err == nil {
		t.Fatal("missing terminal was reported available")
	}
	if _, err := clientTerminalCommandFor("unknown", binary, ticket, script, nil); err == nil {
		t.Fatal("unsupported terminal platform accepted")
	}
}

func TestClientLaunchProcessEnvironmentIsPrivateAndPreservesEmpty(t *testing.T) {
	parent := []string{"Path=old-win-path", "PATH=old-unix-path", "KEEP=", "REMOVE=secret", "remove=lowercase", "TOKEN=old", "=C:=C:\\work"}
	before := append([]string(nil), parent...)
	overrides := map[string]string{"PATH": "new-path", "TOKEN": "synthetic-local-key", "EMPTY": ""}
	got := clientChildEnvironment(parent, overrides, []string{"REMOVE"}, "windows")
	want := []string{"KEEP=", "=C:=C:\\work", "EMPTY=", "PATH=new-path", "TOKEN=synthetic-local-key"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Windows environment = %q, want %q", got, want)
	}
	unix := clientChildEnvironment(parent, overrides, []string{"REMOVE"}, "linux")
	if !strings.Contains(strings.Join(unix, "\n"), "Path=old-win-path") || !strings.Contains(strings.Join(unix, "\n"), "remove=lowercase") {
		t.Fatal("Unix environment lost case-sensitive names")
	}
	if !reflect.DeepEqual(parent, before) || overrides["TOKEN"] != "synthetic-local-key" {
		t.Fatal("child environment changed caller state")
	}
}

func clientProcessTestPlan(t *testing.T) clientLaunchPlan {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return clientLaunchPlan{Client: "codex-cli", Name: "Synthetic client", Kind: "terminal", Executable: binary, Directory: t.TempDir(), Env: map[string]string{}}
}

func TestClientLaunchProcessRunnerPathPreservesTerminalEnvironment(t *testing.T) {
	plan := clientProcessTestPlan(t)
	directory := filepath.Dir(plan.Executable)
	environment := clientRunnerEnvironment([]string{"PATH=terminal-startup-path", "KEEP=1"}, plan)
	want := "PATH=" + directory + string(os.PathListSeparator) + "terminal-startup-path"
	if !slicesContainClientEnvironment(environment, want) || !slicesContainClientEnvironment(environment, "KEEP=1") {
		t.Fatalf("runner PATH discarded terminal startup settings: %q", environment)
	}
	if again := clientRunnerEnvironment(environment, plan); !reflect.DeepEqual(again, environment) {
		t.Fatal("runner prepends duplicate executable directory")
	}
}

func slicesContainClientEnvironment(environment []string, value string) bool {
	for _, entry := range environment {
		if entry == value {
			return true
		}
	}
	return false
}

type clientProcessTestResult struct {
	Input         string
	Directory     string
	Args          []string
	Token         string
	Removed       bool
	EmptyPresent  bool
	TicketPresent bool
}

func TestClientLaunchProcessMainBootstrap(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--client-launch-ticket" {
		return
	}
	os.Args = []string{os.Args[0], clientLaunchRunnerFlag, os.Args[len(os.Args)-1]}
	main()
}

// The only process executed by runner tests is this synthetic test executable.
func TestClientLaunchProcessHelper(t *testing.T) {
	if os.Getenv("KILO_LAUNCH_PROCESS_HELPER") != "1" {
		return
	}
	input, _ := io.ReadAll(os.Stdin)
	directory, _ := os.Getwd()
	_, removed := os.LookupEnv("KILO_LAUNCH_REMOVE_TEST")
	_, empty := os.LookupEnv("KILO_LAUNCH_EMPTY_TEST")
	_, ticketErr := os.Stat(os.Getenv("KILO_LAUNCH_TICKET_TEST"))
	result := clientProcessTestResult{Input: string(input), Directory: directory, Args: os.Args[1:], Token: os.Getenv("KILO_LAUNCH_KEY_TEST"), Removed: !removed, EmptyPresent: empty, TicketPresent: ticketErr == nil}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	_, _ = io.WriteString(os.Stderr, "synthetic stderr")
	os.Exit(0)
}

func TestClientLaunchProcessRunnerConsumesTicketAndUsesRealChildIO(t *testing.T) {
	plan := clientProcessTestPlan(t)
	plan.Args = []string{"-test.run=^TestClientLaunchProcessHelper$", "--", "space and apostrophe '", "unicode café", "$(not-a-command)", "%PATH%!^&"}
	plan.Env = map[string]string{"KILO_LAUNCH_PROCESS_HELPER": "1", "KILO_LAUNCH_KEY_TEST": "synthetic-local-key", "KILO_LAUNCH_EMPTY_TEST": ""}
	plan.Unset = []string{"KILO_LAUNCH_REMOVE_TEST"}
	t.Setenv("KILO_LAUNCH_REMOVE_TEST", "parent-value")
	t.Setenv("KILO_LAUNCH_KEY_TEST", "parent-key")
	ticket, script, cleanup, err := writeClientLaunchTicket(plan, plan.Executable, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	plan.Env["KILO_LAUNCH_TICKET_TEST"] = ticket
	data, _ := json.Marshal(clientLaunchTicket{Version: 1, Expires: time.Now().Add(time.Minute).Unix(), Plan: plan})
	if err := os.WriteFile(ticket, data, 0600); err != nil {
		t.Fatal(err)
	}
	bootstrap, _ := os.ReadFile(script)
	if bytes.Contains(bootstrap, []byte("synthetic-local-key")) || bytes.Contains(bootstrap, []byte("KILO_LAUNCH_KEY_TEST")) {
		t.Fatal("bootstrap exposes credentials or environment settings")
	}
	if runtime.GOOS != "windows" {
		for path, mode := range map[string]os.FileMode{filepath.Dir(ticket): 0700, ticket: 0600, script: 0700} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != mode {
				t.Fatalf("private file mode for %s: %v, %v", path, info, err)
			}
		}
	}
	var output, stderr bytes.Buffer
	if err := runClientLaunchTicket(ticket, strings.NewReader("interactive input\n"), &output, &stderr); err != nil {
		t.Fatalf("synthetic client: %v\n%s", err, stderr.String())
	}
	var got clientProcessTestResult
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("child output: %v\n%s", err, output.String())
	}
	// Windows may report the same directory using its short 8.3 spelling while
	// EvalSymlinks returns the long name. Verify filesystem identity, not spelling.
	wantDirectory, wantErr := os.Stat(plan.Directory)
	gotDirectory, gotErr := os.Stat(got.Directory)
	if wantErr != nil || gotErr != nil || !os.SameFile(wantDirectory, gotDirectory) {
		t.Fatalf("child cwd %q is not requested directory %q: %v, %v", got.Directory, plan.Directory, gotErr, wantErr)
	}
	if got.Input != "interactive input\n" || !reflect.DeepEqual(got.Args, plan.Args) || got.Token != "synthetic-local-key" || !got.Removed || !got.EmptyPresent || got.TicketPresent || stderr.String() != "synthetic stderr" {
		t.Fatalf("child arguments, cwd, environment, cleanup or IO differ: %+v; stderr=%q", got, stderr.String())
	}
	if _, err := os.Stat(filepath.Dir(ticket)); !os.IsNotExist(err) {
		t.Fatalf("ticket directory remains after consumption: %v", err)
	}
	if _, err := readClientLaunchTicket(ticket); err == nil {
		t.Fatal("consumed ticket was replayed")
	}
	if os.Getenv("KILO_LAUNCH_KEY_TEST") != "parent-key" || os.Getenv("KILO_LAUNCH_REMOVE_TEST") != "parent-value" {
		t.Fatal("runner changed its parent environment")
	}
}

func TestClientLaunchProcessRejectsInvalidAndExpiredTickets(t *testing.T) {
	for _, test := range []string{"expired", "unknown-field", "trailing-object", "oversize", "desktop", "symlink"} {
		t.Run(test, func(t *testing.T) {
			plan := clientProcessTestPlan(t)
			ticket, _, cleanup, err := writeClientLaunchTicket(plan, plan.Executable, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			data, _ := os.ReadFile(ticket)
			switch test {
			case "expired":
				data, _ = json.Marshal(clientLaunchTicket{Version: 1, Expires: time.Now().Add(-time.Minute).Unix(), Plan: plan})
			case "unknown-field":
				data = append([]byte(`{"unknown":true,`), data[1:]...)
			case "trailing-object":
				data = append(data, []byte(" {}")...)
			case "oversize":
				data = bytes.Repeat([]byte(" "), clientLaunchTicketLimit+1)
			case "desktop":
				plan.Kind = "desktop"
				data, _ = json.Marshal(clientLaunchTicket{Version: 1, Expires: time.Now().Add(time.Minute).Unix(), Plan: plan})
			case "symlink":
				if runtime.GOOS == "windows" {
					return // Creating arbitrary symlinks requires a Windows privilege.
				}
				target := filepath.Join(t.TempDir(), "kept.json")
				_ = os.WriteFile(target, data, 0600)
				_ = os.Remove(ticket)
				if err := os.Symlink(target, ticket); err != nil {
					t.Fatal(err)
				}
				if _, err := readClientLaunchTicket(ticket); err == nil {
					t.Fatal("symlinked ticket accepted")
				}
				if _, err := os.Stat(target); err != nil {
					t.Fatal("ticket rejection removed an unrelated file")
				}
				return
			}
			_ = os.WriteFile(ticket, data, 0600)
			if _, err := readClientLaunchTicket(ticket); err == nil {
				t.Fatal("invalid launch ticket accepted")
			}
		})
	}
}

func TestClientLaunchProcessRejectsMalformedPlansBeforeWriting(t *testing.T) {
	for _, mutate := range []func(*clientLaunchPlan){
		func(p *clientLaunchPlan) { p.Executable = "relative" },
		func(p *clientLaunchPlan) { p.Directory = "relative" },
		func(p *clientLaunchPlan) { p.Kind = "shell" },
		func(p *clientLaunchPlan) { p.Args = []string{"a\x00b"} },
		func(p *clientLaunchPlan) { p.Env["BAD=NAME"] = "value" },
		func(p *clientLaunchPlan) { p.Unset = []string{""} },
	} {
		plan := clientProcessTestPlan(t)
		mutate(&plan)
		if _, _, _, err := writeClientLaunchTicket(plan, plan.Executable, t.TempDir()); err == nil {
			t.Fatal("malformed plan accepted")
		}
	}
}
