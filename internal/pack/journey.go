package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"gopkg.in/yaml.v3"
)

// ResponseDirectiveDef define a diretiva estruturada enviada ao redator de resposta.
type ResponseDirectiveDef struct {
	Goal        string   `yaml:"goal" json:"goal"`
	MustAsk     []string `yaml:"must_ask,omitempty" json:"must_ask,omitempty"`
	Constraints []string `yaml:"constraints,omitempty" json:"constraints,omitempty"`
}

// JourneyActionDef declara uma ação emitida por uma regra ou estado da jornada.
type JourneyActionDef struct {
	Type   string         `yaml:"type" json:"type"`
	Params map[string]any `yaml:"params,omitempty" json:"params,omitempty"`
}

// IsValidJourneyActionType valida o tipo da ação contra o registro oficial do core.
func IsValidJourneyActionType(t string) bool {
	switch t {
	case "reply_directive", "create_task", "notify_admin", "schedule_timeout", "handoff", "set_status":
		return true
	default:
		return false
	}
}

// JourneyRuleDef define uma regra declarativa de transição na jornada.
type JourneyRuleDef struct {
	ID            string                `yaml:"id" json:"id"`
	Priority      int                   `yaml:"priority" json:"priority"`
	OnEvent       string                `yaml:"on_event,omitempty" json:"on_event,omitempty"`
	Intent        string                `yaml:"intent,omitempty" json:"intent,omitempty"`
	Guard         string                `yaml:"guard,omitempty" json:"guard,omitempty"`
	To            string                `yaml:"to" json:"to"`
	Actions       []JourneyActionDef    `yaml:"actions,omitempty" json:"actions,omitempty"`
	Directive     *ResponseDirectiveDef `yaml:"directive,omitempty" json:"directive,omitempty"`
	Reason        string                `yaml:"reason,omitempty" json:"reason,omitempty"`
	CompiledGuard *vm.Program           `yaml:"-" json:"-"`
}

