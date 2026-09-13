package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"forge/internal/runner"
	"strings"
	"testing"
)

func invoke(t *testing.T, args ...string) (int, map[string]any, string) {
	t.Helper()
	var out, errout bytes.Buffer
	code := Execute(context.Background(), args, strings.NewReader(""), &out, &errout, Dependencies{Version: "1.2.3", Runner: toolFake{}})
	var obj map[string]any
	if err := json.Unmarshal(out.Bytes(), &obj); err != nil {
		t.Fatalf("invalid JSON %q: %v, stderr=%s", out.String(), err, errout.String())
	}
	return code, obj, errout.String()
}

type toolFake struct{}

func (toolFake) Run(_ context.Context, s runner.Spec) error {
	return fmt.Errorf("missing %s", s.Command)
}
func TestJSONErrorContractIncludesArgumentFailures(t *testing.T) {
	for _, args := range [][]string{{"create", "--json"}, {"create", "bad/name", "--json"}, {"wat", "--json"}, {"create", "orders", "--bad", "--json"}, {"create", "orders", "--docker=invalid", "--json"}, {"version", "extra", "--json"}} {
		code, obj, _ := invoke(t, args...)
		if code != 2 || obj["status"] != "error" || obj["code"] != "INVALID_ARGUMENT" {
			t.Errorf("args=%v code=%d obj=%v", args, code, obj)
		}
	}
}
func TestJSONVersionAndHelp(t *testing.T) {
	code, obj, _ := invoke(t, "version", "--json")
	if code != 0 || obj["version"] != "1.2.3" {
		t.Fatalf("version %v", obj)
	}
	code, obj, _ = invoke(t, "create", "--help", "--json")
	if code != 0 || obj["help"] == nil {
		t.Fatalf("help %v", obj)
	}
}
func TestJSONCreateNeverPromptsAndReportsMissingDependency(t *testing.T) {
	code, obj, stderr := invoke(t, "create", "orders", "--output", t.TempDir(), "--json")
	if code != 1 || obj["code"] != "MISSING_DEPENDENCY" || obj["dependency"] != "node" {
		t.Fatalf("code=%d obj=%v stderr=%s", code, obj, stderr)
	}
}
func TestMissingProjectJSON(t *testing.T) {
	for _, cmd := range []string{"describe", "validate"} {
		code, obj, _ := invoke(t, cmd, "--path", t.TempDir(), "--json")
		if code != 1 || obj["code"] != "INVALID_PROJECT" {
			t.Fatalf("%s %d %v", cmd, code, obj)
		}
	}
}
func TestDoctorInventoryJSON(t *testing.T) {
	code, obj, _ := invoke(t, "doctor", "--json")
	if code != 0 || obj["tools"] == nil || obj["platform"] == nil {
		t.Fatalf("doctor %d %v", code, obj)
	}
}
func TestNonInteractiveNeverReadsStdin(t *testing.T) {
	var out, errout bytes.Buffer
	code := Execute(context.Background(), []string{"create", "orders", "--non-interactive", "--output", t.TempDir()}, panicReader{}, &out, &errout, Dependencies{Runner: toolFake{}})
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("unexpected stdin read") }

func TestParseFailureRetainsLastExplicitJSONIntent(t *testing.T) {
	code, obj, _ := invoke(t, "version", "--json=false", "--bad", "--json")
	if code != 2 || obj["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("lost JSON intent: %d %v", code, obj)
	}
}
