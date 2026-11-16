package pack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

// FactType define os tipos suportados para fatos extraídos pelo pack.
type FactType string

const (
	FactTypeString  FactType = "string"
	FactTypeNumber  FactType = "number"
	FactTypeInteger FactType = "integer"
	FactTypeBoolean FactType = "boolean"
	FactTypeEnum    FactType = "enum"
)

func (t FactType) IsValid() bool {
	switch t {
	case FactTypeString, FactTypeNumber, FactTypeInteger, FactTypeBoolean, FactTypeEnum:
		return true
	default:
		return false
	}
}

// ActorsDef identifica os papéis na conversa (ex: agent/customer, support/client).
type ActorsDef struct {
	Business string `yaml:"business" json:"business"`
	Customer string `yaml:"customer" json:"customer"`
}

// FactDef define um fato extraível configurável no pack.yaml.
type FactDef struct {
	Key         string   `yaml:"key" json:"key"`
	Type        FactType `yaml:"type" json:"type"`
	Description string   `yaml:"description" json:"description"`
	Values      []string `yaml:"values,omitempty" json:"values,omitempty"`
}

// CollectionDef define uma coleção repetível de entidades tipadas com id ou chave natural (WIN-05).
type CollectionDef struct {
	Key         string    `yaml:"key" json:"key"`
	Description string    `yaml:"description,omitempty" json:"description,omitempty"`
	IDField     string    `yaml:"id_field,omitempty" json:"id_field,omitempty"`
	NaturalKey  []string  `yaml:"natural_key,omitempty" json:"natural_key,omitempty"`
	Fields      []FactDef `yaml:"fields" json:"fields"`
}

// SignalDef define um sinal booleano configurável no pack.yaml.
type SignalDef struct {
	Key         string `yaml:"key" json:"key"`
	Description string `yaml:"description" json:"description"`
}

// DimensionDef define uma dimensão categórica extraída pelo sensor (ex: sentiment, urgency).
type DimensionDef struct {
	Key         string   `yaml:"key" json:"key"`
	Values      []string `yaml:"values" json:"values"`
	Default     string   `yaml:"default,omitempty" json:"default,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
}

// IntentAction define a ação padrão associada a uma intenção classificada.
type IntentAction string

const (
	IntentActionReply      IntentAction = "reply"
	IntentActionEscalate   IntentAction = "escalate"
	IntentActionCreateTask IntentAction = "create_task"
	IntentActionHandoff    IntentAction = "handoff"
	IntentActionOptOut     IntentAction = "opt_out"
)

func (a IntentAction) IsValid() bool {
	switch a {
	case IntentActionReply, IntentActionEscalate, IntentActionCreateTask, IntentActionHandoff, IntentActionOptOut:
		return true
	default:
		return false
	}
}

// IntentDef define uma intenção classificável pela etapa de análise.
type IntentDef struct {
	Name        string       `yaml:"name" json:"name"`
	Description string       `yaml:"description" json:"description"`
	Examples    []string     `yaml:"examples,omitempty" json:"examples,omitempty"`
	Action      IntentAction `yaml:"action" json:"action"`
}

// ContextFieldDef define um campo dentro de uma seção de contexto.
type ContextFieldDef struct {
	Key         string   `yaml:"key" json:"key"`
	Type        FactType `yaml:"type" json:"type"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Values      []string `yaml:"values,omitempty" json:"values,omitempty"`
}

