package brain

import (
	"sort"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

// StageFunnelMetric resume a distribuição e tempo médio de permanência em um estágio da jornada.
type StageFunnelMetric struct {
	Stage            string  `json:"stage"`
	Count            int     `json:"count"`
	AvgTimeMinutes   float64 `json:"avg_time_minutes"`
	StagnatedCount   int     `json:"stagnated_count"`
	Terminal         bool    `json:"terminal"`
}

// ConversionMetric mede as transições observadas entre dois estágios.
type ConversionMetric struct {
	FromState string  `json:"from_state"`
	ToState   string  `json:"to_state"`
	Count     int     `json:"count"`
	RatePct   float64 `json:"rate_pct"`
}

// FunnelMetrics agrega métricas operacionais de funil (OBS-06).
type FunnelMetrics struct {
	TotalConversations int                 `json:"total_conversations"`
	StagnatedCount     int                 `json:"stagnated_count"`
	AbandonedCount     int                 `json:"abandoned_count"`
	Stages             []StageFunnelMetric `json:"stages"`
	Conversions        []ConversionMetric  `json:"conversions"`
}

// ComputeFunnelMetrics calcula de forma pura tempo por estágio, conversão, abandono e estagnação.
func ComputeFunnelMetrics(
	journeys []core.JourneyState,
	transitions []core.JourneyTransitionRecord,
	jDef *pack.JourneyDef,
	now time.Time,
) FunnelMetrics {
	out := FunnelMetrics{
		TotalConversations: len(journeys),
	}

	stageSumMins := make(map[string]float64)
	stageCounts := make(map[string]int)
	stageStag := make(map[string]int)

	if jDef != nil {
		for name := range jDef.States {
			stageCounts[name] = 0
		}
	}

	for _, j := range journeys {
		st := j.State
		if st == "" && jDef != nil {
			st = jDef.InitialState
		}
		stageCounts[st]++
		if !j.EnteredAt.IsZero() && !now.IsZero() && now.After(j.EnteredAt) {
			stageSumMins[st] += now.Sub(j.EnteredAt).Minutes()
		}

		threshold := 0
		isTerminal := false
		if jDef != nil {
			threshold = jDef.Defaults.StagnationThreshold
			if sDef, ok := jDef.States[st]; ok {
				if sDef.StagnationThreshold > 0 {
					threshold = sDef.StagnationThreshold
				}
				isTerminal = sDef.Terminal
			}
		}
		if threshold > 0 && j.StagnationCount >= threshold && !isTerminal {
			stageStag[st]++
			out.StagnatedCount++
		}
		if isTerminal && (st == "closed_lost" || st == "abandoned" || st == "dormant") {
			out.AbandonedCount++
		}
	}

	stageNames := make([]string, 0, len(stageCounts))
	for name := range stageCounts {
		stageNames = append(stageNames, name)
	}
	sort.Strings(stageNames)

	for _, name := range stageNames {
		cnt := stageCounts[name]
		avg := 0.0
		if cnt > 0 {
			avg = stageSumMins[name] / float64(cnt)
		}
		terminal := false
		if jDef != nil {
			if sDef, ok := jDef.States[name]; ok {
				terminal = sDef.Terminal
			}
		}
		out.Stages = append(out.Stages, StageFunnelMetric{
			Stage:          name,
			Count:          cnt,
			AvgTimeMinutes: avg,
			StagnatedCount: stageStag[name],
			Terminal:       terminal,
		})
	}

	fromTotals := make(map[string]int)
	pairCounts := make(map[string]map[string]int)
	for _, tr := range transitions {
		if tr.FromState == "" || tr.ToState == "" || tr.FromState == tr.ToState {
			continue
		}
		fromTotals[tr.FromState]++
		if pairCounts[tr.FromState] == nil {
			pairCounts[tr.FromState] = make(map[string]int)
		}
		pairCounts[tr.FromState][tr.ToState]++
	}

	var convs []ConversionMetric
	for from, tos := range pairCounts {
		total := fromTotals[from]
		for to, cnt := range tos {
			rate := 0.0
			if total > 0 {
				rate = (float64(cnt) / float64(total)) * 100.0
			}
			convs = append(convs, ConversionMetric{
				FromState: from,
				ToState:   to,
				Count:     cnt,
				RatePct:   rate,
			})
		}
	}
	sort.Slice(convs, func(i, j int) bool {
		if convs[i].FromState != convs[j].FromState {
			return convs[i].FromState < convs[j].FromState
		}
		return convs[i].ToState < convs[j].ToState
	})
	out.Conversions = convs
	return out
}
