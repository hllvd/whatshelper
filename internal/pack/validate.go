package pack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ValidationError descreve uma inconsistência encontrada ao validar o JSON de análise contra o pack.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// AnalysisResult representa a saída estruturada já validada e normalizada pelo schema do pack.
type AnalysisResult struct {
	Summary      string                       `json:"summary"`
	LastSender   string                       `json:"last_sender"`
	Intent       string                       `json:"intent,omitempty"`
	Facts        map[string]string            `json:"facts"`
	ClearedFacts []string                     `json:"cleared_facts,omitempty"`
	Signals      map[string]bool              `json:"signals"`
	Context      map[string]map[string]string `json:"context"`
}

// ValidateAnalysis valida o JSON bruto contra as definições do pack e normaliza os valores.
func ValidateAnalysis(p *Pack, raw []byte) (*AnalysisResult, []ValidationError) {
	if p == nil {
		return nil, []ValidationError{{Field: "pack", Message: "pack nulo"}}
	}
	p.ensureIndexes()

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, []ValidationError{{Field: "$", Message: fmt.Sprintf("JSON inválido: %v", err)}}
	}
	if doc == nil {
		return nil, []ValidationError{{Field: "$", Message: "JSON deve ser um objeto"}}
	}

	var errs []ValidationError

	// Rejeita chaves de topo desconhecidas
	allowedTop := map[string]bool{
		"summary":     true,
		"last_sender": true,
		"intent":      true,
		"facts":       true,
		"signals":     true,
		"context":     true,
	}
	for k := range doc {
		if !allowedTop[k] {
			errs = append(errs, ValidationError{Field: k, Message: "campo desconhecido na raiz"})
		}
	}

	res := &AnalysisResult{
		Facts:   make(map[string]string),
		Signals: make(map[string]bool, len(p.Signals)),
		Context: make(map[string]map[string]string),
	}

	// Inicializa sinais ausentes com false
	for _, s := range p.Signals {
		res.Signals[s.Key] = false
	}

	// 1. summary
	if rawSummary, ok := doc["summary"]; !ok {
		errs = append(errs, ValidationError{Field: "summary", Message: "campo obrigatório ausente"})
	} else if s, ok := rawSummary.(string); !ok || strings.TrimSpace(s) == "" {
		errs = append(errs, ValidationError{Field: "summary", Message: "deve ser uma string não vazia"})
	} else {
		res.Summary = strings.TrimSpace(s)
	}

	// 2. last_sender
	if rawSender, ok := doc["last_sender"]; !ok {
		errs = append(errs, ValidationError{Field: "last_sender", Message: "campo obrigatório ausente"})
	} else if s, ok := rawSender.(string); !ok {
		errs = append(errs, ValidationError{Field: "last_sender", Message: "deve ser string"})
	} else if s != p.Actors.Business && s != p.Actors.Customer {
		errs = append(errs, ValidationError{
			Field:   "last_sender",
			Message: fmt.Sprintf("valor %q inválido; esperado %q ou %q", s, p.Actors.Business, p.Actors.Customer),
		})
	} else {
		res.LastSender = s
	}

	// 2b. intent (obrigatório quando o pack declara intents)
	rawIntent, hasIntent := doc["intent"]
	if len(p.Intents) > 0 {
		if !hasIntent {
			errs = append(errs, ValidationError{Field: "intent", Message: "campo obrigatório ausente"})
		} else if s, ok := rawIntent.(string); !ok {
			errs = append(errs, ValidationError{Field: "intent", Message: "deve ser string"})
		} else if _, exists := p.intentByName[s]; !exists {
			errs = append(errs, ValidationError{Field: "intent", Message: fmt.Sprintf("intenção %q não declarada no pack", s)})
		} else {
			res.Intent = s
		}
	} else if hasIntent && rawIntent != nil {
		if s, ok := rawIntent.(string); ok {
			res.Intent = strings.TrimSpace(s)
		} else {
			errs = append(errs, ValidationError{Field: "intent", Message: "deve ser string"})
		}
	}

	// 3. facts
	if rawFacts, ok := doc["facts"]; ok && rawFacts != nil {
		factsObj, ok := rawFacts.(map[string]any)
		if !ok {
			errs = append(errs, ValidationError{Field: "facts", Message: "deve ser um objeto JSON"})
		} else {
			for k, v := range factsObj {
				def, exists := p.factByKey[k]
				if !exists {
					errs = append(errs, ValidationError{Field: "facts." + k, Message: "chave de fato desconhecida para este pack"})
					continue
				}
				if v == nil {
					res.ClearedFacts = append(res.ClearedFacts, k)
					continue
				}
				norm, errMsg := normalizeTypedValue(def.Type, def.Values, v)
				if errMsg != "" {
					errs = append(errs, ValidationError{Field: "facts." + k, Message: errMsg})
					continue
				}
				res.Facts[k] = norm
			}
			sort.Strings(res.ClearedFacts)
		}
	}

	// 4. signals
	if rawSignals, ok := doc["signals"]; ok && rawSignals != nil {
		sigObj, ok := rawSignals.(map[string]any)
		if !ok {
			errs = append(errs, ValidationError{Field: "signals", Message: "deve ser um objeto JSON"})
		} else {
			for k, v := range sigObj {
				if _, exists := p.signalByKey[k]; !exists {
					errs = append(errs, ValidationError{Field: "signals." + k, Message: "sinal desconhecido para este pack"})
					continue
				}
				b, ok := v.(bool)
				if !ok {
					errs = append(errs, ValidationError{Field: "signals." + k, Message: "sinal deve ser boolean (true ou false)"})
					continue
				}
				res.Signals[k] = b
			}
		}
	}

	// 5. context
	if rawCtx, ok := doc["context"]; ok && rawCtx != nil {
		ctxObj, ok := rawCtx.(map[string]any)
		if !ok {
			errs = append(errs, ValidationError{Field: "context", Message: "deve ser um objeto JSON"})
		} else {
			for secKey, secRaw := range ctxObj {
				secDef, exists := p.contextByKey[secKey]
				if !exists {
					errs = append(errs, ValidationError{Field: "context." + secKey, Message: "seção de contexto desconhecida para este pack"})
					continue
				}
				if secRaw == nil {
					continue
				}
				secMap, ok := secRaw.(map[string]any)
				if !ok {
					errs = append(errs, ValidationError{Field: "context." + secKey, Message: "seção de contexto deve ser objeto"})
					continue
				}
				fieldIndex := make(map[string]ContextFieldDef, len(secDef.Fields))
				for _, fd := range secDef.Fields {
					fieldIndex[fd.Key] = fd
				}
				parsedSec := make(map[string]string)
				for fk, fv := range secMap {
					fDef, fExists := fieldIndex[fk]
					if !fExists {
						errs = append(errs, ValidationError{
							Field:   fmt.Sprintf("context.%s.%s", secKey, fk),
							Message: "campo desconhecido na seção de contexto",
						})
						continue
					}
					if fv == nil {
						continue
					}
					norm, errMsg := normalizeTypedValue(fDef.Type, fDef.Values, fv)
					if errMsg != "" {
						errs = append(errs, ValidationError{
							Field:   fmt.Sprintf("context.%s.%s", secKey, fk),
							Message: errMsg,
						})
						continue
					}
					parsedSec[fk] = norm
				}
				if len(parsedSec) > 0 {
					res.Context[secKey] = parsedSec
				}
			}
		}
	}

	if len(errs) > 0 {
		sort.Slice(errs, func(i, j int) bool {
			return errs[i].Field < errs[j].Field
		})
		return nil, errs
	}
	return res, nil
}