// ContextSectionDef define uma seção de contexto estruturado (ex: schedule, call, last_action).
type ContextSectionDef struct {
	Key         string            `yaml:"key" json:"key"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Fields      []ContextFieldDef `yaml:"fields" json:"fields"`
}

// PromptsDef referencia os arquivos de template dentro da pasta do pack.
type PromptsDef struct {
	Analysis string `yaml:"analysis" json:"analysis"`
	Reply    string `yaml:"reply" json:"reply"`
	Nudge    string `yaml:"nudge" json:"nudge"`
}

const (
	DefaultDebounceMs      = 8000
	DefaultTypingMaxWaitMs = 16000
)

// TimingDef configura janelas de debounce e espera máxima de digitação por pack.
type TimingDef struct {
	DebounceMs      *int `yaml:"debounce_ms,omitempty" json:"debounce_ms,omitempty"`
	TypingMaxWaitMs *int `yaml:"typing_max_wait_ms,omitempty" json:"typing_max_wait_ms,omitempty"`
}

// Pack representa um vertical pack carregado e validado.
// TransitionCondition define os critérios de guarda para acionar uma transição de estado.
type TransitionCondition struct {
	Intent          string   `yaml:"intent,omitempty" json:"intent,omitempty"`
	RequiredFacts   []string `yaml:"required_facts,omitempty" json:"required_facts,omitempty"`
	RequiredSignals []string `yaml:"required_signals,omitempty" json:"required_signals,omitempty"`
	Urgency         string   `yaml:"urgency,omitempty" json:"urgency,omitempty"`
	Sentiment       string   `yaml:"sentiment,omitempty" json:"sentiment,omitempty"`
}

// ActionDef define uma ação disparada por uma transição da FSM.
type ActionDef struct {
	Type   string         `yaml:"type" json:"type"` // message_template, knowledge_reply, escalate, handoff, opt_out
	Text   string         `yaml:"text,omitempty" json:"text,omitempty"`
	Params map[string]any `yaml:"params,omitempty" json:"params,omitempty"`
}

func (a ActionDef) IsValid() bool {
	switch a.Type {
	case "message_template", "knowledge_reply", "escalate", "handoff", "opt_out":
		return true
	default:
		return false
	}
}

// TransitionDef define uma transição de estado da FSM.
type TransitionDef struct {
	From   string              `yaml:"from" json:"from"`
	To     string              `yaml:"to" json:"to"`
	When   TransitionCondition `yaml:"when" json:"when"`
	Action string              `yaml:"action" json:"action"`
}

// FSMDef declara a máquina de estados finitos do pack.
type FSMDef struct {
	InitialState string               `yaml:"initial_state" json:"initial_state"`
	States       []string             `yaml:"states" json:"states"`
	Transitions  []TransitionDef      `yaml:"transitions" json:"transitions"`
	Actions      map[string]ActionDef `yaml:"actions" json:"actions"`
}

// TaskRuleDef define uma regra declarativa para derivar tarefas a partir de sinais, intenções e contexto.
type TaskRuleDef struct {
	Type           string `yaml:"type" json:"type"`
	Title          string `yaml:"title" json:"title"`
	TriggerSignal  string `yaml:"trigger_signal,omitempty" json:"trigger_signal,omitempty"`
	TriggerIntent  string `yaml:"trigger_intent,omitempty" json:"trigger_intent,omitempty"`
	ContextSection string `yaml:"context_section,omitempty" json:"context_section,omitempty"`
	DueInMinutes   int    `yaml:"due_in_minutes,omitempty" json:"due_in_minutes,omitempty"`
}

// ExposedFieldsDef define a allowlist de campos do pack expostos externamente (ex.: external_agent).
type ExposedFieldsDef struct {
	Facts           []string `yaml:"facts,omitempty" json:"facts,omitempty"`
	Signals         []string `yaml:"signals,omitempty" json:"signals,omitempty"`
	ContextSections []string `yaml:"context_sections,omitempty" json:"context_sections,omitempty"`
}

// ContextViewDef configura blocos, formato e orçamento padrão de uma view do módulo de contexto.
type ContextViewDef struct {
	Blocks          []string `yaml:"blocks,omitempty" json:"blocks,omitempty"`
	Format          string   `yaml:"format,omitempty" json:"format,omitempty"`
	MaxTokens       int      `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	IncludeMessages *bool    `yaml:"include_messages,omitempty" json:"include_messages,omitempty"`
}

// ContextModuleDef declara no pack.yaml as views, providers habilitados e campos expostos (CTX-16).
type ContextModuleDef struct {
	Providers     []string                  `yaml:"providers,omitempty" json:"providers,omitempty"`
	ExposedFields ExposedFieldsDef          `yaml:"exposed_fields,omitempty" json:"exposed_fields,omitempty"`
	Views         map[string]ContextViewDef `yaml:"views,omitempty" json:"views,omitempty"`
}

