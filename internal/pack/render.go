package pack

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

const UserPromptMarker = "<!-- USER_PROMPT -->"

// PromptMessage representa uma mensagem histórica ou nova formatada para o prompt.
type PromptMessage struct {
	Sender    string `json:"sender"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

// KnowledgeDoc representa um documento de conhecimento carregado do tenant.
type KnowledgeDoc struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// PromptInput carrega todo o estado estruturado e conhecimento necessários para renderizar um template do pack.
type PromptInput struct {
	Pack           *Pack
	Summary        string
	Facts          map[string]string
	Signals        map[string]bool
	Context        map[string]map[string]string
	RecentMessages []PromptMessage
	NewMessage     PromptMessage
	Knowledge      []KnowledgeDoc
	QAContent      string
	OutputSchema   string
	PlainTextOnly  bool
}

// RenderedPrompt separa o prompt em instruções de sistema e entrada do turno atual.
type RenderedPrompt struct {
	System string `json:"system"`
	User   string `json:"user"`
}

// RenderAnalysis monta o prompt de análise a partir do template do pack.
func RenderAnalysis(p *Pack, input PromptInput) (RenderedPrompt, error) {
	if p == nil || p.AnalysisTmpl == nil {
		return RenderedPrompt{}, fmt.Errorf("%w: template de análise não carregado", ErrInvalidPack)
	}
	if strings.TrimSpace(input.OutputSchema) == "" {
		rawSchema, err := BuildAnalysisSchema(p)
		if err != nil {
			return RenderedPrompt{}, err
		}
		input.OutputSchema = string(rawSchema)
	}
	input.Pack = p
	return executeTemplate(p.AnalysisTmpl, input)
}

// RenderReply monta o prompt de resposta ao cliente a partir do template do pack.
func RenderReply(p *Pack, input PromptInput) (RenderedPrompt, error) {
	if p == nil || p.ReplyTmpl == nil {
		return RenderedPrompt{}, fmt.Errorf("%w: template de resposta não carregado", ErrInvalidPack)
	}
	input.Pack = p
	return executeTemplate(p.ReplyTmpl, input)
}

// RenderNudge monta o prompt de follow-up proativo a partir do template do pack.
func RenderNudge(p *Pack, input PromptInput) (RenderedPrompt, error) {
	if p == nil || p.NudgeTmpl == nil {
		return RenderedPrompt{}, fmt.Errorf("%w: template de nudge não carregado", ErrInvalidPack)
	}
	input.Pack = p
	return executeTemplate(p.NudgeTmpl, input)
}

func executeTemplate(tmpl *template.Template, input PromptInput) (RenderedPrompt, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, input); err != nil {
		return RenderedPrompt{}, fmt.Errorf("erro ao renderizar template %s: %w", tmpl.Name(), err)
	}

	rendered := buf.String()
	parts := strings.SplitN(rendered, UserPromptMarker, 2)
	if len(parts) == 2 {
		return RenderedPrompt{
			System: strings.TrimSpace(parts[0]),
			User:   strings.TrimSpace(parts[1]),
		}, nil
	}
	return RenderedPrompt{
		System: strings.TrimSpace(rendered),
	}, nil
}
