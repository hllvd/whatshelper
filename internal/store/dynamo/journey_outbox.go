package dynamo

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"whatshelper/internal/core"
	"whatshelper/internal/store"
)

type outboundRecordItem struct {
	PK        string                    `dynamodbav:"PK"`
	SK        string                    `dynamodbav:"SK"`
	TenantID  string                    `dynamodbav:"tenant_id"`
	Channel   string                    `dynamodbav:"channel"`
	SourceKey string                    `dynamodbav:"source_key"`
	MessageID string                    `dynamodbav:"message_id"`
	OutboxID  string                    `dynamodbav:"outbox_id,omitempty"`
	Status    core.OutboundRecordStatus `dynamodbav:"status"`
	TTL       int64                     `dynamodbav:"ttl,omitempty"`
	CreatedAt string                    `dynamodbav:"created_at"`
	UpdatedAt string                    `dynamodbav:"updated_at"`
}

type journeyItem struct {
	PK             string            `dynamodbav:"PK"`
	SK             string            `dynamodbav:"SK"`
	TenantID       string            `dynamodbav:"tenant_id"`
	Channel        string            `dynamodbav:"channel"`
	SourceKey      string            `dynamodbav:"source_key"`
	State          string            `dynamodbav:"state"`
	EnteredAt      string            `dynamodbav:"entered_at"`
	TurnsInState   int               `dynamodbav:"turns_in_state"`
	Version        int64             `dynamodbav:"version"`
	JourneyVersion string            `dynamodbav:"journey_version,omitempty"`
	Dimensions     map[string]string `dynamodbav:"dimensions,omitempty"`
	UpdatedAt      string            `dynamodbav:"updated_at"`
}

type jstateItem struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	TenantID  string `dynamodbav:"tenant_id"`
	Channel   string `dynamodbav:"channel"`
	SourceKey string `dynamodbav:"source_key"`
	FromState string `dynamodbav:"from_state"`
	ToState   string `dynamodbav:"to_state"`
	EventType string `dynamodbav:"event_type"`
	RuleID    string `dynamodbav:"rule_id,omitempty"`
	Reason    string `dynamodbav:"reason,omitempty"`
	RunID     string `dynamodbav:"run_id"`
	CreatedAt string `dynamodbav:"created_at"`
}

type outboxDynamoItem struct {
	PK            string            `dynamodbav:"PK"`
	SK            string            `dynamodbav:"SK"`
	GSI2PK        string            `dynamodbav:"GSI2PK,omitempty"`
	GSI2SK        string            `dynamodbav:"GSI2SK,omitempty"`
	ID            string            `dynamodbav:"id"`
	TenantID      string            `dynamodbav:"tenant_id"`
	Channel       string            `dynamodbav:"channel"`
	SourceKey     string            `dynamodbav:"source_key"`
	RunID         string            `dynamodbav:"run_id"`
	Index         int               `dynamodbav:"index"`
	ActionType    string            `dynamodbav:"action_type"`
	ActionName    string            `dynamodbav:"action_name,omitempty"`
	MessageID     string            `dynamodbav:"message_id,omitempty"`
	ParamsJSON    string            `dynamodbav:"params_json,omitempty"`
	Status        core.OutboxStatus `dynamodbav:"status"`
	Attempts      int               `dynamodbav:"attempts"`
	MaxAttempts   int               `dynamodbav:"max_attempts"`
	NextAttemptAt string            `dynamodbav:"next_attempt_at"`
	LastError     string            `dynamodbav:"last_error,omitempty"`
	CreatedAt     string            `dynamodbav:"created_at"`
	UpdatedAt     string            `dynamodbav:"updated_at"`
}

func (s *Store) PreRegisterOutbound(ctx context.Context, rec core.OutboundMessageRecord) error {
	now := rec.CreatedAt
	if now.IsZero() {
		now = s.clock.Now().UTC()
	}
	updated := rec.UpdatedAt
	if updated.IsZero() {
		updated = now
	}
	status := rec.Status
	if status == "" {
		status = core.OutboundStatusReserved
	}
	av, err := attributevalue.MarshalMap(outboundRecordItem{
		PK:        store.OutboundPK(rec.TenantID, rec.Channel),
		SK:        store.OutboundSK(rec.MessageID),
		TenantID:  rec.TenantID,
		Channel:   rec.Channel,
		SourceKey: rec.SourceKey,
		MessageID: rec.MessageID,
		OutboxID:  rec.OutboxID,
		Status:    status,
		TTL:       rec.TTL,
		CreatedAt: formatTime(now),
		UpdatedAt: formatTime(updated),
	})
	if err != nil {
		return err
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      av,
	})
	return err
}