// Pack representa um vertical pack carregado e validado.
type Pack struct {
	Name           string              `yaml:"name" json:"name"`
	Language       string              `yaml:"language" json:"language"`
	Actors         ActorsDef           `yaml:"actors" json:"actors"`
	Facts          []FactDef           `yaml:"facts" json:"facts"`
	Collections    []CollectionDef     `yaml:"collections,omitempty" json:"collections,omitempty"`
	Signals        []SignalDef         `yaml:"signals" json:"signals"`
	Dimensions     []DimensionDef      `yaml:"dimensions,omitempty" json:"dimensions,omitempty"`
	Intents        []IntentDef         `yaml:"intents,omitempty" json:"intents,omitempty"`
	TaskRules      []TaskRuleDef       `yaml:"task_rules,omitempty" json:"task_rules,omitempty"`
	OptOutKeywords []string            `yaml:"opt_out_keywords,omitempty" json:"opt_out_keywords,omitempty"`
	Context        []ContextSectionDef `yaml:"context" json:"context"`
	ContextModule  *ContextModuleDef   `yaml:"context_module,omitempty" json:"context_module,omitempty"`
	FSM            *FSMDef                `yaml:"fsm,omitempty" json:"fsm,omitempty"`
	Journey        *JourneyDef            `yaml:"-" json:"journey,omitempty"`
	Journeys       map[string]*JourneyDef `yaml:"-" json:"journeys,omitempty"`
	Prompts        PromptsDef             `yaml:"prompts" json:"prompts"`
	Timing         *TimingDef             `yaml:"timing,omitempty" json:"timing,omitempty"`
	Rules          []string               `yaml:"rules,omitempty" json:"rules,omitempty"`
	Personality    string                 `yaml:"-" json:"personality,omitempty"`

	// Templates compilados na carga
	AnalysisTmpl *template.Template `yaml:"-" json:"-"`
	ReplyTmpl    *template.Template `yaml:"-" json:"-"`
	NudgeTmpl    *template.Template `yaml:"-" json:"-"`

	// Índices imutáveis derivados para consulta O(1)
	factByKey    map[string]FactDef
	signalByKey  map[string]SignalDef
	intentByName map[string]IntentDef
	contextByKey map[string]ContextSectionDef
}

var (
	ErrInvalidPack = errors.New("pack inválido")
)

// ParsePackYAML decodifica e valida a definição estrutural de um pack sem ler templates do disco.
func ParsePackYAML(data []byte) (*Pack, error) {
	var p Pack
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%w: falha ao parsear pack.yaml: %v", ErrInvalidPack, err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	p.buildIndexes()
	return &p, nil
}

// LoadFromDir carrega pack.yaml e os templates de prompt a partir de um diretório.
func LoadFromDir(dir string) (*Pack, error) {
	yamlPath := filepath.Join(dir, "pack.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("%w: não foi possível ler %s: %v", ErrInvalidPack, yamlPath, err)
	}

	p, err := ParsePackYAML(data)
	if err != nil {
		return nil, err
	}

	if err := p.loadTemplates(dir); err != nil {
		return nil, err
	}
	journey, err := LoadJourneyFromFile(dir, p)
	if err != nil {
		return nil, err
	}
	journeys, err := LoadJourneysFromDir(dir, p)
	if err != nil {
		return nil, err
	}
	p.Journeys = journeys
	if journey == nil && len(journeys) > 0 {
		journey = journeys["support"]
	}
	p.Journey = journey

	persData, err := os.ReadFile(filepath.Join(dir, "personality.md"))
	if err == nil {
		p.Personality = strings.TrimSpace(string(persData))
	}

	return p, nil
}

// ResolveJourney retorna a jornada ativa para o workflow solicitado,
// com fallback para "support" e para o journey.yaml legado do pack.
func (p *Pack) ResolveJourney(workflow string) *JourneyDef {
	if p == nil {
		return nil
	}
	w := strings.TrimSpace(workflow)
	if w != "" && p.Journeys != nil {
		if j, ok := p.Journeys[w]; ok && j != nil {
			return j
		}
	}
	if p.Journeys != nil {
		if j, ok := p.Journeys["support"]; ok && j != nil {
			return j
		}
	}
	return p.Journey
}

