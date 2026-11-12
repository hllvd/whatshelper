package whatsapp

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatshelper/internal/channels/bridge"
	"whatshelper/internal/core"
)

var ErrInvalidPhoneNumber = errors.New("whatsapp: número de telefone E.164 inválido")

// NormalizeE164 converte telefone ou JID WhatsApp em dígitos E.164 puros (sem '+', espaços ou sufixo '@').
func NormalizeE164(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrInvalidPhoneNumber
	}
	if at := strings.IndexByte(trimmed, '@'); at >= 0 {
		trimmed = trimmed[:at]
	}
	if colon := strings.IndexByte(trimmed, ':'); colon >= 0 {
		trimmed = trimmed[:colon]
	}
	trimmed = strings.TrimPrefix(trimmed, "+")
	if strings.HasPrefix(trimmed, "00") && len(trimmed) > 10 {
		trimmed = strings.TrimPrefix(trimmed, "00")
	}

	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range trimmed {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if len(digits) < 8 || len(digits) > 15 {
		return "", fmt.Errorf("%w: %q", ErrInvalidPhoneNumber, raw)
	}
	return digits, nil
}

// IsAdminPhone compara source_key normalizado contra ADMIN_PHONE_E164.
func IsAdminPhone(sourceKey, adminPhone string) bool {
	if strings.TrimSpace(adminPhone) == "" {
		return false
	}
	a, errA := NormalizeE164(sourceKey)
	b, errB := NormalizeE164(adminPhone)
	return errA == nil && errB == nil && a == b
}

// ShouldProcessInbound filtra status/broadcast e grupos (desabilitados por padrão).
// Mensagens próprias (isFromMe=true) são encaminhadas ao Brain para resolução determinística de origin.
func ShouldProcessInbound(isFromMe, isGroup, isBroadcast, allowGroups bool) bool {
	if isBroadcast {
		return false
	}
	if isGroup && !allowGroups {
		return false
	}
	return true
}

// ShouldIgnoreHistoryFromMe ignora ecos from_me antigos recebidos durante history sync antes da conexão atual.
func ShouldIgnoreHistoryFromMe(isFromMe bool, msgTS, connectedAt time.Time) bool {
	if !isFromMe || connectedAt.IsZero() || msgTS.IsZero() {
		return false
	}
	return msgTS.Before(connectedAt.Add(-5 * time.Second))
}

// UnwrapMessage extrai a mensagem interna caso venha encapsulada em Ephemeral ou ViewOnce.
func UnwrapMessage(msg *waE2E.Message) *waE2E.Message {
	if msg == nil {
		return nil
	}
	for range 4 {
		switch {
		case msg.GetEphemeralMessage().GetMessage() != nil:
			msg = msg.GetEphemeralMessage().GetMessage()
		case msg.GetViewOnceMessage().GetMessage() != nil:
			msg = msg.GetViewOnceMessage().GetMessage()
		case msg.GetViewOnceMessageV2().GetMessage() != nil:
			msg = msg.GetViewOnceMessageV2().GetMessage()
		case msg.GetViewOnceMessageV2Extension().GetMessage() != nil:
			msg = msg.GetViewOnceMessageV2Extension().GetMessage()
		case msg.GetDocumentWithCaptionMessage().GetMessage() != nil:
			msg = msg.GetDocumentWithCaptionMessage().GetMessage()
		default:
			return msg
		}
	}
	return msg
}

// ParsedContent representa o conteúdo normalizado extraído de um protobuf waE2E.Message.
type ParsedContent struct {
	Type         core.MessageType
	Text         string
	Downloadable whatsmeow.DownloadableMessage
	Ext          string
	MimeType     string
}