func normalizeTypedValue(t FactType, enumValues []string, v any) (string, string) {
	switch t {
	case FactTypeString:
		s, ok := v.(string)
		if !ok {
			return "", "deve ser string"
		}
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return "", "string não pode ser vazia (omita a chave ou use null)"
		}
		return trimmed, ""

	case FactTypeNumber:
		switch val := v.(type) {
		case json.Number:
			f, err := strconv.ParseFloat(val.String(), 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return "", fmt.Sprintf("número inválido: %q", val.String())
			}
			return strconv.FormatFloat(f, 'f', -1, 64), ""
		case string:
			trimmed := strings.TrimSpace(val)
			f, err := strconv.ParseFloat(trimmed, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return "", fmt.Sprintf("deve ser número puro sem texto extra (recebido %q)", val)
			}
			return strconv.FormatFloat(f, 'f', -1, 64), ""
		default:
			return "", "deve ser número"
		}

	case FactTypeInteger:
		switch val := v.(type) {
		case json.Number:
			if i, err := strconv.ParseInt(val.String(), 10, 64); err == nil {
				return strconv.FormatInt(i, 10), ""
			}
			return "", fmt.Sprintf("deve ser inteiro (recebido %q)", val.String())
		case string:
			trimmed := strings.TrimSpace(val)
			i, err := strconv.ParseInt(trimmed, 10, 64)
			if err != nil {
				return "", fmt.Sprintf("deve ser inteiro puro (recebido %q)", val)
			}
			return strconv.FormatInt(i, 10), ""
		default:
			return "", "deve ser inteiro"
		}

	case FactTypeBoolean:
		switch val := v.(type) {
		case bool:
			return strconv.FormatBool(val), ""
		case string:
			lower := strings.ToLower(strings.TrimSpace(val))
			if lower == "true" || lower == "false" {
				return lower, ""
			}
			return "", fmt.Sprintf("deve ser boolean (recebido %q)", val)
		default:
			return "", "deve ser boolean"
		}

	case FactTypeEnum:
		s, ok := v.(string)
		if !ok {
			return "", "deve ser string com um dos valores permitidos"
		}
		trimmed := strings.TrimSpace(s)
		for _, allowed := range enumValues {
			if strings.EqualFold(trimmed, allowed) {
				return allowed, ""
			}
		}
		return "", fmt.Sprintf("valor %q inválido; permitidos: %s", trimmed, strings.Join(enumValues, ", "))

	default:
		return "", fmt.Sprintf("tipo não suportado: %s", t)
	}
}