func (p *Pack) loadTemplates(baseDir string) error {
	var err error
	p.AnalysisTmpl, err = parseTemplateFile(baseDir, p.Prompts.Analysis, "analysis")
	if err != nil {
		return err
	}
	p.ReplyTmpl, err = parseTemplateFile(baseDir, p.Prompts.Reply, "reply")
	if err != nil {
		return err
	}
	if strings.TrimSpace(p.Prompts.Nudge) != "" {
		p.NudgeTmpl, err = parseTemplateFile(baseDir, p.Prompts.Nudge, "nudge")
		if err != nil {
			return err
		}
	}
	return nil
}

func parseTemplateFile(baseDir, relPath, name string) (*template.Template, error) {
	if strings.TrimSpace(relPath) == "" {
		return nil, fmt.Errorf("%w: caminho do template %s ausente", ErrInvalidPack, name)
	}
	cleanBase := filepath.Clean(baseDir)
	fullPath := filepath.Clean(filepath.Join(cleanBase, relPath))
	if !strings.HasPrefix(fullPath, cleanBase+string(filepath.Separator)) {
		return nil, fmt.Errorf("%w: caminho de template fora do diretório do pack: %s", ErrInvalidPack, relPath)
	}
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("%w: erro ao ler template %s (%s): %v", ErrInvalidPack, name, fullPath, err)
	}
	tmpl, err := template.New(name).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: erro de sintaxe no template %s: %v", ErrInvalidPack, name, err)
	}
	return tmpl, nil
}

