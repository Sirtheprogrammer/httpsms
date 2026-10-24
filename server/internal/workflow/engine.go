// Package workflow executes the node graphs users build in the dashboard's visual editor.
//
// A graph has exactly one trigger node (an incoming SMS). From there, nodes run along
// the edges: AI agent, condition (true/false branches), send SMS, HTTP request, delay.
// Text fields can reference earlier results with {{trigger.body}} or {{node_id.text}}.
package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/httpsms/gateway-server/internal/ai"
	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/secrets"
	"github.com/httpsms/gateway-server/internal/storage"
)

const (
	NodeTrigger = "trigger"
	NodeAI      = "ai_agent"
	NodeIf      = "condition"
	NodeSendSMS = "send_sms"
	NodeHTTP    = "http_request"
	NodeDelay   = "delay"

	maxSteps          = 50
	maxDelaySeconds   = 300
	loopWindow        = 10 * time.Minute
	maxRunsPerSender  = 10
	httpBodyLimit     = 8 << 10
	maxConcurrentRuns = 8
)

type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

type Node struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Position map[string]float64     `json:"position"`
	Data     map[string]interface{} `json:"data"`
}

type Edge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	SourceHandle string `json:"sourceHandle,omitempty"`
}

// Step is one executed node in a run's log.
type Step struct {
	NodeID     string            `json:"node_id"`
	Type       string            `json:"type"`
	Label      string            `json:"label"`
	Status     string            `json:"status"` // success, failed, simulated
	Output     map[string]string `json:"output,omitempty"`
	Error      string            `json:"error,omitempty"`
	DurationMs int64             `json:"duration_ms"`
}

// Trigger describes the incoming SMS that started a run.
type Trigger struct {
	MessageID string
	Sender    string
	Body      string
	DeviceID  string
	SimSlot   int
	At        time.Time
}

// SMSSender lets the engine queue outbound SMS through the gateway.
type SMSSender interface {
	SendFromWorkflow(owner, deviceID string, simSlot int, to, body string) (messageID string, err error)
}

// Notifier pushes run updates to the owner's open dashboards.
type Notifier interface {
	BroadcastToDashboards(owner, eventType string, data interface{})
}

type Engine struct {
	store    *storage.DB
	box      *secrets.Box
	sender   SMSSender
	notifier Notifier
	http     *http.Client
	slots    chan struct{}
}

func NewEngine(store *storage.DB, box *secrets.Box) *Engine {
	return &Engine{
		store: store,
		box:   box,
		http:  &http.Client{Timeout: 20 * time.Second},
		slots: make(chan struct{}, maxConcurrentRuns),
	}
}

// Wire breaks the construction cycle with the WebSocket hub.
func (e *Engine) Wire(sender SMSSender, notifier Notifier) {
	e.sender = sender
	e.notifier = notifier
}

func ParseGraph(raw []byte) (*Graph, error) {
	var g Graph
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("invalid workflow graph: %w", err)
	}
	return &g, nil
}

