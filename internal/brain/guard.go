package brain

import (
	"fmt"

	"github.com/expr-lang/expr"

	"whatshelper/internal/pack"
)

// GuardEvalError registra uma falha segura ao avaliar um guard em runtime (nunca causa panic/crash).
type GuardEvalError struct {
	RuleID string `json:"rule_id"`
	Guard  string `json:"guard"`
	Error  string `json:"error"`
}

// EvalJourneyGuard avalia um guard pré-compilado de forma segura sobre o ambiente imutável da jornada.
// Erro de avaliação retorna (false, err) para que a regra seja ignorada e auditada via evento.
func EvalJourneyGuard(rule pack.JourneyRuleDef, env pack.JourneyGuardEnv) (bool, error) {
	if rule.CompiledGuard == nil {
		return true, nil
	}
	res, err := expr.Run(rule.CompiledGuard, env)
	if err != nil {
		return false, err
	}
	b, ok := res.(bool)
	if !ok {
		return false, fmt.Errorf("guard %q retornou %T em vez de bool", rule.ID, res)
	}
	return b, nil
}
