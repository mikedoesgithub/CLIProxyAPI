package balance

import (
	"strings"
	"testing"
)

func TestExtractOnlyUserTextAcrossProtocols(t *testing.T) {
	for _, raw := range []string{
		`{"messages":[{"role":"system","content":"SECRET"},{"role":"user","content":"context"},{"role":"assistant","content":"SECRET"},{"role":"tool","content":"SECRET"},{"role":"user","content":[{"type":"text","text":"question"},{"type":"image","source":{"data":"SECRET"}}]}]}`,
		`{"contents":[{"role":"model","parts":[{"text":"SECRET"}]},{"role":"user","parts":[{"text":"context"}]},{"role":"user","parts":[{"text":"question"},{"inlineData":{"mimeType":"image/png","data":"SECRET"}}]}]}`,
		`{"input":[{"role":"user","content":"context"},{"type":"function_call_output","output":"SECRET"},{"role":"user","content":[{"type":"input_text","text":"question"},{"type":"input_image","image_url":"SECRET"}]}]}`,
	} {
		in, err := Extract([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if in.Request != "question" || in.Context != "context" || !in.Vision {
			t.Fatalf("got %+v", in)
		}
	}
}

func TestExtractRejectsOpaqueContinuationsAndBoundsText(t *testing.T) {
	for _, raw := range []string{`{"previous_response_id":"resp_1"}`, `{"previous_interaction_id":"abc"}`, `invalid`} {
		if _, err := Extract([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	in, err := Extract([]byte(`{"input":"` + strings.Repeat("界", 40000) + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(in.Request)) != 4000 || !in.LongContext {
		t.Fatalf("bad bounds")
	}
}
