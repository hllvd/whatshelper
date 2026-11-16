package contextmod

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"whatshelper/internal/core"
)

const (
	DefaultWindowHistoryMax      = 10
	DefaultWindowHistoryCustomer = 5
)

// WindowMessage representa uma mensagem de entrada para construção pura da janela do LLM.
type WindowMessage struct {
	MessageID string    `json:"message_id"`
	Timestamp time.Time `json:"timestamp"`
	Direction string    `json:"direction"`
	Origin    string    `json:"origin,omitempty"`
	Sender    string    `json:"sender,omitempty"`
	Text      string    `json:"text"`
	Deleted   bool      `json:"deleted,omitempty"`
	Answered  bool      `json:"answered"`
}

// LabeledWindowMessage representa uma mensagem rotulada com papel, delta de tempo e marcador de lote.
type LabeledWindowMessage struct {
	MessageID     string    `json:"message_id"`
	Timestamp     time.Time `json:"timestamp"`
	Role          string    `json:"role"` // "customer" | "bot" | "human"
	Sender        string    `json:"sender"`
	Text          string    `json:"text"`
	DeltaFromPrev string    `json:"delta_from_prev"`
	IsNewBatch    bool      `json:"is_new_batch"`
	Answered      bool      `json:"answered"`
}

// WindowInput define os parâmetros explícitos de entrada para BuildWindow.
type WindowInput struct {
	History            []WindowMessage
	Batch              []WindowMessage
	HistoryMaxMessages int
	HistoryMinCustomer int
	TokenBudget        int
	BaseOverheadTokens int
}

// WindowOutput retorna a janela estruturada (histórico + lote intacto), uso de tokens e itens cortados.
type WindowOutput struct {
	History           []LabeledWindowMessage `json:"history"`
	Batch             []LabeledWindowMessage `json:"batch"`
	CombinedBatchText string                 `json:"combined_batch_text"`
	UsedTokens        int                    `json:"used_tokens"`
	Dropped           []DroppedItem          `json:"dropped,omitempty"`
}

// BuildWindow constrói de forma pura e determinística a janela enviada ao LLM (WIN-02).
// Invariante: o lote atual (Batch) nunca é cortado.
func BuildWindow(in WindowInput) WindowOutput {
	maxHist := in.HistoryMaxMessages
	if maxHist <= 0 {
		maxHist = DefaultWindowHistoryMax
	}
	minCust := in.HistoryMinCustomer
	if minCust <= 0 {
		minCust = DefaultWindowHistoryCustomer
	}
	if minCust > maxHist {
		minCust = maxHist
	}

	// 1. Filtra e ordena cronologicamente o lote atual (nunca cortado, exceto mensagens deletadas/vazias).
	batchIDs := make(map[string]bool, len(in.Batch))
	cleanBatch := make([]WindowMessage, 0, len(in.Batch))
	for _, m := range in.Batch {
		if m.MessageID != "" {
			batchIDs[m.MessageID] = true
		}
		if m.Deleted || strings.TrimSpace(m.Text) == "" {
			continue
		}
		cleanBatch = append(cleanBatch, m)
	}
	sortWindowMessages(cleanBatch)

	// 2. Filtra histórico (exclui IDs do lote, deletadas e vazias) e ordena cronologicamente.
	cleanHist := make([]WindowMessage, 0, len(in.History))
	for _, m := range in.History {
		if m.Deleted || strings.TrimSpace(m.Text) == "" {
			continue
		}
		if m.MessageID != "" && batchIDs[m.MessageID] {
			continue
		}
		cleanHist = append(cleanHist, m)
	}
	sortWindowMessages(cleanHist)

	// 3. Aplica limite de janela (maxHist total preservando até minCust mensagens do cliente).
	var dropped []DroppedItem
	selectedHist := cleanHist
	if len(cleanHist) > maxHist {
		keep := make([]bool, len(cleanHist))
		kept := 0
		custKept := 0
		for i := len(cleanHist) - 1; i >= 0 && custKept < minCust && kept < maxHist; i-- {
			if ResolveMessageRole(cleanHist[i].Direction, cleanHist[i].Origin) == "customer" {
				keep[i] = true
				custKept++
				kept++
			}
		}
		for i := len(cleanHist) - 1; i >= 0 && kept < maxHist; i-- {
			if !keep[i] {
				keep[i] = true
				kept++
			}
		}
		selectedHist = make([]WindowMessage, 0, kept)
		for i, m := range cleanHist {
			if keep[i] {
				selectedHist = append(selectedHist, m)
			} else {
				dropped = append(dropped, DroppedItem{
					Block:       "recent_messages",
					ItemID:      m.MessageID,
					Reason:      "window_limit",
					TokensSaved: estimateWindowMessageTokens(m),
				})
			}
		}
	}

	// 4. Aplica orçamento de tokens cortando apenas do histórico (mais antigas primeiro), nunca do lote.
	if in.TokenBudget > 0 {
		for len(selectedHist) > 0 && measureWindowTokens(selectedHist, cleanBatch, in.BaseOverheadTokens) > in.TokenBudget {
			oldest := selectedHist[0]
			selectedHist = selectedHist[1:]
			dropped = append(dropped, DroppedItem{
				Block:       "recent_messages",
				ItemID:      oldest.MessageID,
				Reason:      "budget_exceeded",
				TokensSaved: estimateWindowMessageTokens(oldest),
			})
		}
	}

	// 5. Rotula papel e calcula DeltaFromPrev na linha do tempo combinada [History..., Batch...].
	var prevTS time.Time
	hasPrev := false

	outHist := make([]LabeledWindowMessage, len(selectedHist))
	for i, m := range selectedHist {
		delta := "0s"
		if hasPrev {
			delta = FormatTimeDelta(m.Timestamp.Sub(prevTS))
		}
		prevTS = m.Timestamp
		hasPrev = true
		role := ResolveMessageRole(m.Direction, m.Origin)
		sender := strings.TrimSpace(m.Sender)
		if sender == "" {
			sender = role
		}
		outHist[i] = LabeledWindowMessage{
			MessageID:     m.MessageID,
			Timestamp:     m.Timestamp,
			Role:          role,
			Sender:        sender,
			Text:          m.Text,
			DeltaFromPrev: delta,
			IsNewBatch:    false,
			Answered:      m.Answered,
		}
	}

	outBatch := make([]LabeledWindowMessage, len(cleanBatch))
	batchLines := make([]string, 0, len(cleanBatch))
	for i, m := range cleanBatch {
		delta := "0s"
		if hasPrev {
			delta = FormatTimeDelta(m.Timestamp.Sub(prevTS))
		}
		prevTS = m.Timestamp
		hasPrev = true
		role := ResolveMessageRole(m.Direction, m.Origin)
		sender := strings.TrimSpace(m.Sender)
		if sender == "" {
			sender = role
		}
		outBatch[i] = LabeledWindowMessage{
			MessageID:     m.MessageID,
			Timestamp:     m.Timestamp,
			Role:          role,
			Sender:        sender,
			Text:          m.Text,
			DeltaFromPrev: delta,
			IsNewBatch:    true,
			Answered:      false,
		}
		batchLines = append(batchLines, strings.TrimSpace(m.Text))
	}

	return WindowOutput{
		History:           outHist,
		Batch:             outBatch,
		CombinedBatchText: strings.Join(batchLines, "\n"),
		UsedTokens:        measureWindowTokens(selectedHist, cleanBatch, in.BaseOverheadTokens),
		Dropped:           dropped,
	}
}