// Validate verifica a consistência estrutural da configuração do pack.
func (p *Pack) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name é obrigatório", ErrInvalidPack)
	}
	if strings.TrimSpace(p.Language) == "" {
		return fmt.Errorf("%w: language é obrigatório", ErrInvalidPack)
	}
	if strings.TrimSpace(p.Actors.Business) == "" || strings.TrimSpace(p.Actors.Customer) == "" {
		return fmt.Errorf("%w: actors.business e actors.customer são obrigatórios", ErrInvalidPack)
	}
	if p.Actors.Business == p.Actors.Customer {
		return fmt.Errorf("%w: actors.business e actors.customer devem ser distintos", ErrInvalidPack)
	}
	if strings.TrimSpace(p.Prompts.Analysis) == "" || strings.TrimSpace(p.Prompts.Reply) == "" {
		return fmt.Errorf("%w: prompts.analysis e prompts.reply são obrigatórios", ErrInvalidPack)
	}
	if p.Timing != nil {
		if p.Timing.DebounceMs != nil && *p.Timing.DebounceMs < 0 {
			return fmt.Errorf("%w: timing.debounce_ms não pode ser negativo", ErrInvalidPack)
		}
		if p.Timing.TypingMaxWaitMs != nil && *p.Timing.TypingMaxWaitMs < 0 {
			return fmt.Errorf("%w: timing.typing_max_wait_ms não pode ser negativo", ErrInvalidPack)
		}
	}

	seenFacts := make(map[string]bool)
	for _, f := range p.Facts {
		if strings.TrimSpace(f.Key) == "" {
			return fmt.Errorf("%w: fato com key vazia", ErrInvalidPack)
		}
		if seenFacts[f.Key] {
			return fmt.Errorf("%w: fato duplicado %q", ErrInvalidPack, f.Key)
		}
		seenFacts[f.Key] = true
		if !f.Type.IsValid() {
			return fmt.Errorf("%w: tipo inválido %q para o fato %q", ErrInvalidPack, f.Type, f.Key)
		}
		if f.Type == FactTypeEnum && len(f.Values) == 0 {
			return fmt.Errorf("%w: fato enum %q deve declarar values", ErrInvalidPack, f.Key)
		}
	}

	seenSignals := make(map[string]bool)
	for _, s := range p.Signals {
		if strings.TrimSpace(s.Key) == "" {
			return fmt.Errorf("%w: sinal com key vazia", ErrInvalidPack)
		}
		if seenSignals[s.Key] {
			return fmt.Errorf("%w: sinal duplicado %q", ErrInvalidPack, s.Key)
		}
		seenSignals[s.Key] = true
	}

	seenDims := make(map[string]bool)
	for _, d := range p.Dimensions {
		if strings.TrimSpace(d.Key) == "" {
			return fmt.Errorf("%w: dimensão com key vazia", ErrInvalidPack)
		}
		if seenDims[d.Key] {
			return fmt.Errorf("%w: dimensão duplicada %q", ErrInvalidPack, d.Key)
		}
		seenDims[d.Key] = true
		if len(d.Values) == 0 {
			return fmt.Errorf("%w: dimensão %q deve declarar values", ErrInvalidPack, d.Key)
		}
	}

	seenIntents := make(map[string]bool)
	for _, in := range p.Intents {
		if strings.TrimSpace(in.Name) == "" {
			return fmt.Errorf("%w: intenção com name vazio", ErrInvalidPack)
		}
		if seenIntents[in.Name] {
			return fmt.Errorf("%w: intenção duplicada %q", ErrInvalidPack, in.Name)
		}
		seenIntents[in.Name] = true
		if !in.Action.IsValid() {
			return fmt.Errorf("%w: action inválida %q para intenção %q", ErrInvalidPack, in.Action, in.Name)
		}
	}

	seenCtx := make(map[string]bool)
	for _, c := range p.Context {
		if strings.TrimSpace(c.Key) == "" {
			return fmt.Errorf("%w: seção de contexto com key vazia", ErrInvalidPack)
		}
		if seenCtx[c.Key] {
			return fmt.Errorf("%w: seção de contexto duplicada %q", ErrInvalidPack, c.Key)
		}
		seenCtx[c.Key] = true

		seenFields := make(map[string]bool)
		for _, field := range c.Fields {
			if strings.TrimSpace(field.Key) == "" {
				return fmt.Errorf("%w: campo vazio na seção de contexto %q", ErrInvalidPack, c.Key)
			}
			if seenFields[field.Key] {
				return fmt.Errorf("%w: campo duplicado %q na seção de contexto %q", ErrInvalidPack, field.Key, c.Key)
			}
			seenFields[field.Key] = true
			if !field.Type.IsValid() {
				return fmt.Errorf("%w: tipo inválido %q para %s.%s", ErrInvalidPack, field.Type, c.Key, field.Key)
			}
			if field.Type == FactTypeEnum && len(field.Values) == 0 {
				return fmt.Errorf("%w: campo enum %s.%s deve declarar values", ErrInvalidPack, c.Key, field.Key)
			}
		}
	}

	seenTaskTypes := make(map[string]bool)
	for _, tr := range p.TaskRules {
		if strings.TrimSpace(tr.Type) == "" || strings.TrimSpace(tr.Title) == "" {
			return fmt.Errorf("%w: task_rule exige type e title", ErrInvalidPack)
		}
		if seenTaskTypes[tr.Type] {
			return fmt.Errorf("%w: task_rule duplicada %q", ErrInvalidPack, tr.Type)
		}
		seenTaskTypes[tr.Type] = true
		if tr.TriggerSignal == "" && tr.TriggerIntent == "" {
			return fmt.Errorf("%w: task_rule %q exige trigger_signal ou trigger_intent", ErrInvalidPack, tr.Type)
		}
		if tr.TriggerSignal != "" && !seenSignals[tr.TriggerSignal] {
			return fmt.Errorf("%w: task_rule %q referencia sinal desconhecido %q", ErrInvalidPack, tr.Type, tr.TriggerSignal)
		}
		if tr.TriggerIntent != "" && !seenIntents[tr.TriggerIntent] {
			return fmt.Errorf("%w: task_rule %q referencia intenção desconhecida %q", ErrInvalidPack, tr.Type, tr.TriggerIntent)
		}
		if tr.ContextSection != "" && !seenCtx[tr.ContextSection] {
			return fmt.Errorf("%w: task_rule %q referencia contexto desconhecido %q", ErrInvalidPack, tr.Type, tr.ContextSection)
		}
	}

	if p.ContextModule != nil {
		if err := validateContextModule(p.ContextModule, seenFacts, seenSignals, seenCtx); err != nil {
			return err
		}
	}

	if p.FSM != nil {
		if err := validateFSM(p.FSM, seenFacts, seenSignals, seenIntents); err != nil {
			return err
		}
	}

	return nil
}