func (s *Store) GetOutboundRecord(ctx context.Context, tenant, channel, messageID string) (*core.OutboundMessageRecord, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: store.OutboundPK(tenant, channel)},
			"SK": &types.AttributeValueMemberS{Value: store.OutboundSK(messageID)},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo GetOutboundRecord falhou: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	var item outboundRecordItem
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return nil, err
	}
	if item.TTL > 0 && s.clock.Now().UTC().Unix() > item.TTL {
		return nil, nil
	}
	return &core.OutboundMessageRecord{
		TenantID:  item.TenantID,
		Channel:   item.Channel,
		SourceKey: item.SourceKey,
		MessageID: item.MessageID,
		OutboxID:  item.OutboxID,
		Status:    item.Status,
		TTL:       item.TTL,
		CreatedAt: parseTime(item.CreatedAt),
		UpdatedAt: parseTime(item.UpdatedAt),
	}, nil
}

func (s *Store) UpdateOutboundStatus(ctx context.Context, tenant, channel, messageID string, status core.OutboundRecordStatus, now time.Time) error {
	if now.IsZero() {
		now = s.clock.Now().UTC()
	}
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: store.OutboundPK(tenant, channel)},
			"SK": &types.AttributeValueMemberS{Value: store.OutboundSK(messageID)},
		},
		ConditionExpression: aws.String("attribute_exists(PK)"),
		UpdateExpression:    aws.String("SET #st = :st, updated_at = :upd"),
		ExpressionAttributeNames: map[string]string{
			"#st": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":st":  &types.AttributeValueMemberS{Value: string(status)},
			":upd": &types.AttributeValueMemberS{Value: formatTime(now)},
		},
	})
	if err != nil && isConditionalCheckFailed(err) {
		return nil
	}
	return err
}

func (s *Store) GetJourneyState(ctx context.Context, tenant, channel, sourceKey string) (*core.JourneyState, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: store.SourcePK(tenant, channel, sourceKey)},
			"SK": &types.AttributeValueMemberS{Value: store.SKJourney},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo GetJourneyState falhou: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	var item journeyItem
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return nil, err
	}
	return &core.JourneyState{
		TenantID:       item.TenantID,
		Channel:        item.Channel,
		SourceKey:      item.SourceKey,
		State:          item.State,
		EnteredAt:      parseTime(item.EnteredAt),
		TurnsInState:   item.TurnsInState,
		Version:        item.Version,
		JourneyVersion: item.JourneyVersion,
		Dimensions:     item.Dimensions,
		UpdatedAt:      parseTime(item.UpdatedAt),
	}, nil
}

func (s *Store) ListJourneyTransitions(ctx context.Context, tenant, channel, sourceKey string, limit int) ([]core.JourneyTransitionRecord, error) {
	input := &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: store.SourcePK(tenant, channel, sourceKey)},
			":prefix": &types.AttributeValueMemberS{Value: "JSTATE#"},
		},
		ScanIndexForward: aws.Bool(false),
		ConsistentRead:   aws.Bool(true),
	}
	if limit > 0 {
		input.Limit = aws.Int32(int32(limit))
	}
	out, err := s.client.Query(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("dynamo ListJourneyTransitions falhou: %w", err)
	}
	res := make([]core.JourneyTransitionRecord, 0, len(out.Items))
	for _, raw := range out.Items {
		var item jstateItem
		if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
			return nil, err
		}
		res = append(res, core.JourneyTransitionRecord{
			TenantID:  item.TenantID,
			Channel:   item.Channel,
			SourceKey: item.SourceKey,
			FromState: item.FromState,
			ToState:   item.ToState,
			EventType: item.EventType,
			RuleID:    item.RuleID,
			Reason:    item.Reason,
			RunID:     item.RunID,
			CreatedAt: parseTime(item.CreatedAt),
		})
	}
	return res, nil
}