// ResolveMessageRole determina de forma pura o papel semântico ("customer", "bot", "human").
func ResolveMessageRole(direction, origin string) string {
	orig := strings.ToLower(strings.TrimSpace(origin))
	switch orig {
	case string(core.OriginHumanPhone), string(core.OriginHumanAdmin), "human":
		return "human"
	case string(core.OriginBot):
		return "bot"
	case string(core.OriginCustomer):
		return "customer"
	}
	if strings.EqualFold(strings.TrimSpace(direction), string(core.DirectionOutbound)) {
		return "bot"
	}
	return "customer"
}

// FormatTimeDelta formata de forma pura e compacta o intervalo entre duas mensagens.
func FormatTimeDelta(d time.Duration) string {
	if d <= 0 {
		return "+0s"
	}
	secs := int(d.Round(time.Second).Seconds())
	if secs < 60 {
		return fmt.Sprintf("+%ds", secs)
	}
	mins := secs / 60
	remSecs := secs % 60
	if mins < 60 {
		if remSecs == 0 {
			return fmt.Sprintf("+%dm", mins)
		}
		return fmt.Sprintf("+%dm%ds", mins, remSecs)
	}
	hours := mins / 60
	remMins := mins % 60
	if remMins == 0 {
		return fmt.Sprintf("+%dh", hours)
	}
	return fmt.Sprintf("+%dh%dm", hours, remMins)
}

func sortWindowMessages(msgs []WindowMessage) {
	sort.SliceStable(msgs, func(i, j int) bool {
		return msgs[i].Timestamp.Before(msgs[j].Timestamp)
	})
}

func estimateWindowMessageTokens(m WindowMessage) int {
	role := ResolveMessageRole(m.Direction, m.Origin)
	return EstimateTokens(role + ": " + m.Text)
}

func measureWindowTokens(hist, batch []WindowMessage, overhead int) int {
	total := overhead
	if total < 0 {
		total = 0
	}
	for _, m := range hist {
		total += estimateWindowMessageTokens(m)
	}
	for _, m := range batch {
		total += estimateWindowMessageTokens(m)
	}
	return total
}
