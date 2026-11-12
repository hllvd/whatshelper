package contextmod

import (
	"fmt"
	"strings"

	"whatshelper/internal/pack"
)

// ViewSpec define os blocos, ordem, formato, orçamento e restrições de uma view.
type ViewSpec struct {
	Name                ViewName
	Blocks              []string
	Format              string
	MaxTokens           int
	IncludeMessages     bool
	ApplyAllowlist      bool
	DelimitUntrustedMsg bool
	AllowedFacts        map[string]bool
	AllowedSignals      map[string]bool
	AllowedSections     map[string]bool
}

var defaultViewSpecs = map[ViewName]ViewSpec{
	ViewAnalysis: {
		Name:            ViewAnalysis,
		Blocks:          []string{"summary", "facts", "signals", "structured_context", "recent_messages"},
		Format:          "prompt",
		MaxTokens:       6000,
		IncludeMessages: true,
	},
	ViewResponse: {
		Name:            ViewResponse,
		Blocks:          []string{"summary", "facts", "signals", "structured_context", "recent_messages", "knowledge", "qa"},
		Format:          "prompt",
		MaxTokens:       12000,
		IncludeMessages: true,
	},
	ViewAdmin: {
		Name:            ViewAdmin,
		Blocks:          []string{"summary", "facts", "signals", "structured_context", "recent_messages", "knowledge", "qa"},
		Format:          "markdown",
		MaxTokens:       8000,
		IncludeMessages: true,
	},
	ViewExternalAgent: {
		Name:                ViewExternalAgent,
		Blocks:              []string{"summary", "facts", "signals", "structured_context", "recent_messages"},
		Format:              "delimited",
		MaxTokens:           4000,
		IncludeMessages:     true,
		ApplyAllowlist:      true,
		DelimitUntrustedMsg: true,
	},
	ViewSummaryOnly: {
		Name:            ViewSummaryOnly,
		Blocks:          []string{"summary"},
		Format:          "json",
		MaxTokens:       600,
		IncludeMessages: false,
	},
}

// ResolveViewSpec combina as regras padrão da view com as declarações do pack.yaml e orçamento opcional.
func ResolveViewSpec(view ViewName, pk *pack.Pack, budgetOverride int) (ViewSpec, error) {
	base, ok := defaultViewSpecs[view]
	if !ok {
		return ViewSpec{}, fmt.Errorf("%w: %q", ErrInvalidView, view)
	}

	spec := ViewSpec{
		Name:                base.Name,
		Blocks:              append([]string(nil), base.Blocks...),
		Format:              base.Format,
		MaxTokens:           base.MaxTokens,
		IncludeMessages:     base.IncludeMessages,
		ApplyAllowlist:      base.ApplyAllowlist,
		DelimitUntrustedMsg: base.DelimitUntrustedMsg,
	}

	if pk != nil && pk.ContextModule != nil {
		if override, has := pk.ContextModule.Views[string(view)]; has {
			if len(override.Blocks) > 0 {
				spec.Blocks = append([]string(nil), override.Blocks...)
			}
			if strings.TrimSpace(override.Format) != "" {
				spec.Format = strings.TrimSpace(override.Format)
			}
			if override.MaxTokens > 0 {
				spec.MaxTokens = override.MaxTokens
			}
			if override.IncludeMessages != nil {
				spec.IncludeMessages = *override.IncludeMessages
			}
		}
		if spec.ApplyAllowlist {
			spec.AllowedFacts = toSet(pk.ContextModule.ExposedFields.Facts)
			spec.AllowedSignals = toSet(pk.ContextModule.ExposedFields.Signals)
			spec.AllowedSections = toSet(pk.ContextModule.ExposedFields.ContextSections)
		}
	}

	if budgetOverride > 0 {
		spec.MaxTokens = budgetOverride
	}
	return spec, nil
}

// DelimitUntrustedMessage envolve conteúdo vindo do usuário como dado não confiável (mitiga prompt injection).
func DelimitUntrustedMessage(text string) string {
	cleaned := strings.ReplaceAll(text, "<<<END_UNTRUSTED_MESSAGE>>>", "")
	return "<<<UNTRUSTED_MESSAGE>>>\n" + cleaned + "\n<<<END_UNTRUSTED_MESSAGE>>>"
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
}
