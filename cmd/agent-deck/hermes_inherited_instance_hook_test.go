package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// A Hermes process started from inside another tool's pane (a Claude session
// running `hermes -z ...`) inherits that pane's AGENTDECK_INSTANCE_ID, and the
// shell hooks agent-deck installs into ~/.hermes/config.yaml then report its
// events against that instance. Its cwd is the pane's cwd, so the #1729 cwd
// evidence cannot tell it apart. Those events must leave the other tool's
// hook status and session binding alone; Hermes events for a Hermes instance
// and Claude events for a Claude instance keep writing.

const inheritedHermesSID = "20261022_061500_abcdef"

func inheritedHookEnv(t *testing.T, instanceID, tool string) {
	t.Helper()
	t.Setenv("AGENTDECK_INSTANCE_ID", instanceID)
	for _, name := range []string{"AGENTDECK_TOOL", "AGENTDECK_HOOK_GENERATION"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	if tool != "" {
		t.Setenv("AGENTDECK_TOOL", tool)
	}
}

func readHookStatusFile(t *testing.T, instanceID string) (hookStatusFile, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(getHooksDir(), instanceID+".json"))
	if err != nil {
		t.Fatalf("read status file for %s: %v", instanceID, err)
	}
	var st hookStatusFile
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("decode status file for %s: %v", instanceID, err)
	}
	return st, raw
}

func TestHermesHookFromInheritedInstanceLeavesOtherToolUntouched(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	const claudeID, claudeSID = "claude-inst", "0d7f6c2e-claude-own-session"

	// Claude's own turn binds the instance.
	inheritedHookEnv(t, claudeID, "")
	runHookHandlerWithStdin(t, fmt.Sprintf(`{"hook_event_name":"UserPromptSubmit","session_id":%q,"cwd":%q}`, claudeSID, cwd))
	before, beforeRaw := readHookStatusFile(t, claudeID)
	if before.SessionID != claudeSID || before.Status != "running" {
		t.Fatalf("claude event did not write: %+v", before)
	}

	// A Hermes one-shot launched from that pane (AGENTDECK_TOOL unset, as a
	// Claude launch exports none), or from a Codex pane (AGENTDECK_TOOL=codex).
	for _, tool := range []string{"", "codex"} {
		inheritedHookEnv(t, claudeID, tool)
		for _, event := range []string{"on_session_start", "pre_llm_call", "pre_api_request", "pre_tool_call",
			"post_tool_call", "post_api_request", "post_llm_call", "on_session_end", "on_session_finalize"} {
			runHookHandlerWithStdin(t, fmt.Sprintf(`{"hook_event_name":%q,"session_id":%q,"cwd":%q,"extra":{"platform":"cli"}}`,
				event, inheritedHermesSID, cwd))
			if _, raw := readHookStatusFile(t, claudeID); string(raw) != string(beforeRaw) {
				t.Fatalf("hermes %s with AGENTDECK_TOOL=%q rewrote the claude status file:\n got %s\nwant %s", event, tool, raw, beforeRaw)
			}
			if got := session.ReadHookSessionAnchor(claudeID); got != claudeSID {
				t.Fatalf("hermes %s with AGENTDECK_TOOL=%q moved the claude session anchor to %q", event, tool, got)
			}
		}
	}

	// Claude's next event for the same instance still writes.
	inheritedHookEnv(t, claudeID, "")
	runHookHandlerWithStdin(t, fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q,"cwd":%q}`, claudeSID, cwd))
	if after, _ := readHookStatusFile(t, claudeID); after.Event != "Stop" || after.Status != "waiting" || after.SessionID != claudeSID {
		t.Fatalf("claude Stop after the hermes events did not write: %+v", after)
	}

	// Hermes events for a Hermes instance still write.
	const hermesID = "hermes-inst"
	inheritedHookEnv(t, hermesID, "hermes")
	runHookHandlerWithStdin(t, fmt.Sprintf(`{"hook_event_name":"pre_llm_call","session_id":%q,"cwd":%q,"extra":{"platform":"cli"}}`,
		inheritedHermesSID, cwd))
	if st, _ := readHookStatusFile(t, hermesID); st.Event != "pre_llm_call" || st.Status != "running" || st.SessionID != inheritedHermesSID {
		t.Fatalf("hermes event for a hermes instance did not write: %+v", st)
	}
	if got := session.ReadHookSessionAnchor(hermesID); got != inheritedHermesSID {
		t.Fatalf("hermes instance anchor = %q, want %q", got, inheritedHermesSID)
	}
}
