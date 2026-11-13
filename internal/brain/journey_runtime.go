package brain

import (
	"context"
	"fmt"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

// LoadTenantPack carrega o pack configurado para o tenant (usado pela UI de jornada e what-if).
func (p *Pipeline) LoadTenantPack(ctx context.Context, tenantID string) (*pack.Pack, error) {
	if p.cfg.Tenants == nil || p.cfg.PackLoader == nil {
		return nil, fmt.Errorf("pipeline sem carregador de pack")
	}
	tenantCfg, err := p.cfg.Tenants.LoadTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return p.cfg.PackLoader(tenantCfg.Pack)
}

// EmitSystemEvent injeta um evento de sistema (ex.: system_timeout, session_start, integration_result)
// na FSM de jornada do pack, grava a transição e o outbox de forma atômica e despacha efeitos.
func (p *Pipeline) EmitSystemEvent(
	ctx context.Context,
	tenantID, channelID, sourceKey string,
	sysEvt core.SystemEvent,
) (*NextJourneyOutput, error) {
	if p.cfg.Tenants == nil || p.cfg.PackLoader == nil || p.cfg.Store == nil {
		return nil, fmt.Errorf("pipeline sem dependências para evento de sistema")
	}
	tenantCfg, err := p.cfg.Tenants.LoadTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	pk, err := p.cfg.PackLoader(tenantCfg.Pack)
	if err != nil {
		return nil, err
	}
	if pk.Journey == nil {
		return nil, nil
	}
	state, err := p.cfg.Store.GetSourceState(ctx, tenantID, channelID, sourceKey)
	if err != nil {
		return nil, err
	}
	if sysEvt.OccurredAt.IsZero() {
		sysEvt.OccurredAt = p.now()
	}
	if sysEvt.EventID == "" {
		sysEvt.EventID = newID("sys")
	}
	sysEvt.TenantID = tenantID
	sysEvt.Channel = channelID
	sysEvt.SourceKey = sourceKey

	out := p.evaluateAndCommitJourney(ctx, pk, tenantID, channelID, sourceKey, sysEvt.EventID, nil, state, sysEvt)
	return &out, nil
}

func (p *Pipeline) evaluateAndCommitJourney(
	ctx context.Context,
	pk *pack.Pack,
	tenantID, channelID, sourceKey, runID string,
	analysis *pack.AnalysisResult,
	state *core.SourceState,
	sysEvt core.SystemEvent,
) NextJourneyOutput {
	now := p.now()
	curJourney, _ := p.cfg.Store.GetJourneyState(ctx, tenantID, channelID, sourceKey)

	facts := map[string]string{}
	if state != nil && state.Facts != nil {
		facts = state.Facts
	}
	var signals map[string]bool
	if analysis != nil {
		signals = analysis.Signals
	} else if state != nil {
		signals = state.Signals
	}
	var dims map[string]string
	if analysis != nil {
		dims = analysis.Dimensions
	} else if curJourney != nil {
		dims = curJourney.Dimensions
	}

	jOut := NextJourney(NextJourneyInput{
		Journey:    pk.Journey,
		Current:    curJourney,
		Event:      sysEvt,
		Analysis:   analysis,
		Facts:      facts,
		Signals:    signals,
		Dimensions: dims,
		Now:        now,
	})

	for _, gErr := range jOut.GuardErrors {
		p.publish(core.Event{
			ID:        newID("ev"),
			Type:      core.EventGuardError,
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			RunID:     runID,
			At:        now,
			Payload: map[string]any{
				"rule_id": gErr.RuleID,
				"guard":   gErr.Guard,
				"error":   gErr.Error,
			},
		})
	}

	var expectedVer int64
	enteredAt := now
	turnsInState := 1
	if curJourney != nil {
		expectedVer = curJourney.Version
		if !jOut.Transitioned && !curJourney.EnteredAt.IsZero() {
			enteredAt = curJourney.EnteredAt
			turnsInState = curJourney.TurnsInState + 1
		}
	}

	nextJourneyState := core.JourneyState{
		TenantID:        tenantID,
		Channel:         channelID,
		SourceKey:       sourceKey,
		State:           jOut.NextState,
		ReturnToState:   jOut.ReturnToState,
		EnteredAt:       enteredAt,
		TurnsInState:    turnsInState,
		StagnationCount: jOut.StagnationCount,
		Version:         expectedVer + 1,
		JourneyVersion:  pk.Journey.Version,
		Dimensions:      dims,
		UpdatedAt:       now,
	}

	var transitionRec *core.JourneyTransitionRecord
	if jOut.Transitioned || curJourney == nil {
		transitionRec = &core.JourneyTransitionRecord{
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			FromState: jOut.PrevState,
			ToState:   jOut.NextState,
			EventType: string(sysEvt.Type),
			RuleID:    jOut.RuleID,
			Reason:    jOut.Reason,
			RunID:     runID,
			CreatedAt: now,
		}
	}

	var outboxItems []core.OutboxItem
	for idx, act := range jOut.Actions {
		if act.Type == "reply_directive" {
			continue
		}
		outboxItems = append(outboxItems, core.OutboxItem{
			ID:            fmt.Sprintf("%s#%d", runID, idx),
			TenantID:      tenantID,
			Channel:       channelID,
			SourceKey:     sourceKey,
			RunID:         runID,
			Index:         idx,
			ActionType:    act.Type,
			Params:        act.Params,
			Status:        core.OutboxStatusPending,
			MaxAttempts:   5,
			NextAttemptAt: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}

	if err := p.cfg.Store.CommitJourneyTurn(ctx, core.CommitJourneyTurnInput{
		TenantID:        tenantID,
		Channel:         channelID,
		SourceKey:       sourceKey,
		ExpectedVersion: expectedVer,
		Journey:         nextJourneyState,
		Transition:      transitionRec,
		OutboxItems:     outboxItems,
	}); err != nil {
		p.logger.Warn("falha ao commitar turno de jornada", "error", err, "run_id", runID)
		return jOut
	}

	if jOut.Transitioned {
		p.publish(core.Event{
			ID:        newID("ev"),
			Type:      core.EventJourneyTransition,
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			RunID:     runID,
			At:        now,
			Payload: map[string]any{
				"from_state": jOut.PrevState,
				"to_state":   jOut.NextState,
				"rule_id":    jOut.RuleID,
				"reason":     jOut.Reason,
				"event_type": string(sysEvt.Type),
			},
		})
	}

	for _, item := range outboxItems {
		p.publish(core.Event{
			ID:        newID("ev"),
			Type:      core.EventOutboxQueued,
			TenantID:  tenantID,
			Channel:   channelID,
			SourceKey: sourceKey,
			RunID:     runID,
			At:        now,
			Payload: map[string]any{
				"outbox_id":   item.ID,
				"action_type": item.ActionType,
			},
		})
		if p.outboxWorker != nil {
			_ = p.outboxWorker.DispatchItem(ctx, item)
		}
	}
	return jOut
}