func (s *Store) CommitJourneyTurn(ctx context.Context, input core.CommitJourneyTurnInput) error {
	now := input.UpdatedAt
	if now.IsZero() {
		now = s.clock.Now().UTC()
	}

	pk := store.SourcePK(input.TenantID, input.Channel, input.SourceKey)
	newVersion := input.ExpectedVersion + 1

	avJourney, err := attributevalue.MarshalMap(journeyItem{
		PK:             pk,
		SK:             store.SKJourney,
		TenantID:       input.TenantID,
		Channel:        input.Channel,
		SourceKey:      input.SourceKey,
		State:          input.Journey.State,
		EnteredAt:      formatTime(input.Journey.EnteredAt),
		TurnsInState:   input.Journey.TurnsInState,
		Version:        newVersion,
		JourneyVersion: input.Journey.JourneyVersion,
		Dimensions:     input.Journey.Dimensions,
		UpdatedAt:      formatTime(now),
	})
	if err != nil {
		return err
	}

	var cond *string
	var exprNames map[string]string
	var exprValues map[string]types.AttributeValue
	if input.ExpectedVersion == 0 {
		cond = aws.String("attribute_not_exists(PK) OR #ver = :expected")
		exprNames = map[string]string{"#ver": "version"}
		exprValues = map[string]types.AttributeValue{
			":expected": &types.AttributeValueMemberN{Value: "0"},
		}
	} else {
		cond = aws.String("#ver = :expected")
		exprNames = map[string]string{"#ver": "version"}
		exprValues = map[string]types.AttributeValue{
			":expected": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", input.ExpectedVersion)},
		}
	}

	txItems := []types.TransactWriteItem{
		{
			Put: &types.Put{
				TableName:                 aws.String(s.tableName),
				Item:                      avJourney,
				ConditionExpression:       cond,
				ExpressionAttributeNames:  exprNames,
				ExpressionAttributeValues: exprValues,
			},
		},
	}

	if input.Transition != nil {
		tr := *input.Transition
		ts := tr.CreatedAt
		if ts.IsZero() {
			ts = now
		}
		avTr, err := attributevalue.MarshalMap(jstateItem{
			PK:        pk,
			SK:        store.JStateSK(ts, tr.RunID),
			TenantID:  input.TenantID,
			Channel:   input.Channel,
			SourceKey: input.SourceKey,
			FromState: tr.FromState,
			ToState:   tr.ToState,
			EventType: tr.EventType,
			RuleID:    tr.RuleID,
			Reason:    tr.Reason,
			RunID:     tr.RunID,
			CreatedAt: formatTime(ts),
		})
		if err != nil {
			return err
		}
		txItems = append(txItems, types.TransactWriteItem{
			Put: &types.Put{TableName: aws.String(s.tableName), Item: avTr},
		})
	}

	for _, ob := range input.OutboxItems {
		if ob.CreatedAt.IsZero() {
			ob.CreatedAt = now
		}
		if ob.UpdatedAt.IsZero() {
			ob.UpdatedAt = now
		}
		if ob.NextAttemptAt.IsZero() {
			ob.NextAttemptAt = now
		}
		avOb, err := marshalOutboxItem(ob)
		if err != nil {
			return err
		}
		txItems = append(txItems, types.TransactWriteItem{
			Put: &types.Put{
				TableName:           aws.String(s.tableName),
				Item:                avOb,
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			},
		})
	}

	for _, rec := range input.OutboundRecords {
		created := rec.CreatedAt
		if created.IsZero() {
			created = now
		}
		updated := rec.UpdatedAt
		if updated.IsZero() {
			updated = now
		}
		st := rec.Status
		if st == "" {
			st = core.OutboundStatusReserved
		}
		avRec, err := attributevalue.MarshalMap(outboundRecordItem{
			PK:        store.OutboundPK(rec.TenantID, rec.Channel),
			SK:        store.OutboundSK(rec.MessageID),
			TenantID:  rec.TenantID,
			Channel:   rec.Channel,
			SourceKey: rec.SourceKey,
			MessageID: rec.MessageID,
			OutboxID:  rec.OutboxID,
			Status:    st,
			TTL:       rec.TTL,
			CreatedAt: formatTime(created),
			UpdatedAt: formatTime(updated),
		})
		if err != nil {
			return err
		}
		txItems = append(txItems, types.TransactWriteItem{
			Put: &types.Put{TableName: aws.String(s.tableName), Item: avRec},
		})
	}

	_, err = s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: txItems,
	})
	if err != nil {
		if isConditionalCheckFailed(err) {
			return fmt.Errorf("%w: journey conflito na transação", core.ErrVersionConflict)
		}
		return fmt.Errorf("dynamo CommitJourneyTurn falhou: %w", err)
	}
	return nil
}

func (s *Store) ListDueOutboxItems(ctx context.Context, tenant string, dueBefore time.Time, limit int) ([]core.OutboxItem, error) {
	input := &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String("GSI2"),
		KeyConditionExpression: aws.String("GSI2PK = :pk AND GSI2SK <= :due"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":  &types.AttributeValueMemberS{Value: store.OutboxDueGSI2PK(tenant)},
			":due": &types.AttributeValueMemberS{Value: store.OutboxDueGSI2SK(dueBefore, "~")},
		},
	}
	if limit > 0 {
		input.Limit = aws.Int32(int32(limit))
	}
	out, err := s.client.Query(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("dynamo ListDueOutboxItems falhou: %w", err)
	}
	res := make([]core.OutboxItem, 0, len(out.Items))
	for _, raw := range out.Items {
		item, err := unmarshalOutboxItem(raw)
		if err != nil {
			return nil, err
		}
		if item.Status == core.OutboxStatusPending || item.Status == core.OutboxStatusFailed {
			res = append(res, item)
		}
	}
	return res, nil
}

