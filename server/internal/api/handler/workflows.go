package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/httpsms/gateway-server/internal/ai"
	"github.com/httpsms/gateway-server/internal/domain"
	"github.com/httpsms/gateway-server/internal/workflow"
)

// --- AI provider keys (BYOK) ---

func (h *APIHandler) ListAIProviders(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, ai.Providers)
}

// ListAICredentials returns only the caller's keys: provider keys stay private even from admins.
func (h *APIHandler) ListAICredentials(w http.ResponseWriter, r *http.Request) {
	creds, err := h.store.ListAICredentials(principal(r).UserID())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, creds)
}

func keyHint(key string) string {
	if len(key) <= 8 {
		return "••••"
	}
	return "••••" + key[len(key)-4:]
}

// CreateAICredential verifies the key by listing the provider's models, then stores it encrypted.
func (h *APIHandler) CreateAICredential(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.User == nil {
		WriteError(w, http.StatusBadRequest, "NO_ACCOUNT", "Sign in with a user account to add API keys")
		return
	}
	var req struct {
		Provider string `json:"provider"`
		Label    string `json:"label"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
		SkipTest bool   `json:"skip_test"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.BaseURL = strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if !ai.IsProvider(req.Provider) {
		WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Unknown provider")
		return
	}
	if req.APIKey == "" {
		WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "API key is required")
		return
	}
	if req.BaseURL != "" && !strings.HasPrefix(req.BaseURL, "https://") && !strings.HasPrefix(req.BaseURL, "http://") {
		WriteError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Base URL must start with https://")
		return
	}

	var models []string
	if !req.SkipTest {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		var err error
		models, err = ai.ListModels(ctx, req.Provider, req.APIKey, req.BaseURL)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "KEY_CHECK_FAILED", err.Error())
			return
		}
	}

	sealed, err := h.box.Seal(req.APIKey)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not encrypt key")
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = req.Provider + " key"
	}
	cred := domain.AICredential{
		ID:           uuid.New().String(),
		UserID:       p.User.ID,
		Provider:     req.Provider,
		Label:        label,
		BaseURL:      req.BaseURL,
		KeyHint:      keyHint(req.APIKey),
		EncryptedKey: sealed,
		CreatedAt:    time.Now().UTC(),
	}
	if err := h.store.CreateAICredential(&cred); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]interface{}{"credential": cred, "models": models})
}

func (h *APIHandler) ownCredential(w http.ResponseWriter, r *http.Request) *domain.AICredential {
	cred, err := h.store.GetAICredential(chi.URLParam(r, "id"))
	if err != nil || cred == nil || cred.UserID != principal(r).UserID() {
		notFound(w, "API key")
		return nil
	}
	return cred
}

// ListCredentialModels handles GET /api/v1/ai/credentials/{id}/models for the model picker.
func (h *APIHandler) ListCredentialModels(w http.ResponseWriter, r *http.Request) {
	cred := h.ownCredential(w, r)
	if cred == nil {
		return
	}
	key, err := h.box.Open(cred.EncryptedKey)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "DECRYPT_FAILED", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	models, err := ai.ListModels(ctx, cred.Provider, key, cred.BaseURL)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "PROVIDER_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"provider": cred.Provider, "models": models})
}

