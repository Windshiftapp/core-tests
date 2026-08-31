//go:build test

package aitools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTimeToolSchemaPreventsAgentPreOffsetAndExposesTemporalContext(t *testing.T) {
	logTime, ok := Default.Lookup("log_time")
	if !ok {
		t.Fatal("log_time is not registered")
	}
	if !strings.Contains(logTime.Description, "never convert or pre-offset") || !strings.Contains(string(logTime.Schema), "timezone") || !strings.Contains(string(logTime.Schema), "never convert or pre-offset") {
		t.Fatalf("log_time contract does not make civil timezone semantics explicit: description=%q schema=%s", logTime.Description, logTime.Schema)
	}

	temporal, ok := Default.Lookup("get_temporal_context")
	if !ok {
		t.Fatal("get_temporal_context is not registered")
	}
	args := temporal.NewArgs()
	if err := json.Unmarshal([]byte(`{}`), args); err != nil {
		t.Fatalf("decode temporal args: %v", err)
	}
	result, err := temporal.Run(context.Background(), &Env{Timezone: "Europe/Zurich"}, args)
	if err != nil {
		t.Fatalf("run get_temporal_context: %v", err)
	}
	out, ok := result.(temporalContextOut)
	if !ok {
		t.Fatalf("temporal result type = %T", result)
	}
	if out.Timezone != "Europe/Zurich" || out.LocalDate == "" || out.LocalNow == "" || out.UTCNow == "" {
		t.Fatalf("temporal context = %+v", out)
	}
}