func validateContextModule(cm *ContextModuleDef, facts, signals, sections map[string]bool) error {
	validProviders := map[string]bool{
		"summary": true, "facts": true, "signals": true, "recent_messages": true, "knowledge": true,
	}
	validViews := map[string]bool{
		"analysis": true, "response": true, "admin": true, "external_agent": true, "summary_only": true,
	}
	validBlocks := map[string]bool{
		"summary": true, "facts": true, "signals": true, "structured_context": true,
		"recent_messages": true, "knowledge": true, "qa": true,
	}
	validFormats := map[string]bool{
		"": true, "prompt": true, "json": true, "markdown": true, "delimited": true,
	}

	seenProviders := make(map[string]bool)
	for _, prov := range cm.Providers {
		if !validProviders[prov] {
			return fmt.Errorf("%w: context_module.providers contém provider desconhecido %q", ErrInvalidPack, prov)
		}
		if seenProviders[prov] {
			return fmt.Errorf("%w: context_module.providers duplicado %q", ErrInvalidPack, prov)
		}
		seenProviders[prov] = true
	}

	for _, fk := range cm.ExposedFields.Facts {
		if !facts[fk] {
			return fmt.Errorf("%w: context_module.exposed_fields.facts referencia fato desconhecido %q", ErrInvalidPack, fk)
		}
	}
	for _, sk := range cm.ExposedFields.Signals {
		if !signals[sk] {
			return fmt.Errorf("%w: context_module.exposed_fields.signals referencia sinal desconhecido %q", ErrInvalidPack, sk)
		}
	}
	for _, ck := range cm.ExposedFields.ContextSections {
		if !sections[ck] {
			return fmt.Errorf("%w: context_module.exposed_fields.context_sections referencia seção desconhecida %q", ErrInvalidPack, ck)
		}
	}

	for viewName, view := range cm.Views {
		if !validViews[viewName] {
			return fmt.Errorf("%w: context_module.views contém view inválida %q", ErrInvalidPack, viewName)
		}
		if view.MaxTokens < 0 {
			return fmt.Errorf("%w: context_module.views.%s.max_tokens não pode ser negativo", ErrInvalidPack, viewName)
		}
		if !validFormats[view.Format] {
			return fmt.Errorf("%w: context_module.views.%s.format inválido %q", ErrInvalidPack, viewName, view.Format)
		}
		for _, blk := range view.Blocks {
			if !validBlocks[blk] {
				return fmt.Errorf("%w: context_module.views.%s.blocks contém bloco desconhecido %q", ErrInvalidPack, viewName, blk)
			}
		}
	}
	return nil
}

