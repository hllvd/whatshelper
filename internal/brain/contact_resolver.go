package brain

import (
	"context"
	"regexp"
	"strings"

	"whatshelper/internal/core"
)

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)

// resolveMessageContact garante a identificação, criação e vinculação do contato permanente.
func (p *Pipeline) resolveMessageContact(ctx context.Context, msg core.NormalizedMessage) (*core.Contact, error) {
	if p.cfg.Store == nil {
		return nil, nil
	}

	tenantID := msg.TenantID
	if tenantID == "" {
		tenantID = "demo"
	}
	channelID := msg.Channel
	sourceKey := msg.SourceKey

	now := p.now()

	// 1. Modo Teste / Simulador: não precisa de dados complexos de sessão, somente o ID mesmo.
	if channelID == "simulator" {
		contactID := strings.TrimSpace(sourceKey)
		if contactID == "" {
			contactID = "sim-user-1"
		}
		c, err := p.cfg.Store.GetContact(ctx, tenantID, contactID)
		if err != nil {
			displayName := strings.TrimSpace(msg.DisplayName)
			if displayName == "" {
				displayName = "Simulador " + contactID
			}
			newContact := core.Contact{
				ID:        contactID,
				TenantID:  tenantID,
				Name:      displayName,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if saveErr := p.cfg.Store.SaveContact(ctx, newContact); saveErr != nil {
				return nil, saveErr
			}
			c = &newContact
		}
		_ = p.cfg.Store.LinkSourceToContact(ctx, tenantID, channelID, sourceKey, contactID)
		return c, nil
	}

	// 2. Extrai telefone e/ou e-mail
	rawContact := strings.TrimSpace(msg.RawContact)
	if rawContact == "" {
		rawContact = sourceKey
	}

	var email, phone string
	if channelID == "email" || strings.Contains(rawContact, "@") {
		if match := emailPattern.FindString(rawContact); match != "" {
			email = core.NormalizeEmail(match)
		} else {
			email = core.NormalizeEmail(rawContact)
		}
	} else if channelID == "whatsapp" {
		phone = core.NormalizePhone(rawContact)
	} else {
		if strings.Contains(rawContact, "@") {
			if match := emailPattern.FindString(rawContact); match != "" {
				email = core.NormalizeEmail(match)
			} else {
				email = core.NormalizeEmail(rawContact)
			}
		} else {
			phone = core.NormalizePhone(rawContact)
		}
	}

	// Checa se o corpo da mensagem contém um e-mail informado
	if email == "" && (msg.Direction == core.DirectionInbound || msg.Direction == "") && msg.Text != "" {
		if match := emailPattern.FindString(msg.Text); match != "" {
			email = core.NormalizeEmail(match)
		}
	}

	// 3. Procura contato existente por e-mail ou telefone
	existing, err := p.cfg.Store.FindContactByEmailOrPhone(ctx, tenantID, email, phone)
	if err == nil && existing != nil {
		changed := false
		if existing.Email == "" && email != "" {
			existing.Email = email
			changed = true
		}
		if existing.Phone == "" && phone != "" {
			existing.Phone = phone
			changed = true
		}
		if existing.Name == "" && strings.TrimSpace(msg.DisplayName) != "" {
			existing.Name = strings.TrimSpace(msg.DisplayName)
			changed = true
		}
		if changed {
			existing.UpdatedAt = now
			_ = p.cfg.Store.SaveContact(ctx, *existing)
		}
		_ = p.cfg.Store.LinkSourceToContact(ctx, tenantID, channelID, sourceKey, existing.ID)
		return existing, nil
	}

	// 4. Cria novo contato permanente
	var contactID string
	if email != "" {
		sanitized := strings.ReplaceAll(email, "@", "_at_")
		sanitized = strings.ReplaceAll(sanitized, ".", "_")
		contactID = "c-" + sanitized
	} else if phone != "" {
		contactID = "c-" + phone
	} else {
		contactID = "c-" + newID("usr")
	}

	name := strings.TrimSpace(msg.DisplayName)
	if name == "" {
		if email != "" {
			name = email
		} else if phone != "" {
			name = phone
		} else {
			name = "Cliente " + contactID
		}
	}

	newContact := core.Contact{
		ID:        contactID,
		TenantID:  tenantID,
		Name:      name,
		Email:     email,
		Phone:     phone,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := p.cfg.Store.SaveContact(ctx, newContact); err != nil {
		return nil, err
	}
	_ = p.cfg.Store.LinkSourceToContact(ctx, tenantID, channelID, sourceKey, contactID)
	return &newContact, nil
}
