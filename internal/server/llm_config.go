package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/llm"
)

var validLLMStages = []string{"analysis", "response", "nudge", "learning", "vision", "audio"}

func isValidLLMStage(stage string) bool {
	for _, s := range validLLMStages {
		if s == stage {
			return true
		}
	}
	return false
}

func (s *Server) handleGetLLMPresets(w http.ResponseWriter, r *http.Request) {
	var activeProviders []string
	defaultProvider := s.cfg.LLMProvider
	if s.deps.LLMRouter != nil {
		activeProviders = s.deps.LLMRouter.ActiveProviders()
		defaultProvider = s.deps.LLMRouter.DefaultProvider()
	} else if defaultProvider != "" {
		activeProviders = []string{defaultProvider}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"default_provider": defaultProvider,
		"active_providers": activeProviders,
		"stages":           validLLMStages,
		"presets":          llm.Presets(),
	})
}

func (s *Server) loadTenantPolicyForLLM(ctx context.Context, tenantID string) (core.TenantPolicy, error) {
	fallback := brain.NewLocalTenantLoader(s.cfg.TenantsDir)
	if s.deps.Store != nil {
		return brain.NewStoreTenantPolicyLoader(s.deps.Store, fallback).LoadTenant(ctx, tenantID)
	}
	return fallback.LoadTenant(ctx, tenantID)
}

func (s *Server) buildLLMConfigResponse(tenantID string, policy core.TenantPolicy) map[string]any {
	var activeProviders []string
	defaultProvider := s.cfg.LLMProvider
	defaults := map[string]core.StageLLMConfig{
		"analysis": {Provider: s.cfg.LLMProvider, Model: s.cfg.AnalysisModel},
		"response": {Provider: s.cfg.LLMProvider, Model: s.cfg.ResponseModel},
		"nudge":    {Provider: s.cfg.LLMProvider, Model: s.cfg.NudgeModel},
		"learning": {Provider: s.cfg.LLMProvider, Model: s.cfg.LearningModel},
		"vision":   {Provider: s.cfg.LLMProvider, Model: s.cfg.VisionModel},
		"audio":    {Provider: s.cfg.LLMProvider, Model: s.cfg.AudioModel},
	}
	effective := make(map[string]core.StageLLMConfig, len(validLLMStages))

	if s.deps.LLMRouter != nil {
		activeProviders = s.deps.LLMRouter.ActiveProviders()
		defaultProvider = s.deps.LLMRouter.DefaultProvider()
		defaults = s.deps.LLMRouter.StageDefaults()
		for _, stage := range validLLMStages {
			effective[stage] = s.deps.LLMRouter.Resolve(stage, policy.LLMOverrides)
		}
	} else {
		if defaultProvider != "" {
			activeProviders = []string{defaultProvider}
		}
		for _, stage := range validLLMStages {
			eff := defaults[stage]
			if ov, ok := policy.LLMOverrides[stage]; ok {
				if ov.Provider != "" {
					eff.Provider = ov.Provider
				}
				if ov.Model != "" {
					eff.Model = ov.Model
				}
			}
			effective[stage] = eff
		}
	}

	overrides := policy.LLMOverrides
	if overrides == nil {
		overrides = map[string]core.StageLLMConfig{}
	}

	return map[string]any{
		"tenant_id":        tenantID,
		"default_provider": defaultProvider,
		"active_providers": activeProviders,
		"defaults":         defaults,
		"overrides":        overrides,
		"effective":        effective,
	}
}

func (s *Server) handleGetLLMConfig(w http.ResponseWriter, r *http.Request) {
	tenantID := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenantID == "" {
		writeJSONError(w, http.StatusBadRequest, "parâmetro 'tenant' é obrigatório")
		return
	}
	policy, err := s.loadTenantPolicyForLLM(r.Context(), tenantID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("tenant %q não encontrado: %v", tenantID, err))
		return
	}
	writeJSON(w, http.StatusOK, s.buildLLMConfigResponse(tenantID, policy))
}

func (s *Server) handlePutLLMConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não inicializado")
		return
	}
	tenantID := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenantID == "" {
		writeJSONError(w, http.StatusBadRequest, "parâmetro 'tenant' é obrigatório")
		return
	}

	var req struct {
		Overrides map[string]core.StageLLMConfig `json:"overrides"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	var activeSet map[string]bool
	if s.deps.LLMRouter != nil {
		active := s.deps.LLMRouter.ActiveProviders()
		activeSet = make(map[string]bool, len(active))
		for _, p := range active {
			activeSet[p] = true
		}
	}

	cleaned := make(map[string]core.StageLLMConfig)
	for stage, ov := range req.Overrides {
		if !isValidLLMStage(stage) {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("estágio inválido: %q", stage))
			return
		}
		p := strings.TrimSpace(ov.Provider)
		m := strings.TrimSpace(ov.Model)
		if p == "" && m == "" {
			continue
		}
		if p != "" && activeSet != nil && !activeSet[p] {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("provedor %q não está configurado no ambiente", p))
			return
		}
		cleaned[stage] = core.StageLLMConfig{Provider: p, Model: m}
	}

	policy, err := s.loadTenantPolicyForLLM(r.Context(), tenantID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("tenant %q não encontrado: %v", tenantID, err))
		return
	}
	policy.LLMOverrides = cleaned
	if err := brain.SeedTenantPolicy(r.Context(), s.deps.Store, policy); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("falha ao salvar configuração LLM: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, s.buildLLMConfigResponse(tenantID, policy))
}
