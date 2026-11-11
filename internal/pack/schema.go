package pack

import (
	"encoding/json"
	"fmt"
)

// BuildAnalysisSchema gera deterministicamente um JSON Schema a partir da definição do pack.
func BuildAnalysisSchema(p *Pack) ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: pack nulo", ErrInvalidPack)
	}

	factProps := make(map[string]any, len(p.Facts))
	for _, f := range p.Facts {
		factProps[f.Key] = schemaForType(f.Type, f.Description, f.Values, true)
	}

	signalProps := make(map[string]any, len(p.Signals))
	signalRequired := make([]string, 0, len(p.Signals))
	for _, s := range p.Signals {
		prop := map[string]any{
			"type": "boolean",
		}
		if s.Description != "" {
			prop["description"] = s.Description
		}
		signalProps[s.Key] = prop
		signalRequired = append(signalRequired, s.Key)
	}

	contextProps := make(map[string]any, len(p.Context))
	for _, sec := range p.Context {
		fieldProps := make(map[string]any, len(sec.Fields))
		for _, field := range sec.Fields {
			fieldProps[field.Key] = schemaForType(field.Type, field.Description, field.Values, true)
		}
		secObj := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           fieldProps,
		}
		if sec.Description != "" {
			secObj["description"] = sec.Description
		}
		contextProps[sec.Key] = secObj
	}

	required := []string{"summary", "last_sender", "facts", "signals", "context"}
	properties := map[string]any{
		"summary": map[string]any{
			"type":        "string",
			"description": "Resumo conciso e factual (1 a 3 frases) no idioma do pack",
		},
		"last_sender": map[string]any{
			"type": "string",
			"enum": []string{p.Actors.Business, p.Actors.Customer},
		},
		"facts": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           factProps,
		},
		"signals": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             signalRequired,
			"properties":           signalProps,
		},
		"context": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           contextProps,
		},
	}

	if len(p.Intents) > 0 {
		intentNames := make([]string, 0, len(p.Intents))
		for _, in := range p.Intents {
			intentNames = append(intentNames, in.Name)
		}
		required = append(required, "intent")
		properties["intent"] = map[string]any{
			"type":        "string",
			"enum":        intentNames,
			"description": "Intenção principal identificada na última mensagem",
		}
	}

	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             required,
		"properties":           properties,
	}

	return json.MarshalIndent(schema, "", "  ")
}

func schemaForType(t FactType, desc string, values []string, nullable bool) map[string]any {
	prop := make(map[string]any)
	if desc != "" {
		prop["description"] = desc
	}

	var baseType string
	switch t {
	case FactTypeNumber:
		baseType = "number"
	case FactTypeInteger:
		baseType = "integer"
	case FactTypeBoolean:
		baseType = "boolean"
	case FactTypeEnum, FactTypeString:
		baseType = "string"
	default:
		baseType = "string"
	}

	if nullable {
		prop["type"] = []string{baseType, "null"}
	} else {
		prop["type"] = baseType
	}

	if t == FactTypeEnum && len(values) > 0 {
		if nullable {
			enumVals := make([]any, 0, len(values)+1)
			for _, v := range values {
				enumVals = append(enumVals, v)
			}
			enumVals = append(enumVals, nil)
			prop["enum"] = enumVals
		} else {
			prop["enum"] = values
		}
	}

	return prop
}
