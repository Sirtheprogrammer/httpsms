package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// MCP / JSON-RPC 2.0 Types
type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type RPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema ToolSchema  `json:"inputSchema"`
}

type ToolSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]PropertyDef `json:"properties"`
	Required   []string               `json:"required,omitempty"`
}

type PropertyDef struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
	Items       *PropertyDef `json:"items,omitempty"`
}

type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type ToolCallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type GatewayClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func newGatewayClient() *GatewayClient {
	baseURL := os.Getenv("HTTPSMS_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	apiKey := os.Getenv("HTTPSMS_API_KEY")
	if apiKey == "" {
		apiKey = "htsms_admin_secret_change_me"
	}

	return &GatewayClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *GatewayClient) doRequest(method, path string, body interface{}) (string, error) {
	url := fmt.Sprintf("%s%s", c.baseURL, path)

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "HttpSMS-MCP-Server/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("Gateway error (HTTP %d): %s", resp.StatusCode, string(respBytes))
	}

	return string(respBytes), nil
}

func getTools() []Tool {
	return []Tool{
		{
			Name:        "httpsms_send_sms",
			Description: "Send an outbound SMS message through a connected Android handset gateway. Supports dual SIM slots, character counting, and idempotency deduplication.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"recipient": {
						Type:        "string",
						Description: "Destination mobile phone number in E.164 format (e.g. '+15551234567').",
					},
					"body": {
						Type:        "string",
						Description: "SMS message text payload. GSM 7-bit allows 160 chars/part; Unicode allows 70 chars/part.",
					},
					"sim_slot": {
						Type:        "number",
						Description: "SIM slot index (0 for SIM 1, 1 for SIM 2). Defaults to 0.",
					},
					"device_id": {
						Type:        "string",
						Description: "Optional target Android device UUID. If omitted, gateway automatically routes to any online device.",
					},
					"idempotency_key": {
						Type:        "string",
						Description: "Optional unique transaction key. Prevents sending accidental duplicate messages on retries.",
					},
				},
				Required: []string{"recipient", "body"},
			},
		},
		{
			Name:        "httpsms_list_messages",
			Description: "List or search messages (inbound received SMS and outbound sent SMS) stored in the gateway database.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"direction": {
						Type:        "string",
						Description: "Filter by direction: 'inbound', 'outbound', or leave empty for all.",
						Enum:        []string{"inbound", "outbound"},
					},
					"search": {
						Type:        "string",
						Description: "Search text query matching sender, recipient, or message body content.",
					},
					"limit": {
						Type:        "number",
						Description: "Maximum number of messages to return (1-100, default: 20).",
					},
					"offset": {
						Type:        "number",
						Description: "Pagination offset index (default: 0).",
					},
				},
			},
		},
		{
			Name:        "httpsms_get_message",
			Description: "Retrieve detailed delivery telemetry for a specific message by its UUID.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"id": {
						Type:        "string",
						Description: "The message UUID.",
					},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "httpsms_list_devices",
			Description: "List all paired Android gateway handsets, their active SIM cards, battery %, charging state, signal level (dBm), and connection status.",
			InputSchema: ToolSchema{
				Type:       "object",
				Properties: map[string]PropertyDef{},
			},
		},
		{
			Name:        "httpsms_create_webhook",
			Description: "Register a webhook endpoint on the gateway to receive real-time signed HTTP POST notifications for SMS events.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"url": {
						Type:        "string",
						Description: "HTTPS destination webhook URL.",
					},
					"events": {
						Type:        "array",
						Description: "List of event types to subscribe to: 'sms.received', 'sms.sent', 'sms.delivered', 'sms.failed', 'device.heartbeat'. Use ['*'] for all.",
						Items: &PropertyDef{
							Type: "string",
						},
					},
					"secret": {
						Type:        "string",
						Description: "Optional secret for HMAC-SHA256 signature calculation (sent in X-Signature-SHA256 header).",
					},
				},
				Required: []string{"url"},
			},
		},
		{
			Name:        "httpsms_list_webhooks",
			Description: "List all configured webhooks and inspect their subscribed events and status.",
			InputSchema: ToolSchema{
				Type:       "object",
				Properties: map[string]PropertyDef{},
			},
		},
		{
			Name:        "httpsms_test_webhook",
			Description: "Trigger an immediate test ping delivery to a registered webhook and return latency and HTTP response status.",
			InputSchema: ToolSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"id": {
						Type:        "string",
						Description: "Webhook UUID to test.",
					},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "httpsms_get_stats",
			Description: "Retrieve high-level system metrics (online devices, total inbound SMS received, total outbound SMS sent, active webhooks, active workflows).",
			InputSchema: ToolSchema{
				Type:       "object",
				Properties: map[string]PropertyDef{},
			},
		},
		{
			Name:        "httpsms_list_workflows",
			Description: "List all configured automated SMS responder workflows and their AI prompts.",
			InputSchema: ToolSchema{
				Type:       "object",
				Properties: map[string]PropertyDef{},
			},
		},
		{
			Name:        "httpsms_list_workflow_runs",
			Description: "List execution logs and AI responses generated by automated SMS responder workflows.",
			InputSchema: ToolSchema{
				Type:       "object",
				Properties: map[string]PropertyDef{},
			},
		},
	}
}