// ExtractContent extrai texto, tipo canônico e referência de download de mídia de uma mensagem WhatsApp.
func ExtractContent(raw *waE2E.Message) (ParsedContent, bool) {
	msg := UnwrapMessage(raw)
	if msg == nil {
		return ParsedContent{}, false
	}
	if conv := strings.TrimSpace(msg.GetConversation()); conv != "" {
		return ParsedContent{Type: core.MessageTypeText, Text: conv}, true
	}
	if extTxt := msg.GetExtendedTextMessage(); extTxt != nil && strings.TrimSpace(extTxt.GetText()) != "" {
		return ParsedContent{Type: core.MessageTypeText, Text: strings.TrimSpace(extTxt.GetText())}, true
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		mime := strings.TrimSpace(aud.GetMimetype())
		if mime == "" {
			mime = "audio/ogg"
		}
		return ParsedContent{
			Type:         core.MessageTypeAudio,
			Downloadable: aud,
			Ext:          extFromMimeOrName(mime, "", "ogg"),
			MimeType:     mime,
		}, true
	}
	if img := msg.GetImageMessage(); img != nil {
		mime := strings.TrimSpace(img.GetMimetype())
		if mime == "" {
			mime = "image/jpeg"
		}
		return ParsedContent{
			Type:         core.MessageTypeImage,
			Text:         strings.TrimSpace(img.GetCaption()),
			Downloadable: img,
			Ext:          extFromMimeOrName(mime, "", "jpg"),
			MimeType:     mime,
		}, true
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		mime := strings.TrimSpace(doc.GetMimetype())
		if mime == "" {
			mime = "application/octet-stream"
		}
		return ParsedContent{
			Type:         core.MessageTypeDoc,
			Text:         strings.TrimSpace(doc.GetCaption()),
			Downloadable: doc,
			Ext:          extFromMimeOrName(mime, doc.GetFileName(), "bin"),
			MimeType:     mime,
		}, true
	}
	return ParsedContent{}, false
}

// ResolveSenderPhone escolhe o identificador E.164 da contraparte, preferindo número real sobre LID.
// Para mensagens próprias (IsFromMe=true), a contraparte é o destinatário (RecipientAlt/Chat), nunca SenderAlt.
func ResolveSenderPhone(src types.MessageSource) (string, error) {
	var candidates []types.JID
	if src.IsFromMe {
		if src.RecipientAlt.Server == types.DefaultUserServer {
			candidates = append(candidates, src.RecipientAlt)
		}
		candidates = append(candidates, src.Chat, src.Sender)
	} else {
		candidates = []types.JID{src.Chat, src.Sender}
		if src.SenderAlt.Server == types.DefaultUserServer {
			candidates = append([]types.JID{src.SenderAlt}, candidates...)
		}
		if src.RecipientAlt.Server == types.DefaultUserServer {
			candidates = append([]types.JID{src.RecipientAlt}, candidates...)
		}
	}
	for _, jid := range candidates {
		if jid.IsEmpty() {
			continue
		}
		if jid.Server == types.DefaultUserServer || jid.Server == types.LegacyUserServer || jid.Server == "" {
			if norm, err := NormalizeE164(jid.User); err == nil {
				return norm, nil
			}
		}
	}
	return NormalizeE164(src.Sender.User)
}

// BuildInboundRequest converte um evento WhatsApp + mediaRef opcional no DTO do protocolo v1.
func BuildInboundRequest(
	tenantID, account, adminPhone string,
	evt *events.Message,
	parsed ParsedContent,
	mediaRef string,
) (bridge.InboundRequest, error) {
	sourceKey, err := ResolveSenderPhone(evt.Info.MessageSource)
	if err != nil {
		return bridge.InboundRequest{}, err
	}
	ts := evt.Info.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	msgID := strings.TrimSpace(evt.Info.ID)
	if msgID == "" {
		msgID = fmt.Sprintf("wa-%d", ts.UnixNano())
	}
	return bridge.InboundRequest{
		TenantID:     tenantID,
		Channel:      "whatsapp",
		Account:      account,
		SourceKey:    sourceKey,
		MessageID:    msgID,
		Timestamp:    ts.UTC(),
		Type:         parsed.Type,
		Text:         parsed.Text,
		MediaRef:     strings.TrimSpace(mediaRef),
		RawContact:   "+" + sourceKey,
		DisplayName:  strings.TrimSpace(evt.Info.PushName),
		FromMe:       evt.Info.IsFromMe,
		RouteToAdmin: IsAdminPhone(sourceKey, adminPhone),
	}, nil
}

func extFromMimeOrName(mimeType, fileName, fallback string) string {
	if ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSpace(fileName))), "."); ext != "" {
		return ext
	}
	lower := strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.Contains(lower, "ogg"):
		return "ogg"
	case strings.Contains(lower, "mpeg") || strings.Contains(lower, "mp3"):
		return "mp3"
	case strings.Contains(lower, "mp4") || strings.Contains(lower, "m4a"):
		return "m4a"
	case strings.Contains(lower, "wav"):
		return "wav"
	case strings.Contains(lower, "jpeg") || strings.Contains(lower, "jpg"):
		return "jpg"
	case strings.Contains(lower, "png"):
		return "png"
	case strings.Contains(lower, "webp"):
		return "webp"
	case strings.Contains(lower, "pdf"):
		return "pdf"
	default:
		return fallback
	}
}