func (g *Graph) node(id string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

func (g *Graph) trigger() *Node {
	for i := range g.Nodes {
		if g.Nodes[i].Type == NodeTrigger {
			return &g.Nodes[i]
		}
	}
	return nil
}

// Validate returns human-readable problems; an enabled workflow must have none.
func Validate(g *Graph) []string {
	var problems []string
	triggers := 0
	for _, n := range g.Nodes {
		label := nodeLabel(&n)
		switch n.Type {
		case NodeTrigger:
			triggers++
			if str(n.Data, "match") == "regex" {
				if _, err := regexp.Compile(str(n.Data, "pattern")); err != nil {
					problems = append(problems, fmt.Sprintf("%s: invalid regex", label))
				}
			}
		case NodeAI:
			if str(n.Data, "credentialId") == "" {
				problems = append(problems, fmt.Sprintf("%s: choose an API key", label))
			}
			if str(n.Data, "model") == "" {
				problems = append(problems, fmt.Sprintf("%s: choose a model", label))
			}
		case NodeSendSMS:
			if strings.TrimSpace(str(n.Data, "body")) == "" {
				problems = append(problems, fmt.Sprintf("%s: message text is empty", label))
			}
		case NodeHTTP:
			if !strings.HasPrefix(str(n.Data, "url"), "http") {
				problems = append(problems, fmt.Sprintf("%s: URL must start with http:// or https://", label))
			}
		case NodeIf:
			if str(n.Data, "operator") == "regex" {
				if _, err := regexp.Compile(str(n.Data, "compare")); err != nil {
					problems = append(problems, fmt.Sprintf("%s: invalid regex", label))
				}
			}
		case NodeDelay:
		default:
			problems = append(problems, fmt.Sprintf("unknown node type %q", n.Type))
		}
	}
	if triggers != 1 {
		problems = append(problems, "a workflow needs exactly one \"Incoming SMS\" trigger")
	}
	if t := g.trigger(); t != nil {
		hasOut := false
		for _, e := range g.Edges {
			if e.Source == t.ID {
				hasOut = true
			}
		}
		if !hasOut {
			problems = append(problems, "connect the trigger to at least one step")
		}
	}
	return problems
}

// TriggerMatches applies the trigger node's filters to an incoming SMS.
func TriggerMatches(n *Node, t Trigger) bool {
	if dev := str(n.Data, "deviceId"); dev != "" && dev != t.DeviceID {
		return false
	}
	if f := strings.TrimSpace(str(n.Data, "senderFilter")); f != "" && !strings.Contains(t.Sender, f) {
		return false
	}
	pattern := str(n.Data, "pattern")
	body := t.Body
	if !boolean(n.Data, "caseSensitive") && str(n.Data, "match") != "regex" {
		pattern = strings.ToLower(pattern)
		body = strings.ToLower(body)
	}
	switch str(n.Data, "match") {
	case "contains":
		return strings.Contains(body, pattern)
	case "starts_with":
		return strings.HasPrefix(strings.TrimSpace(body), pattern)
	case "equals":
		return strings.TrimSpace(body) == strings.TrimSpace(pattern)
	case "regex":
		re, err := regexp.Compile(pattern)
		return err == nil && re.MatchString(t.Body)
	default:
		return true
	}
}

// HandleInbound starts every matching enabled workflow of the message's owner.
func (e *Engine) HandleInbound(msg domain.Message) {
	if msg.UserID == "" {
		return
	}
	workflows, err := e.store.ListWorkflows(msg.UserID, true)
	if err != nil {
		log.Printf("[Workflow] list failed: %v", err)
		return
	}
	trig := Trigger{MessageID: msg.ID, Sender: msg.Sender, Body: msg.Body, DeviceID: msg.DeviceID, SimSlot: msg.SimSlot, At: msg.CreatedAt}
	for _, wf := range workflows {
		g, err := ParseGraph(wf.Graph)
		if err != nil {
			continue
		}
		t := g.trigger()
		if t == nil || !TriggerMatches(t, trig) {
			continue
		}
		recent, _ := e.store.CountRecentRuns(wf.ID, msg.Sender, time.Now().UTC().Add(-loopWindow))
		if recent >= maxRunsPerSender {
			log.Printf("[Workflow] %q paused for %s: %d runs in %s (loop guard)", wf.Name, msg.Sender, recent, loopWindow)
			continue
		}
		wf := wf
		go func() {
			e.slots <- struct{}{}
			defer func() { <-e.slots }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if _, err := e.Run(ctx, &wf, g, trig, false); err != nil {
				log.Printf("[Workflow] %q failed: %v", wf.Name, err)
			}
		}()
	}
}

// Run executes a workflow. In a dry run SMS and non-GET HTTP steps are simulated.
func (e *Engine) Run(ctx context.Context, wf *domain.Workflow, g *Graph, trig Trigger, dryRun bool) (*domain.WorkflowRun, error) {
	run := &domain.WorkflowRun{
		ID:               uuid.New().String(),
		WorkflowID:       wf.ID,
		UserID:           wf.UserID,
		TriggerMessageID: trig.MessageID,
		Status:           "running",
		DryRun:           dryRun,
		StartedAt:        time.Now().UTC(),
	}
	_ = e.store.SaveWorkflowRun(run)

	vars := map[string]string{
		"trigger.body":        trig.Body,
		"trigger.sender":      trig.Sender,
		"trigger.device_id":   trig.DeviceID,
		"trigger.sim_slot":    strconv.Itoa(trig.SimSlot),
		"trigger.message_id":  trig.MessageID,
		"trigger.received_at": trig.At.Format(time.RFC3339),
	}

	var steps []Step
	runErr := e.walk(ctx, wf, g, trig, dryRun, vars, &steps)

	now := time.Now().UTC()
	run.FinishedAt = &now
	run.Status = "success"
	if runErr != nil {
		run.Status = "failed"
		run.Error = runErr.Error()
	}
	run.Steps, _ = json.Marshal(steps)
	if err := e.store.SaveWorkflowRun(run); err != nil {
		log.Printf("[Workflow] saving run failed: %v", err)
	}
	if e.notifier != nil {
		e.notifier.BroadcastToDashboards(wf.UserID, "workflow.run", map[string]interface{}{
			"id": run.ID, "workflow_id": wf.ID, "workflow_name": wf.Name, "status": run.Status, "error": run.Error, "dry_run": dryRun,
		})
	}
	return run, nil
}

func (e *Engine) walk(ctx context.Context, wf *domain.Workflow, g *Graph, trig Trigger, dryRun bool, vars map[string]string, steps *[]Step) error {
	start := g.trigger()
	if start == nil {
		return errors.New("workflow has no trigger")
	}
	queue := e.next(g, start.ID, "")
	for len(queue) > 0 {
		if len(*steps) >= maxSteps {
			return fmt.Errorf("stopped after %d steps (does the graph loop?)", maxSteps)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		n := g.node(queue[0])
		queue = queue[1:]
		if n == nil {
			continue
		}

		began := time.Now()
		step := Step{NodeID: n.ID, Type: n.Type, Label: nodeLabel(n), Status: "success"}
		out, branch, err := e.exec(ctx, wf, n, trig, dryRun, vars)
		step.DurationMs = time.Since(began).Milliseconds()
		step.Output = out
		for k, v := range out {
			vars[n.ID+"."+k] = v
		}
		if out["simulated"] == "true" {
			step.Status = "simulated"
		}
		if err != nil {
			step.Status = "failed"
			step.Error = err.Error()
			*steps = append(*steps, step)
			return fmt.Errorf("%s: %w", step.Label, err)
		}
		*steps = append(*steps, step)
		queue = append(queue, e.next(g, n.ID, branch)...)
	}
	return nil
}

// next returns targets of a node's outgoing edges; for conditions only the taken branch.
func (e *Engine) next(g *Graph, nodeID, branch string) []string {
	var ids []string
	for _, edge := range g.Edges {
		if edge.Source != nodeID {
			continue
		}
		if branch != "" && edge.SourceHandle != branch {
			continue
		}
		ids = append(ids, edge.Target)
	}
	return ids
}

func (e *Engine) exec(ctx context.Context, wf *domain.Workflow, n *Node, trig Trigger, dryRun bool, vars map[string]string) (map[string]string, string, error) {
	switch n.Type {
	case NodeAI:
		return e.execAI(ctx, wf, n, trig, vars)
	case NodeIf:
		return execCondition(n, vars)
	case NodeSendSMS:
		return e.execSendSMS(wf, n, trig, dryRun, vars)
	case NodeHTTP:
		return e.execHTTP(ctx, n, dryRun, vars)
	case NodeDelay:
		secs := intVal(n.Data, "seconds", 5)
		if secs > maxDelaySeconds {
			secs = maxDelaySeconds
		}
		if dryRun {
			return map[string]string{"waited": "0", "simulated": "true"}, "", nil
		}
		select {
		case <-time.After(time.Duration(secs) * time.Second):
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
		return map[string]string{"waited": strconv.Itoa(secs)}, "", nil
	case NodeTrigger:
		return nil, "", nil
	}
	return nil, "", fmt.Errorf("unknown node type %q", n.Type)
}

func (e *Engine) execAI(ctx context.Context, wf *domain.Workflow, n *Node, trig Trigger, vars map[string]string) (map[string]string, string, error) {
	cred, err := e.store.GetAICredential(str(n.Data, "credentialId"))
	if err != nil || cred == nil || cred.UserID != wf.UserID {
		return nil, "", errors.New("the selected API key no longer exists")
	}
	key, err := e.box.Open(cred.EncryptedKey)
	if err != nil {
		return nil, "", err
	}

	prompt := Render(firstNonEmpty(str(n.Data, "prompt"), "{{trigger.body}}"), vars)
	var history []ai.Message
	if boolean(n.Data, "memory") && trig.Sender != "" {
		history = e.conversation(wf.UserID, trig.Sender, trig.MessageID, intVal(n.Data, "memoryTurns", 10))
	}
	history = append(history, ai.Message{Role: "user", Content: prompt})

	res, err := ai.Complete(ctx, ai.Request{
		Provider:  cred.Provider,
		APIKey:    key,
		BaseURL:   cred.BaseURL,
		Model:     str(n.Data, "model"),
		System:    Render(str(n.Data, "systemPrompt"), vars),
		Messages:  history,
		MaxTokens: intVal(n.Data, "maxTokens", 1024),
	})
	if err != nil {
		return nil, "", err
	}
	return map[string]string{
		"text":          res.Text,
		"model":         res.Model,
		"input_tokens":  strconv.Itoa(res.InputTokens),
		"output_tokens": strconv.Itoa(res.OutputTokens),
	}, "", nil
}

// conversation loads earlier SMS with this contact, oldest first, as chat turns.
func (e *Engine) conversation(owner, contact, excludeID string, turns int) []ai.Message {
	if turns <= 0 {
		return nil
	}
	if turns > 50 {
		turns = 50
	}
	msgs, _, err := e.store.ListMessages(storage.MessageFilter{Owner: owner, Contact: contact, Limit: turns + 1})
	if err != nil {
		return nil
	}
	var history []ai.Message
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.ID == excludeID || m.Status == domain.MessageStatusFailed {
			continue
		}
		role := "user"
		if m.Direction == domain.DirectionOutbound {
			role = "assistant"
		}
		history = append(history, ai.Message{Role: role, Content: m.Body})
	}
	if len(history) > turns {
		history = history[len(history)-turns:]
	}
	return history
}

func execCondition(n *Node, vars map[string]string) (map[string]string, string, error) {
	value := Render(str(n.Data, "value"), vars)
	compare := Render(str(n.Data, "compare"), vars)
	v, c := value, compare
	if !boolean(n.Data, "caseSensitive") {
		v, c = strings.ToLower(value), strings.ToLower(compare)
	}
	var ok bool
	switch str(n.Data, "operator") {
	case "equals":
		ok = strings.TrimSpace(v) == strings.TrimSpace(c)
	case "not_contains":
		ok = !strings.Contains(v, c)
	case "starts_with":
		ok = strings.HasPrefix(strings.TrimSpace(v), c)
	case "regex":
		re, err := regexp.Compile(compare)
		if err != nil {
			return nil, "", fmt.Errorf("invalid regex: %w", err)
		}
		ok = re.MatchString(value)
	case "is_empty":
		ok = strings.TrimSpace(value) == ""
	case "not_empty":
		ok = strings.TrimSpace(value) != ""
	default: // contains
		ok = strings.Contains(v, c)
	}
	branch := "false"
	if ok {
		branch = "true"
	}
	return map[string]string{"result": branch, "value": truncate(value, 500)}, branch, nil
}

func (e *Engine) execSendSMS(wf *domain.Workflow, n *Node, trig Trigger, dryRun bool, vars map[string]string) (map[string]string, string, error) {
	to := strings.TrimSpace(Render(firstNonEmpty(str(n.Data, "to"), "{{trigger.sender}}"), vars))
	body := strings.TrimSpace(Render(str(n.Data, "body"), vars))
	if to == "" {
		return nil, "", errors.New("no recipient")
	}
	if body == "" {
		return nil, "", errors.New("message text rendered empty")
	}
	if len([]rune(body)) > 1600 {
		body = string([]rune(body)[:1600])
	}
	deviceID := firstNonEmpty(str(n.Data, "deviceId"), trig.DeviceID)
	simSlot := intVal(n.Data, "simSlot", -1)
	if simSlot < 0 {
		simSlot = trig.SimSlot
	}
	out := map[string]string{"to": to, "body": body}
	if dryRun {
		out["simulated"] = "true"
		return out, "", nil
	}
	if e.sender == nil {
		return nil, "", errors.New("SMS sending is not available")
	}
	id, err := e.sender.SendFromWorkflow(wf.UserID, deviceID, simSlot, to, body)
	if err != nil {
		return nil, "", err
	}
	out["message_id"] = id
	return out, "", nil
}

func (e *Engine) execHTTP(ctx context.Context, n *Node, dryRun bool, vars map[string]string) (map[string]string, string, error) {
	method := strings.ToUpper(firstNonEmpty(str(n.Data, "method"), "POST"))
	target := Render(str(n.Data, "url"), vars)
	body := Render(str(n.Data, "body"), vars)
	if dryRun && method != http.MethodGet {
		return map[string]string{"url": target, "method": method, "request_body": truncate(body, 1000), "simulated": "true"}, "", nil
	}

	var reader io.Reader
	if body != "" && method != http.MethodGet {
		reader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, "", fmt.Errorf("bad request: %w", err)
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "HttpSMS-Workflow/1.0")
	for _, line := range strings.Split(Render(str(n.Data, "headers"), vars), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) != "" {
			req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, httpBodyLimit))
	out := map[string]string{"status": strconv.Itoa(resp.StatusCode), "body": string(raw)}
	if resp.StatusCode >= 400 && !boolean(n.Data, "continueOnError") {
		return out, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return out, "", nil
}

var templateVar = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.\-]+)\s*\}\}`)

// Render replaces {{name}} placeholders; unknown names become empty strings.
func Render(tpl string, vars map[string]string) string {
	return templateVar.ReplaceAllStringFunc(tpl, func(m string) string {
		return vars[templateVar.FindStringSubmatch(m)[1]]
	})
}

func nodeLabel(n *Node) string {
	if l := str(n.Data, "label"); l != "" {
		return l
	}
	return n.Type + " " + n.ID
}

func str(data map[string]interface{}, key string) string {
	switch v := data[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func boolean(data map[string]interface{}, key string) bool {
	b, _ := data[key].(bool)
	return b
}

func intVal(data map[string]interface{}, key string, def int) int {
	switch v := data[key].(type) {
	case float64:
		return int(v)
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return def
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