func handleCallTool(client *GatewayClient, params CallToolParams) ToolCallResult {
	switch params.Name {
	case "httpsms_send_sms":
		recipient, _ := params.Arguments["recipient"].(string)
		body, _ := params.Arguments["body"].(string)
		simSlot := 0
		if s, ok := params.Arguments["sim_slot"].(float64); ok {
			simSlot = int(s)
		}
		deviceID, _ := params.Arguments["device_id"].(string)
		idempotencyKey, _ := params.Arguments["idempotency_key"].(string)

		payload := map[string]interface{}{
			"recipient":       recipient,
			"body":            body,
			"sim_slot":        simSlot,
			"device_id":       deviceID,
			"idempotency_key": idempotencyKey,
		}

		res, err := client.doRequest("POST", "/api/v1/sms/send", payload)
		if err != nil {
			return ToolCallResult{Content: []ToolContent{{Type: "text", Text: err.Error()}}, IsError: true}
		}
		return ToolCallResult{Content: []ToolContent{{Type: "text", Text: res}}}

	case "httpsms_list_messages":
		path := "/api/v1/messages?"
		if dir, ok := params.Arguments["direction"].(string); ok && dir != "" {
			path += "direction=" + dir + "&"
		}
		if search, ok := params.Arguments["search"].(string); ok && search != "" {
			path += "search=" + search + "&"
		}
		limit := 20
		if l, ok := params.Arguments["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		path += "limit=" + strconv.Itoa(limit)

		if off, ok := params.Arguments["offset"].(float64); ok && off > 0 {
			path += "&offset=" + strconv.Itoa(int(off))
		}

		res, err := client.doRequest("GET", path, nil)
		if err != nil {
			return ToolCallResult{Content: []ToolContent{{Type: "text", Text: err.Error()}}, IsError: true}
		}
		return ToolCallResult{Content: []ToolContent{{Type: "text", Text: res}}}

	case "httpsms_get_message":
		id, _ := params.Arguments["id"].(string)
		if id == "" {
			return ToolCallResult{Content: []ToolContent{{Type: "text", Text: "Message 'id' is required"}}, IsError: true}
		}
		res, err := client.doRequest("GET", "/api/v1/messages/"+id, nil)
		if err != nil {
			return ToolCallResult{Content: []ToolContent{{Type: "text", Text: err.Error()}}, IsError: true}
		}
		return ToolCallResult{Content: []ToolContent{{Type: "text", Text: res}}}

	case "httpsms_list_devices":
		res, err := client.doRequest("GET", "/api/v1/devices", nil)
		if err != nil {
			return ToolCallResult{Content: []ToolContent{{Type: "text", Text: err.Error()}}, IsError: true}
		}
		return ToolCallResult{Content: []ToolContent{{Type: "text", Text: res}}}

	case "httpsms_create_webhook":
		url, _ := params.Arguments["url"].(string)
		secret, _ := params.Arguments["secret"].(string)
		var events []string
		if evs, ok := params.Arguments["events"].([]interface{}); ok {
			for _, e := range evs {
				if es, ok := e.(string); ok {
					events = append(events, es)
				}
			}
		}

		payload := map[string]interface{}{
			"url":    url,
			"secret": secret,
			"events": events,
		}
		res, err := client.doRequest("POST", "/api/v1/webhooks", payload)
		if err != nil {
			return ToolCallResult{Content: []ToolContent{{Type: "text", Text: err.Error()}}, IsError: true}
		}
		return ToolCallResult{Content: []ToolContent{{Type: "text", Text: res}}}

	case "httpsms_list_webhooks":
		res, err := client.doRequest("GET", "/api/v1/webhooks", nil)
		if err != nil {
			return ToolCallResult{Content: []ToolContent{{Type: "text", Text: err.Error()}}, IsError: true}
		}
		return ToolCallResult{Content: []ToolContent{{Type: "text", Text: res}}}

	case "httpsms_test_webhook":
		id, _ := params.Arguments["id"].(string)
		res, err := client.doRequest("POST", "/api/v1/webhooks/"+id+"/test", nil)
		if err != nil {
			return ToolCallResult{Content: []ToolContent{{Type: "text", Text: err.Error()}}, IsError: true}
		}
		return ToolCallResult{Content: []ToolContent{{Type: "text", Text: res}}}

	case "httpsms_get_stats":
		res, err := client.doRequest("GET", "/api/v1/system/stats", nil)
		if err != nil {
			return ToolCallResult{Content: []ToolContent{{Type: "text", Text: err.Error()}}, IsError: true}
		}
		return ToolCallResult{Content: []ToolContent{{Type: "text", Text: res}}}

	case "httpsms_list_workflows":
		res, err := client.doRequest("GET", "/api/v1/workflows", nil)
		if err != nil {
			return ToolCallResult{Content: []ToolContent{{Type: "text", Text: err.Error()}}, IsError: true}
		}
		return ToolCallResult{Content: []ToolContent{{Type: "text", Text: res}}}

	case "httpsms_list_workflow_runs":
		res, err := client.doRequest("GET", "/api/v1/workflow-runs", nil)
		if err != nil {
			return ToolCallResult{Content: []ToolContent{{Type: "text", Text: err.Error()}}, IsError: true}
		}
		return ToolCallResult{Content: []ToolContent{{Type: "text", Text: res}}}

	default:
		return ToolCallResult{
			Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Unknown tool '%s'", params.Name)}},
			IsError: true,
		}
	}
}

func main() {
	client := newGatewayClient()
	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req RPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		var resp RPCResponse
		resp.JSONRPC = "2.0"
		resp.ID = req.ID

		switch req.Method {
		case "initialize":
			resp.Result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				"serverInfo": map[string]interface{}{
					"name":    "httpsms-gateway-mcp",
					"version": "1.0.0",
				},
			}

		case "notifications/initialized":
			// Client initialized notification, no response needed for notifications
			continue

		case "tools/list":
			resp.Result = map[string]interface{}{
				"tools": getTools(),
			}

		case "tools/call":
			var params CallToolParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				resp.Error = &RPCError{Code: -32602, Message: "Invalid tool params"}
			} else {
				res := handleCallTool(client, params)
				resp.Result = res
			}

		default:
			resp.Error = &RPCError{Code: -32601, Message: fmt.Sprintf("Method '%s' not found", req.Method)}
		}

		out, _ := json.Marshal(resp)
		os.Stdout.Write(out)
		os.Stdout.Write([]byte("\n"))
	}
}