// JourneyStateDef define um estado do funil/jornada no journey.yaml.
type JourneyStateDef struct {
	Description         string                `yaml:"description,omitempty" json:"description,omitempty"`
	Terminal            bool                  `yaml:"terminal,omitempty" json:"terminal,omitempty"`
	TimeoutMinutes      int                   `yaml:"timeout_minutes,omitempty" json:"timeout_minutes,omitempty"`
	StagnationThreshold int                   `yaml:"stagnation_threshold,omitempty" json:"stagnation_threshold,omitempty"`
	StagnationAction    string                `yaml:"stagnation_action,omitempty" json:"stagnation_action,omitempty"`
	StagnationTo        string                `yaml:"stagnation_to,omitempty" json:"stagnation_to,omitempty"`
	Directive           *ResponseDirectiveDef `yaml:"directive,omitempty" json:"directive,omitempty"`
	Rules               []JourneyRuleDef      `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// JourneyDefaultsDef define parâmetros padrão para estagnação e diretiva de fallback.
type JourneyDefaultsDef struct {
	StagnationThreshold int                   `yaml:"stagnation_threshold,omitempty" json:"stagnation_threshold,omitempty"`
	StagnationTo        string                `yaml:"stagnation_to,omitempty" json:"stagnation_to,omitempty"`
	FallbackDirective   *ResponseDirectiveDef `yaml:"fallback_directive,omitempty" json:"fallback_directive,omitempty"`
}

// JourneyDef representa o arquivo journey.yaml compilado e validado de um pack.
type JourneyDef struct {
	Version      string                     `yaml:"version" json:"version"`
	InitialState string                     `yaml:"initial_state" json:"initial_state"`
	States       map[string]JourneyStateDef `yaml:"states" json:"states"`
	GlobalRules  []JourneyRuleDef           `yaml:"global_rules,omitempty" json:"global_rules,omitempty"`
	Defaults     JourneyDefaultsDef         `yaml:"defaults,omitempty" json:"defaults,omitempty"`
}

// JourneyGuardEnv define o ambiente seguro e imutável exposto às expressões de guard (expr).
type JourneyGuardEnv struct {
	Facts              map[string]string `expr:"facts"`
	Signals            map[string]bool   `expr:"signals"`
	Dimensions         map[string]string `expr:"dimensions"`
	Intent             string            `expr:"intent"`
	EventType          string            `expr:"event_type"`
	State              string            `expr:"state"`
	StagnationCount    int               `expr:"stagnation_count"`
	TimeInStateMinutes int               `expr:"time_in_state_minutes"`
}

// HasFact retorna true se o fato existir com valor não vazio.
func (e JourneyGuardEnv) HasFact(key string) bool {
	if e.Facts == nil {
		return false
	}
	return strings.TrimSpace(e.Facts[key]) != ""
}

// FactNum converte um fato numérico para float64 (0 se ausente ou inválido).
func (e JourneyGuardEnv) FactNum(key string) float64 {
	if e.Facts == nil {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(e.Facts[key]), 64)
	if err != nil {
		return 0
	}
	return v
}

// CompileJourneyGuard compila e valida uma expressão booleana de guard usando expr.
func CompileJourneyGuard(source string) (*vm.Program, error) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return nil, nil
	}
	prog, err := expr.Compile(trimmed, expr.Env(JourneyGuardEnv{}), expr.AsBool())
	if err != nil {
		return nil, fmt.Errorf("guard inválido %q: %w", trimmed, err)
	}
	return prog, nil
}

// ParseJourneyYAML decodifica, compila guards e valida um journey.yaml.
func ParseJourneyYAML(data []byte, p *Pack) (*JourneyDef, error) {
	var j JourneyDef
	if err := yaml.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("%w: falha ao parsear journey.yaml: %v", ErrInvalidPack, err)
	}
	if err := ValidateJourney(&j, p); err != nil {
		return nil, err
	}
	return &j, nil
}

// LoadJourneyFromFile carrega journey.yaml do diretório do pack se existir.
func LoadJourneyFromFile(dir string, p *Pack) (*JourneyDef, error) {
	path := filepath.Join(dir, "journey.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: erro ao ler journey.yaml: %v", ErrInvalidPack, err)
	}
	return ParseJourneyYAML(data, p)
}

// LoadJourneysFromDir carrega todas as jornadas em <dir>/journeys/*.yaml (opcional).
func LoadJourneysFromDir(dir string, p *Pack) (map[string]*JourneyDef, error) {
	journeysDir := filepath.Join(dir, "journeys")
	entries, err := os.ReadDir(journeysDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: erro ao listar %s: %v", ErrInvalidPack, journeysDir, err)
	}
	out := make(map[string]*JourneyDef)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		data, readErr := os.ReadFile(filepath.Join(journeysDir, entry.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("%w: erro ao ler journeys/%s: %v", ErrInvalidPack, entry.Name(), readErr)
		}
		j, parseErr := ParseJourneyYAML(data, p)
		if parseErr != nil {
			return nil, fmt.Errorf("journeys/%s: %w", entry.Name(), parseErr)
		}
		out[name] = j
	}
	return out, nil
}

// ValidateJourney valida estados, transições, prioridades únicas, ações e compila todos os guards.
func ValidateJourney(j *JourneyDef, p *Pack) error {
	if j == nil {
		return fmt.Errorf("%w: journey nulo", ErrInvalidPack)
	}
	if strings.TrimSpace(j.InitialState) == "" {
		return fmt.Errorf("%w: journey.initial_state é obrigatório", ErrInvalidPack)
	}
	if len(j.States) == 0 {
		return fmt.Errorf("%w: journey.states não pode ser vazio", ErrInvalidPack)
	}
	if _, ok := j.States[j.InitialState]; !ok {
		return fmt.Errorf("%w: journey.initial_state %q não existe em states", ErrInvalidPack, j.InitialState)
	}
	if j.Defaults.StagnationTo != "" {
		if _, ok := j.States[j.Defaults.StagnationTo]; !ok {
			return fmt.Errorf("%w: journey.defaults.stagnation_to %q não existe em states", ErrInvalidPack, j.Defaults.StagnationTo)
		}
	}

	seenRuleIDs := make(map[string]bool)
	if err := validateRuleSlice("global_rules", j.GlobalRules, j, seenRuleIDs); err != nil {
		return err
	}

	for stateName, st := range j.States {
		if strings.TrimSpace(stateName) == "" {
			return fmt.Errorf("%w: nome de estado vazio em journey.states", ErrInvalidPack)
		}
		if st.StagnationTo != "" {
			if _, ok := j.States[st.StagnationTo]; !ok {
				return fmt.Errorf("%w: journey.states.%s.stagnation_to %q não existe em states", ErrInvalidPack, stateName, st.StagnationTo)
			}
		}
		if err := validateRuleSlice("states."+stateName+".rules", st.Rules, j, seenRuleIDs); err != nil {
			return err
		}
	}
	return nil
}

func validateRuleSlice(scope string, rules []JourneyRuleDef, j *JourneyDef, seenRuleIDs map[string]bool) error {
	seenPriority := make(map[string]string) // key: onEvent+"|"+intent+"|"+priority -> ruleID
	for i := range rules {
		r := &rules[i]
		if strings.TrimSpace(r.ID) == "" {
			return fmt.Errorf("%w: regra sem id em %s[%d]", ErrInvalidPack, scope, i)
		}
		if seenRuleIDs[r.ID] {
			return fmt.Errorf("%w: id de regra duplicado %q", ErrInvalidPack, r.ID)
		}
		seenRuleIDs[r.ID] = true

		if r.To != "" {
			if _, ok := j.States[r.To]; !ok {
				return fmt.Errorf("%w: regra %q em %s aponta para estado desconhecido %q", ErrInvalidPack, r.ID, scope, r.To)
			}
		}

		onEv := strings.TrimSpace(r.OnEvent)
		if onEv == "" {
			onEv = "*"
		}
		prioKey := fmt.Sprintf("%s|%s|%d", onEv, strings.TrimSpace(r.Intent), r.Priority)
		if prevID, dup := seenPriority[prioKey]; dup {
			return fmt.Errorf("%w: empate de prioridade (%d) entre regras %q e %q em %s", ErrInvalidPack, r.Priority, prevID, r.ID, scope)
		}
		seenPriority[prioKey] = r.ID

		for _, act := range r.Actions {
			if !IsValidJourneyActionType(act.Type) {
				return fmt.Errorf("%w: ação desconhecida %q na regra %q", ErrInvalidPack, act.Type, r.ID)
			}
		}

		if strings.TrimSpace(r.Guard) != "" {
			prog, err := CompileJourneyGuard(r.Guard)
			if err != nil {
				return fmt.Errorf("%w: regra %q: %v", ErrInvalidPack, r.ID, err)
			}
			r.CompiledGuard = prog
		}
	}
	return nil
}