func (h *APIHandler) DeleteAICredential(w http.ResponseWriter, r *http.Request) {
	cred := h.ownCredential(w, r)
	if cred == nil {
		return
	}
	if err := h.store.DeleteAICredential(cred.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- Workflows ---

type workflowView struct {
	domain.Workflow
	Problems []string            `json:"problems"`
	LastRun  *domain.WorkflowRun `json:"last_run,omitempty"`
}

func (h *APIHandler) view(wf *domain.Workflow, withLastRun bool) workflowView {
	v := workflowView{Workflow: *wf, Problems: []string{}}
	if g, err := workflow.ParseGraph(wf.Graph); err == nil {
		if p := workflow.Validate(g); p != nil {
			v.Problems = p
		}
	} else {
		v.Problems = []string{err.Error()}
	}
	if withLastRun {
		if runs, _ := h.store.ListWorkflowRuns("", wf.ID, 1); len(runs) > 0 {
			v.LastRun = &runs[0]
		}
	}
	return v
}

func (h *APIHandler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	wfs, err := h.store.ListWorkflows(principal(r).Owner(), false)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	out := make([]workflowView, 0, len(wfs))
	for i := range wfs {
		out = append(out, h.view(&wfs[i], true))
	}
	WriteJSON(w, http.StatusOK, out)
}

// defaultGraph is a new workflow's starting canvas: just the SMS trigger.
var defaultGraph = json.RawMessage(`{"nodes":[{"id":"trigger","type":"trigger","position":{"x":80,"y":160},"data":{"label":"Incoming SMS","match":"any"}}],"edges":[]}`)

func (h *APIHandler) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Graph       json.RawMessage `json:"graph"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Untitled workflow"
	}
	graph := req.Graph
	if len(graph) == 0 || string(graph) == "null" {
		graph = defaultGraph
	}
	if _, err := workflow.ParseGraph(graph); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_GRAPH", err.Error())
		return
	}
	now := time.Now().UTC()
	wf := domain.Workflow{
		ID:          uuid.New().String(),
		UserID:      principal(r).UserID(),
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		Graph:       graph,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := h.store.SaveWorkflow(&wf); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, h.view(&wf, false))
}

func (h *APIHandler) ownedWorkflow(w http.ResponseWriter, r *http.Request) *domain.Workflow {
	wf, err := h.store.GetWorkflow(chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return nil
	}
	if wf == nil || !principal(r).CanAccess(wf.UserID) {
		notFound(w, "Workflow")
		return nil
	}
	return wf
}

func (h *APIHandler) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	if wf := h.ownedWorkflow(w, r); wf != nil {
		WriteJSON(w, http.StatusOK, h.view(wf, true))
	}
}

// UpdateWorkflow saves the canvas. Enabling is refused while the graph has problems.
func (h *APIHandler) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	wf := h.ownedWorkflow(w, r)
	if wf == nil {
		return
	}
	var req struct {
		Name        *string         `json:"name"`
		Description *string         `json:"description"`
		Enabled     *bool           `json:"enabled"`
		Graph       json.RawMessage `json:"graph"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		wf.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		wf.Description = strings.TrimSpace(*req.Description)
	}
	if len(req.Graph) > 0 && string(req.Graph) != "null" {
		if _, err := workflow.ParseGraph(req.Graph); err != nil {
			WriteError(w, http.StatusBadRequest, "INVALID_GRAPH", err.Error())
			return
		}
		wf.Graph = req.Graph
	}
	if req.Enabled != nil {
		wf.Enabled = *req.Enabled
	}
	v := h.view(wf, false)
	if wf.Enabled && len(v.Problems) > 0 {
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error":    map[string]string{"code": "WORKFLOW_INVALID", "message": "Fix the problems before turning this workflow on"},
			"problems": v.Problems,
		})
		return
	}
	wf.UpdatedAt = time.Now().UTC()
	if err := h.store.SaveWorkflow(wf); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, v)
}

func (h *APIHandler) DeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	wf := h.ownedWorkflow(w, r)
	if wf == nil {
		return
	}
	if err := h.store.DeleteWorkflow(wf.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// TestWorkflow runs the saved graph (or an unsaved one from the editor) against a
// sample SMS. SMS and non-GET HTTP steps are simulated; AI steps really run.
func (h *APIHandler) TestWorkflow(w http.ResponseWriter, r *http.Request) {
	wf := h.ownedWorkflow(w, r)
	if wf == nil {
		return
	}
	var req struct {
		Sender string          `json:"sender"`
		Body   string          `json:"body"`
		Graph  json.RawMessage `json:"graph"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Graph) > 0 && string(req.Graph) != "null" {
		wf.Graph = req.Graph
	}
	g, err := workflow.ParseGraph(wf.Graph)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_GRAPH", err.Error())
		return
	}
	trig := workflow.Trigger{
		Sender: strings.TrimSpace(req.Sender),
		Body:   req.Body,
		At:     time.Now().UTC(),
	}
	if trig.Sender == "" {
		trig.Sender = "+10000000000"
	}

	matched := true
	for _, n := range g.Nodes {
		if n.Type == workflow.NodeTrigger {
			matched = workflow.TriggerMatches(&n, trig)
		}
	}
	if !matched {
		WriteJSON(w, http.StatusOK, map[string]interface{}{"matched": false})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	run, err := h.engine.Run(ctx, wf, g, trig, true)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "RUN_FAILED", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"matched": true, "run": run})
}

func (h *APIHandler) ListWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	workflowID := chi.URLParam(r, "id")
	if workflowID != "" && h.ownedWorkflow(w, r) == nil {
		return
	}
	runs, err := h.store.ListWorkflowRuns(principal(r).Owner(), workflowID, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, runs)
}
