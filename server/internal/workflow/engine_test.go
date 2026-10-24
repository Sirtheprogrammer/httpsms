package workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/secrets"
	"github.com/httpsms/gateway-server/internal/storage"
)

type fakeSender struct{ sent []string }

func (f *fakeSender) SendFromWorkflow(owner, deviceID string, simSlot int, to, body string) (string, error) {
	f.sent = append(f.sent, to+"|"+body)
	return "msg_out_1", nil
}

func TestRender(t *testing.T) {
	got := Render("Hi {{ trigger.sender }}: {{ai.text}}{{missing}}", map[string]string{"trigger.sender": "+1", "ai.text": "ok"})
	if got != "Hi +1: ok" {
		t.Fatalf("Render = %q", got)
	}
}

func TestTriggerMatches(t *testing.T) {
	n := &Node{Type: NodeTrigger, Data: map[string]interface{}{"match": "starts_with", "pattern": "ORDER"}}
	if !TriggerMatches(n, Trigger{Body: "order 42"}) {
		t.Fatal("case-insensitive starts_with should match")
	}
	if TriggerMatches(n, Trigger{Body: "hello"}) {
		t.Fatal("should not match")
	}
}

// End to end: trigger -> AI (OpenAI-compatible fake) -> condition -> send SMS.
func TestRunAIConditionAndSend(t *testing.T) {
	var gotAuth string
	var gotBody map[string]interface{}
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"model":"fake-1","choices":[{"message":{"content":"Your order ships today."},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":4}}`))
	}))
	defer llm.Close()

	dir := t.TempDir()
	store, err := storage.NewDB(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	box, err := secrets.Load("", filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := box.Seal("sk-test-123")
	if err := store.CreateAICredential(&domain.AICredential{ID: "cred1", UserID: "u1", Provider: "openai", BaseURL: llm.URL, EncryptedKey: sealed, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	graph := `{"nodes":[
		{"id":"trigger","type":"trigger","data":{"match":"contains","pattern":"order"}},
		{"id":"ai","type":"ai_agent","data":{"credentialId":"cred1","model":"fake-1","systemPrompt":"Be brief","prompt":"Customer said: {{trigger.body}}"}},
		{"id":"check","type":"condition","data":{"value":"{{ai.text}}","operator":"contains","compare":"ships"}},
		{"id":"reply","type":"send_sms","data":{"body":"{{ai.text}}"}},
		{"id":"never","type":"send_sms","data":{"body":"wrong branch"}}],
		"edges":[{"source":"trigger","target":"ai"},{"source":"ai","target":"check"},
		{"source":"check","sourceHandle":"true","target":"reply"},{"source":"check","sourceHandle":"false","target":"never"}]}`
	wf := &domain.Workflow{ID: "wf1", UserID: "u1", Name: "Order bot", Graph: []byte(graph), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	g, _ := ParseGraph(wf.Graph)
	if p := Validate(g); len(p) != 0 {
		t.Fatalf("unexpected problems: %v", p)
	}

	sender := &fakeSender{}
	engine := NewEngine(store, box)
	engine.Wire(sender, nil)

	run, err := engine.Run(context.Background(), wf, g, Trigger{Sender: "+255700", Body: "where is my order?"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "success" {
		t.Fatalf("run status %s: %s", run.Status, run.Error)
	}
	if gotAuth != "Bearer sk-test-123" {
		t.Fatalf("provider got auth %q", gotAuth)
	}
	msgs, _ := gotBody["messages"].([]interface{})
	if len(msgs) != 2 || !strings.Contains(msgs[1].(map[string]interface{})["content"].(string), "where is my order") {
		t.Fatalf("unexpected prompt: %v", gotBody["messages"])
	}
	if len(sender.sent) != 1 || sender.sent[0] != "+255700|Your order ships today." {
		t.Fatalf("sent = %v", sender.sent)
	}

	// A dry run must not send anything.
	sender.sent = nil
	run, _ = engine.Run(context.Background(), wf, g, Trigger{Sender: "+255700", Body: "order?"}, true)
	if run.Status != "success" || len(sender.sent) != 0 {
		t.Fatalf("dry run sent %v (status %s)", sender.sent, run.Status)
	}
}

func TestValidateFlagsMissingTrigger(t *testing.T) {
	g := &Graph{Nodes: []Node{{ID: "a", Type: NodeSendSMS, Data: map[string]interface{}{"body": "x"}}}}
	if len(Validate(g)) == 0 {
		t.Fatal("expected a problem for missing trigger")
	}
}
