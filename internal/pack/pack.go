package pack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

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

// SignalDef define um sinal booleano configurável no pack.yaml.
type SignalDef struct {
	Key         string `yaml:"key" json:"key"`
	Description string `yaml:"description" json:"description"`
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

// Pack representa um vertical pack carregado e validado.
type Pack struct {
	Name     string              `yaml:"name" json:"name"`
	Language string              `yaml:"language" json:"language"`
	Actors   ActorsDef           `yaml:"actors" json:"actors"`
	Facts    []FactDef           `yaml:"facts" json:"facts"`
	Signals  []SignalDef         `yaml:"signals" json:"signals"`
	Context  []ContextSectionDef `yaml:"context" json:"context"`
	Prompts  PromptsDef          `yaml:"prompts" json:"prompts"`
	Rules    []string            `yaml:"rules,omitempty" json:"rules,omitempty"`

	// Templates compilados na carga
	AnalysisTmpl *template.Template `yaml:"-" json:"-"`
	ReplyTmpl    *template.Template `yaml:"-" json:"-"`
	NudgeTmpl    *template.Template `yaml:"-" json:"-"`

	// Índices imutáveis derivados para consulta O(1)
	factByKey    map[string]FactDef
	signalByKey  map[string]SignalDef
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
	return p, nil
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
	p.contextByKey = make(map[string]ContextSectionDef, len(p.Context))
	for _, c := range p.Context {
		p.contextByKey[c.Key] = c
	}
}

func (p *Pack) ensureIndexes() {
	if p.factByKey == nil {
		p.buildIndexes()
	}
}
