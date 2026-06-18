package expense

import (
	"context"
	"testing"
)

// TestToolParity asserts the agent and MCP surfaces expose exactly the same
// tools: every tool in the shared expenseTools metadata becomes one MCP tool,
// and every tool name routes to a real core handler (no "unknown tool" gaps).
func TestToolParity(t *testing.T) {
	mcpTools := buildExpenseMCPTools(nil)
	if len(mcpTools) != len(expenseTools) {
		t.Fatalf("MCP exposes %d tools, expenseTools has %d", len(mcpTools), len(expenseTools))
	}
	mcpNames := map[string]bool{}
	for _, st := range mcpTools {
		mcpNames[st.Tool.Name] = true
	}
	for _, meta := range expenseTools {
		if !mcpNames[meta.Name] {
			t.Errorf("tool %q in expenseTools is missing from the MCP surface", meta.Name)
		}
		// Every tool name must route to a real core handler. dispatchCore's
		// unknown-tool branch returns its sentinel BEFORE touching the (nil)
		// service; a recognised name instead reaches the service and panics on
		// the nil pointer — which we recover and treat as "handler exists".
		if !hasCoreHandler(meta.Name) {
			t.Errorf("tool %q has no core handler in dispatchCore", meta.Name)
		}
	}
}

// hasCoreHandler reports whether dispatchCore routes name to a handler. It
// probes with a nil service: an unhandled name returns the sentinel error with
// no panic; a handled name dereferences the nil service and panics, which we
// recover.
func hasCoreHandler(name string) (handled bool) {
	defer func() {
		if recover() != nil {
			handled = true
		}
	}()
	_, err := dispatchCore(context.TODO(), nil, nil, name, []byte(`{}`))
	return err == nil || err.Error() != "unknown expense tool: "+name
}