func (s *Store) ListOutboxItems(ctx context.Context, tenant, channel, sourceKey string, limit int) ([]core.OutboxItem, error) {
	if sourceKey != "" {
		input := &dynamodb.QueryInput{
			TableName:              aws.String(s.tableName),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk":     &types.AttributeValueMemberS{Value: store.SourcePK(tenant, channel, sourceKey)},
				":prefix": &types.AttributeValueMemberS{Value: "OUTBOX#"},
			},
			ConsistentRead: aws.Bool(true),
		}
		out, err := s.client.Query(ctx, input)
		if err != nil {
			return nil, err
		}
		res := make([]core.OutboxItem, 0, len(out.Items))
		for _, raw := range out.Items {
			it, err := unmarshalOutboxItem(raw)
			if err != nil {
				return nil, err
			}
			res = append(res, it)
		}
		sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.After(res[j].CreatedAt) })
		if limit > 0 && len(res) > limit {
			res = res[:limit]
		}
		return res, nil
	}

	// Lista por tenant via sources
	sources, err := s.ListSources(ctx, tenant)
	if err != nil {
		return nil, err
	}
	var all []core.OutboxItem
	for _, src := range sources {
		if channel != "" && src.Channel != channel {
			continue
		}
		items, err := s.ListOutboxItems(ctx, tenant, src.Channel, src.SourceKey, 0)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func (s *Store) GetOutboxItem(ctx context.Context, tenant, channel, sourceKey, outboxID string) (*core.OutboxItem, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: store.SourcePK(tenant, channel, sourceKey)},
			"SK": &types.AttributeValueMemberS{Value: store.OutboxSK(outboxID)},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	it, err := unmarshalOutboxItem(out.Item)
	if err != nil {
		return nil, err
	}
	return &it, nil
}

func (s *Store) UpdateOutboxItem(ctx context.Context, item core.OutboxItem) error {
	av, err := marshalOutboxItem(item)
	if err != nil {
		return err
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      av,
	})
	return err
}

func marshalOutboxItem(ob core.OutboxItem) (map[string]types.AttributeValue, error) {
	var paramsJSON string
	if len(ob.Params) > 0 {
		b, err := json.Marshal(ob.Params)
		if err != nil {
			return nil, err
		}
		paramsJSON = string(b)
	}
	item := outboxDynamoItem{
		PK:            store.SourcePK(ob.TenantID, ob.Channel, ob.SourceKey),
		SK:            store.OutboxSK(ob.ID),
		ID:            ob.ID,
		TenantID:      ob.TenantID,
		Channel:       ob.Channel,
		SourceKey:     ob.SourceKey,
		RunID:         ob.RunID,
		Index:         ob.Index,
		ActionType:    ob.ActionType,
		ActionName:    ob.ActionName,
		MessageID:     ob.MessageID,
		ParamsJSON:    paramsJSON,
		Status:        ob.Status,
		Attempts:      ob.Attempts,
		MaxAttempts:   ob.MaxAttempts,
		NextAttemptAt: formatTime(ob.NextAttemptAt),
		LastError:     ob.LastError,
		CreatedAt:     formatTime(ob.CreatedAt),
		UpdatedAt:     formatTime(ob.UpdatedAt),
	}
	if ob.Status == core.OutboxStatusPending || ob.Status == core.OutboxStatusFailed {
		item.GSI2PK = store.OutboxDueGSI2PK(ob.TenantID)
		item.GSI2SK = store.OutboxDueGSI2SK(ob.NextAttemptAt, ob.ID)
	}
	return attributevalue.MarshalMap(item)
}

func unmarshalOutboxItem(raw map[string]types.AttributeValue) (core.OutboxItem, error) {
	var d outboxDynamoItem
	if err := attributevalue.UnmarshalMap(raw, &d); err != nil {
		return core.OutboxItem{}, err
	}
	var params map[string]any
	if d.ParamsJSON != "" {
		_ = json.Unmarshal([]byte(d.ParamsJSON), &params)
	}
	return core.OutboxItem{
		ID:            d.ID,
		TenantID:      d.TenantID,
		Channel:       d.Channel,
		SourceKey:     d.SourceKey,
		RunID:         d.RunID,
		Index:         d.Index,
		ActionType:    d.ActionType,
		ActionName:    d.ActionName,
		MessageID:     d.MessageID,
		Params:        params,
		Status:        d.Status,
		Attempts:      d.Attempts,
		MaxAttempts:   d.MaxAttempts,
		NextAttemptAt: parseTime(d.NextAttemptAt),
		LastError:     d.LastError,
		CreatedAt:     parseTime(d.CreatedAt),
		UpdatedAt:     parseTime(d.UpdatedAt),
	}, nil
}