func validateFSM(fsm *FSMDef, seenFacts, seenSignals, seenIntents map[string]bool) error {
	if fsm == nil {
		return nil
	}
	if strings.TrimSpace(fsm.InitialState) == "" {
		return fmt.Errorf("%w: fsm.initial_state é obrigatório", ErrInvalidPack)
	}
	if len(fsm.States) == 0 {
		return fmt.Errorf("%w: fsm.states deve conter ao menos um estado", ErrInvalidPack)
	}
	seenStates := make(map[string]bool, len(fsm.States))
	for _, st := range fsm.States {
		trimmed := strings.TrimSpace(st)
		if trimmed == "" {
			return fmt.Errorf("%w: fsm.states contém estado vazio", ErrInvalidPack)
		}
		if seenStates[trimmed] {
			return fmt.Errorf("%w: fsm.states duplicado %q", ErrInvalidPack, trimmed)
		}
		seenStates[trimmed] = true
	}
	if !seenStates[fsm.InitialState] {
		return fmt.Errorf("%w: fsm.initial_state %q não está na lista de estados", ErrInvalidPack, fsm.InitialState)
	}
	if len(fsm.Actions) == 0 {
		return fmt.Errorf("%w: fsm.actions deve conter ao menos uma ação", ErrInvalidPack)
	}
	for actionName, actionDef := range fsm.Actions {
		if strings.TrimSpace(actionName) == "" {
			return fmt.Errorf("%w: fsm.actions contém ação com nome vazio", ErrInvalidPack)
		}
		if !actionDef.IsValid() {
			return fmt.Errorf("%w: fsm.actions[%s] possui tipo inválido %q", ErrInvalidPack, actionName, actionDef.Type)
		}
		if actionDef.Type == "message_template" && strings.TrimSpace(actionDef.Text) == "" {
			return fmt.Errorf("%w: fsm.actions[%s] do tipo message_template exige text não vazio", ErrInvalidPack, actionName)
		}
	}
	for i, tr := range fsm.Transitions {
		if strings.TrimSpace(tr.From) == "" || strings.TrimSpace(tr.To) == "" {
			return fmt.Errorf("%w: fsm.transitions[%d] exige 'from' e 'to'", ErrInvalidPack, i)
		}
		if tr.From != "*" && !seenStates[tr.From] {
			return fmt.Errorf("%w: fsm.transitions[%d] possui 'from' desconhecido %q", ErrInvalidPack, i, tr.From)
		}
		if !seenStates[tr.To] {
			return fmt.Errorf("%w: fsm.transitions[%d] possui 'to' desconhecido %q", ErrInvalidPack, i, tr.To)
		}
		if strings.TrimSpace(tr.Action) == "" {
			return fmt.Errorf("%w: fsm.transitions[%d] exige 'action'", ErrInvalidPack, i)
		}
		if _, ok := fsm.Actions[tr.Action]; !ok {
			return fmt.Errorf("%w: fsm.transitions[%d] referencia ação desconhecida %q", ErrInvalidPack, i, tr.Action)
		}
		if tr.When.Intent != "" && len(seenIntents) > 0 && !seenIntents[tr.When.Intent] {
			return fmt.Errorf("%w: fsm.transitions[%d] referencia intenção desconhecida %q", ErrInvalidPack, i, tr.When.Intent)
		}
		for _, reqFact := range tr.When.RequiredFacts {
			if !seenFacts[reqFact] {
				return fmt.Errorf("%w: fsm.transitions[%d] referencia fato desconhecido %q", ErrInvalidPack, i, reqFact)
			}
		}
		for _, reqSig := range tr.When.RequiredSignals {
			if !seenSignals[reqSig] {
				return fmt.Errorf("%w: fsm.transitions[%d] referencia sinal desconhecido %q", ErrInvalidPack, i, reqSig)
			}
		}
	}
	return nil
}

func (p *Pack) buildIndexes() {
	p.factByKey = make(map[string]FactDef, len(p.Facts))
	for _, f := range p.Facts {
		p.factByKey[f.Key] = f
	}
	p.signalByKey = make(map[string]SignalDef, len(p.Signals))
	for _, s := range p.Signals {
		p.signalByKey[s.Key] = s
	}
	p.intentByName = make(map[string]IntentDef, len(p.Intents))
	for _, in := range p.Intents {
		p.intentByName[in.Name] = in
	}
	p.contextByKey = make(map[string]ContextSectionDef, len(p.Context))
	for _, c := range p.Context {
		p.contextByKey[c.Key] = c
	}
}

// IntentByName consulta uma intenção definida no pack.
func (p *Pack) IntentByName(name string) (IntentDef, bool) {
	if p == nil {
		return IntentDef{}, false
	}
	p.ensureIndexes()
	def, ok := p.intentByName[name]
	return def, ok
}

func (p *Pack) ensureIndexes() {
	if p.factByKey == nil {
		p.buildIndexes()
	}
}

// EffectiveTiming retorna os tempos efetivos de debounce e espera máxima por digitação.
func (p *Pack) EffectiveTiming() (debounce, typingMaxWait time.Duration) {
	debounceMs := DefaultDebounceMs
	typingWaitMs := DefaultTypingMaxWaitMs
	if p != nil && p.Timing != nil {
		if p.Timing.DebounceMs != nil {
			debounceMs = *p.Timing.DebounceMs
		}
		if p.Timing.TypingMaxWaitMs != nil {
			typingWaitMs = *p.Timing.TypingMaxWaitMs
		}
	}
	return time.Duration(debounceMs) * time.Millisecond, time.Duration(typingWaitMs) * time.Millisecond
}
