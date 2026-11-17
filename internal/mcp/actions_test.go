package mcp

import (
	"slices"
	"strings"
	"testing"
)

// An agent lists the plugins' actions and runs one: told the fields of
// the form it asks, it answers them.
func TestActionTools(t *testing.T) {
	cs := connect(t, New(&testHost{lib: testLibrary(t)}, "test", true))
	var list actionsOut
	if msg := call(t, cs, "list_actions", nil, &list); msg != "" {
		t.Fatal(msg)
	}
	if len(list.Actions) != 1 || list.Actions[0].ID != "find" || list.Actions[0].Scope != "library" {
		t.Fatalf("actions %+v", list)
	}
	var out runActionOut
	if msg := call(t, cs, "run_action", map[string]any{"plugin": "com.example.dups", "action": "find"}, &out); msg != "" {
		t.Fatal(msg)
	}
	if out.OK || out.Error == nil || out.Error.Kind != "invalid" || !strings.Contains(out.Error.Message, "by (select: title | isbn)") {
		t.Fatalf("without answers: %+v %+v", out, out.Error)
	}
	out = runActionOut{}
	if msg := call(t, cs, "run_action", map[string]any{"plugin": "com.example.dups", "action": "find", "answers": map[string]any{"by": "isbn"}}, &out); msg != "" {
		t.Fatal(msg)
	}
	if !out.OK || out.Outcome.Message != "by isbn" || out.Outcome.Applied || len(out.Outcome.Groups[0].Books) != 2 {
		t.Fatalf("answered: %+v", out.Outcome)
	}
	// What is wrong with the call is the call's error.
	if msg := call(t, cs, "run_action", map[string]any{"plugin": "nope", "action": "find"}, nil); !strings.Contains(msg, "no loaded plugin") {
		t.Errorf("unknown plugin: %q", msg)
	}

	// Reading only, agents don't get them.
	ro := connect(t, New(&testHost{lib: testLibrary(t)}, "test", false))
	if names := toolNames(t, ro); slices.Contains(names, "run_action") || slices.Contains(names, "list_actions") {
		t.Errorf("read-only tools %v", names)
	}
}
