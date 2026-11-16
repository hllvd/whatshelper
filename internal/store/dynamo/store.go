package dynamo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"whatshelper/internal/core"
	"whatshelper/internal/store"
)

// Store implementa core.Store sobre uma única tabela no DynamoDB.
type Store struct {
	client    *dynamodb.Client
	tableName string
	clock     core.Clock
}

// NewStore cria uma instância de repositório DynamoDB.
func NewStore(client *dynamodb.Client, tableName string, clock core.Clock) *Store {
	if clock == nil {
		clock = core.SystemClock{}
	}
	return &Store{
		client:    client,
		tableName: tableName,
		clock:     clock,
	}
}

type metaItem struct {
	PK                   string            `dynamodbav:"PK"`
	SK                   string            `dynamodbav:"SK"`
	GSI1PK               string            `dynamodbav:"GSI1PK,omitempty"`
	GSI1SK               string            `dynamodbav:"GSI1SK,omitempty"`
	TenantID             string            `dynamodbav:"tenant_id"`
	Channel              string            `dynamodbav:"channel"`
	SourceKey            string            `dynamodbav:"source_key"`
	ContactID            string            `dynamodbav:"contact_id,omitempty"`
	Status               core.SourceStatus `dynamodbav:"status"`
	JourneyStage         string            `dynamodbav:"journey_stage,omitempty"`
	PausedUntil          string            `dynamodbav:"paused_until,omitempty"`
	Version              int64             `dynamodbav:"version"`
	EncryptedContactInfo string            `dynamodbav:"encrypted_contact_info,omitempty"`
	CreatedAt            string            `dynamodbav:"created_at"`
	UpdatedAt            string            `dynamodbav:"updated_at"`
}

type summaryItem struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	TenantID  string `dynamodbav:"tenant_id"`
	Channel   string `dynamodbav:"channel"`
	SourceKey string `dynamodbav:"source_key"`
	Text      string `dynamodbav:"text"`
	TurnCount int    `dynamodbav:"turn_count"`
	UpdatedAt string `dynamodbav:"updated_at"`
}

type factsItem struct {
	PK        string            `dynamodbav:"PK"`
	SK        string            `dynamodbav:"SK"`
	Facts     map[string]string `dynamodbav:"facts"`
	UpdatedAt string            `dynamodbav:"updated_at"`
}

type signalsItem struct {
	PK        string          `dynamodbav:"PK"`
	SK        string          `dynamodbav:"SK"`
	Signals   map[string]bool `dynamodbav:"signals"`
	UpdatedAt string          `dynamodbav:"updated_at"`
}

type contextItem struct {
	PK             string                       `dynamodbav:"PK"`
	SK             string                       `dynamodbav:"SK"`
	LastActionType string                       `dynamodbav:"last_action_type,omitempty"`
	LastActionBy   string                       `dynamodbav:"last_action_by,omitempty"`
	Description    string                       `dynamodbav:"description,omitempty"`
	Details        map[string]string            `dynamodbav:"details,omitempty"`
	Sections       map[string]map[string]string `dynamodbav:"sections,omitempty"`
	UpdatedAt      string                       `dynamodbav:"updated_at"`
}

type messageItem struct {
	PK        string           `dynamodbav:"PK"`
	SK        string           `dynamodbav:"SK"`
	TenantID  string           `dynamodbav:"tenant_id"`
	Channel   string           `dynamodbav:"channel"`
	SourceKey string           `dynamodbav:"source_key"`
	MessageID string           `dynamodbav:"message_id"`
	Timestamp string           `dynamodbav:"timestamp"`
	Direction core.Direction     `dynamodbav:"direction"`
	Type      core.MessageType   `dynamodbav:"type"`
	Origin    core.MessageOrigin `dynamodbav:"origin,omitempty"`
	MediaRef  string             `dynamodbav:"media_ref,omitempty"`
	RawRef    string             `dynamodbav:"raw_ref,omitempty"`
	Sender    string             `dynamodbav:"sender,omitempty"`
	Answered  bool               `dynamodbav:"answered"`
}

type dedupItem struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	MessageID string `dynamodbav:"message_id"`
	CreatedAt string `dynamodbav:"created_at"`
}

type sourceCatalogItem struct {
	PK        string            `dynamodbav:"PK"`
	SK        string            `dynamodbav:"SK"`
	TenantID  string            `dynamodbav:"tenant_id"`
	Channel   string            `dynamodbav:"channel"`
	SourceKey string            `dynamodbav:"source_key"`
	Status    core.SourceStatus `dynamodbav:"status"`
	UpdatedAt string            `dynamodbav:"updated_at"`
}

type taskItem struct {
	PK           string            `dynamodbav:"PK"`
	SK           string            `dynamodbav:"SK"`
	GSI2PK       string            `dynamodbav:"GSI2PK,omitempty"`
	GSI2SK       string            `dynamodbav:"GSI2SK,omitempty"`
	ID           string            `dynamodbav:"id"`
	TenantID     string            `dynamodbav:"tenant_id"`
	Channel      string            `dynamodbav:"channel"`
	SourceKey    string            `dynamodbav:"source_key"`
	Kind         string            `dynamodbav:"kind,omitempty"`
	Title        string            `dynamodbav:"title"`
	Type         string            `dynamodbav:"type"`
	Status       core.TaskStatus   `dynamodbav:"status"`
	DueAt        string            `dynamodbav:"due_at,omitempty"`
	DraftBlobRef string            `dynamodbav:"draft_blob_ref,omitempty"`
	Attempt      int               `dynamodbav:"attempt,omitempty"`
	MaxAttempts  int               `dynamodbav:"max_attempts,omitempty"`
	Metadata     map[string]string `dynamodbav:"metadata,omitempty"`
	CreatedAt    string            `dynamodbav:"created_at"`
	CompletedAt  string            `dynamodbav:"completed_at,omitempty"`
}

type runItem struct {
	PK               string `dynamodbav:"PK"`
	SK               string `dynamodbav:"SK"`
	ID               string `dynamodbav:"id"`
	TenantID         string `dynamodbav:"tenant_id"`
	Channel          string `dynamodbav:"channel"`
	SourceKey        string `dynamodbav:"source_key"`
	MessageID        string `dynamodbav:"message_id"`
	Stage            string `dynamodbav:"stage"`
	Status           string `dynamodbav:"status"`
	Model            string `dynamodbav:"model"`
	PromptTokens     int    `dynamodbav:"prompt_tokens"`
	CompletionTokens int    `dynamodbav:"completion_tokens"`
	DurationMs       int64  `dynamodbav:"duration_ms"`
	TraceBlobRef     string `dynamodbav:"trace_blob_ref,omitempty"`
	TTL              int64  `dynamodbav:"ttl,omitempty"`
	CreatedAt        string `dynamodbav:"created_at"`
}

type tenantConfigItem struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	TenantID  string `dynamodbav:"tenant_id"`
	Name      string `dynamodbav:"name"`
	Data      []byte `dynamodbav:"data"`
	UpdatedAt string `dynamodbav:"updated_at"`
}

type contactLinkItem struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	GSI1PK    string `dynamodbav:"GSI1PK"`
	GSI1SK    string `dynamodbav:"GSI1SK"`
	TenantID  string `dynamodbav:"tenant_id"`
	ContactID string `dynamodbav:"contact_id"`
	Channel   string `dynamodbav:"channel"`
	SourceKey string `dynamodbav:"source_key"`
	LinkedAt  string `dynamodbav:"linked_at"`
}

func (s *Store) GetSourceState(ctx context.Context, tenant, channel, sourceKey string) (*core.SourceState, error) {
	pk := store.SourcePK(tenant, channel, sourceKey)
	keys := make([]map[string]types.AttributeValue, 0, 5)
	for _, sk := range []string{store.SKMeta, store.SKSummary, store.SKFacts, store.SKSignals, store.SKContext} {
		keys = append(keys, map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		})
	}

	out, err := s.client.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
		RequestItems: map[string]types.KeysAndAttributes{
			s.tableName: {
				Keys:           keys,
				ConsistentRead: aws.Bool(true),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo BatchGetItem falhou: %w", err)
	}

	state := &core.SourceState{
		Version: 0,
		Meta: core.SourceMeta{
			TenantID:  tenant,
			Channel:   channel,
			SourceKey: sourceKey,
			Status:    core.SourceStatusActive,
			Version:   0,
		},
		Summary: core.Summary{
			TenantID:  tenant,
			Channel:   channel,
			SourceKey: sourceKey,
		},
		Facts:   make(map[string]string),
		Signals: make(map[string]bool),
		Context: core.ConversationContext{},
	}

	for _, raw := range out.Responses[s.tableName] {
		skAttr, ok := raw["SK"].(*types.AttributeValueMemberS)
		if !ok {
			continue
		}
		switch skAttr.Value {
		case store.SKMeta:
			var item metaItem
			if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
				return nil, fmt.Errorf("erro ao decodificar META: %w", err)
			}
			state.Version = item.Version
			state.Meta = core.SourceMeta{
				TenantID:             item.TenantID,
				Channel:              item.Channel,
				SourceKey:            item.SourceKey,
				ContactID:            item.ContactID,
				Status:               item.Status,
				JourneyStage:         item.JourneyStage,
				PausedUntil:          parseTimePtr(item.PausedUntil),
				Version:              item.Version,
				EncryptedContactInfo: item.EncryptedContactInfo,
				CreatedAt:            parseTime(item.CreatedAt),
				UpdatedAt:            parseTime(item.UpdatedAt),
			}
		case store.SKSummary:
			var item summaryItem
			if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
				return nil, fmt.Errorf("erro ao decodificar SUMMARY: %w", err)
			}
			state.Summary = core.Summary{
				TenantID:  item.TenantID,
				Channel:   item.Channel,
				SourceKey: item.SourceKey,
				Text:      item.Text,
				TurnCount: item.TurnCount,
				UpdatedAt: parseTime(item.UpdatedAt),
			}
		case store.SKFacts:
			var item factsItem
			if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
				return nil, fmt.Errorf("erro ao decodificar FACTS: %w", err)
			}
			if item.Facts != nil {
				state.Facts = item.Facts
			}
		case store.SKSignals:
			var item signalsItem
			if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
				return nil, fmt.Errorf("erro ao decodificar SIGNALS: %w", err)
			}
			if item.Signals != nil {
				state.Signals = item.Signals
			}
		case store.SKContext:
			var item contextItem
			if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
				return nil, fmt.Errorf("erro ao decodificar CONTEXT: %w", err)
			}
			state.Context = core.ConversationContext{
				LastActionType: item.LastActionType,
				LastActionBy:   item.LastActionBy,
				Description:    item.Description,
				Details:        item.Details,
				Sections:       item.Sections,
				UpdatedAt:      parseTime(item.UpdatedAt),
			}
		}
	}

	return state, nil
}

func (s *Store) SaveSourceMeta(ctx context.Context, meta core.SourceMeta) error {
	now := meta.UpdatedAt
	if now.IsZero() {
		now = s.clock.Now().UTC()
	}
	created := meta.CreatedAt
	if created.IsZero() {
		created = now
	}
	status := meta.Status
	if status == "" {
		status = core.SourceStatusActive
	}

	item := metaItem{
		PK:                   store.SourcePK(meta.TenantID, meta.Channel, meta.SourceKey),
		SK:                   store.SKMeta,
		TenantID:             meta.TenantID,
		Channel:              meta.Channel,
		SourceKey:            meta.SourceKey,
		ContactID:            meta.ContactID,
		Status:               status,
		JourneyStage:         meta.JourneyStage,
		PausedUntil:          formatTimePtr(meta.PausedUntil),
		Version:              meta.Version,
		EncryptedContactInfo: meta.EncryptedContactInfo,
		CreatedAt:            formatTime(created),
		UpdatedAt:            formatTime(now),
	}
	if meta.ContactID != "" {
		item.GSI1PK = store.ContactPK(meta.TenantID, meta.ContactID)
		item.GSI1SK = store.TenantSourceSK(meta.Channel, meta.SourceKey)
	}

	avMeta, err := attributevalue.MarshalMap(item)
	if err != nil {
		return err
	}

	catItem := sourceCatalogItem{
		PK:        store.TenantPK(meta.TenantID),
		SK:        store.TenantSourceSK(meta.Channel, meta.SourceKey),
		TenantID:  meta.TenantID,
		Channel:   meta.Channel,
		SourceKey: meta.SourceKey,
		Status:    status,
		UpdatedAt: formatTime(now),
	}
	avCat, err := attributevalue.MarshalMap(catItem)
	if err != nil {
		return err
	}

	_, err = s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{TableName: aws.String(s.tableName), Item: avMeta}},
			{Put: &types.Put{TableName: aws.String(s.tableName), Item: avCat}},
		},
	})
	return err
}

func (s *Store) CommitAnalysis(ctx context.Context, input core.CommitAnalysisInput) error {
	if err := core.CheckCommitAnalysisSizes(input); err != nil {
		return err
	}
	now := input.UpdatedAt
	if now.IsZero() {
		now = s.clock.Now().UTC()
	}

	pk := store.SourcePK(input.TenantID, input.Channel, input.SourceKey)
	newVersion := input.ExpectedVersion + 1

	// Carrega META atual para preservar ContactID / CreatedAt / Status se já existir
	existingState, err := s.GetSourceState(ctx, input.TenantID, input.Channel, input.SourceKey)
	if err != nil {
		return err
	}
	if existingState.Version != input.ExpectedVersion {
		return fmt.Errorf("%w: esperado %d, atual %d", core.ErrVersionConflict, input.ExpectedVersion, existingState.Version)
	}

	createdAt := existingState.Meta.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	status := existingState.Meta.Status
	if status == "" {
		status = core.SourceStatusActive
	}

	stage := input.JourneyStage
	if stage == "" {
		stage = existingState.Meta.JourneyStage
	}

	meta := metaItem{
		PK:                   pk,
		SK:                   store.SKMeta,
		TenantID:             input.TenantID,
		Channel:              input.Channel,
		SourceKey:            input.SourceKey,
		ContactID:            existingState.Meta.ContactID,
		Status:               status,
		JourneyStage:         stage,
		PausedUntil:          formatTimePtr(existingState.Meta.PausedUntil),
		Version:              newVersion,
		EncryptedContactInfo: existingState.Meta.EncryptedContactInfo,
		CreatedAt:            formatTime(createdAt),
		UpdatedAt:            formatTime(now),
	}
	if existingState.Meta.ContactID != "" {
		meta.GSI1PK = store.ContactPK(input.TenantID, existingState.Meta.ContactID)
		meta.GSI1SK = store.TenantSourceSK(input.Channel, input.SourceKey)
	}
	avMeta, err := attributevalue.MarshalMap(meta)
	if err != nil {
		return err
	}

	var metaCondition *string
	var metaExprNames map[string]string
	var metaExprValues map[string]types.AttributeValue
	if input.ExpectedVersion == 0 {
		metaCondition = aws.String("attribute_not_exists(PK) OR #ver = :expected")
		metaExprNames = map[string]string{"#ver": "version"}
		metaExprValues = map[string]types.AttributeValue{
			":expected": &types.AttributeValueMemberN{Value: "0"},
		}
	} else {
		metaCondition = aws.String("#ver = :expected")
		metaExprNames = map[string]string{"#ver": "version"}
		metaExprValues = map[string]types.AttributeValue{
			":expected": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", input.ExpectedVersion)},
		}
	}

	avSummary, err := attributevalue.MarshalMap(summaryItem{
		PK:        pk,
		SK:        store.SKSummary,
		TenantID:  input.TenantID,
		Channel:   input.Channel,
		SourceKey: input.SourceKey,
		Text:      input.Summary.Text,
		TurnCount: input.Summary.TurnCount,
		UpdatedAt: formatTime(now),
	})
	if err != nil {
		return err
	}

	facts := input.Facts
	if facts == nil {
		facts = map[string]string{}
	}
	avFacts, err := attributevalue.MarshalMap(factsItem{
		PK:        pk,
		SK:        store.SKFacts,
		Facts:     facts,
		UpdatedAt: formatTime(now),
	})
	if err != nil {
		return err
	}

	signals := input.Signals
	if signals == nil {
		signals = map[string]bool{}
	}
	avSignals, err := attributevalue.MarshalMap(signalsItem{
		PK:        pk,
		SK:        store.SKSignals,
		Signals:   signals,
		UpdatedAt: formatTime(now),
	})
	if err != nil {
		return err
	}

	avContext, err := attributevalue.MarshalMap(contextItem{
		PK:             pk,
		SK:             store.SKContext,
		LastActionType: input.Context.LastActionType,
		LastActionBy:   input.Context.LastActionBy,
		Description:    input.Context.Description,
		Details:        input.Context.Details,
		Sections:       input.Context.Sections,
		UpdatedAt:      formatTime(now),
	})
	if err != nil {
		return err
	}

	avCatalog, err := attributevalue.MarshalMap(sourceCatalogItem{
		PK:        store.TenantPK(input.TenantID),
		SK:        store.TenantSourceSK(input.Channel, input.SourceKey),
		TenantID:  input.TenantID,
		Channel:   input.Channel,
		SourceKey: input.SourceKey,
		Status:    status,
		UpdatedAt: formatTime(now),
	})
	if err != nil {
		return err
	}

	txItems := []types.TransactWriteItem{
		{
			Put: &types.Put{
				TableName:                 aws.String(s.tableName),
				Item:                      avMeta,
				ConditionExpression:       metaCondition,
				ExpressionAttributeNames:  metaExprNames,
				ExpressionAttributeValues: metaExprValues,
			},
		},
		{Put: &types.Put{TableName: aws.String(s.tableName), Item: avSummary}},
		{Put: &types.Put{TableName: aws.String(s.tableName), Item: avFacts}},
		{Put: &types.Put{TableName: aws.String(s.tableName), Item: avSignals}},
		{Put: &types.Put{TableName: aws.String(s.tableName), Item: avContext}},
		{Put: &types.Put{TableName: aws.String(s.tableName), Item: avCatalog}},
	}

	if input.Run.ID != "" {
		avRun, err := marshalRunItem(input.Run)
		if err != nil {
			return err
		}
		txItems = append(txItems, types.TransactWriteItem{
			Put: &types.Put{TableName: aws.String(s.tableName), Item: avRun},
		})
	}

	_, err = s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: txItems,
	})
	if err != nil {
		if isConditionalCheckFailed(err) {
			return fmt.Errorf("%w: condição de versão falhou", core.ErrVersionConflict)
		}
		return fmt.Errorf("dynamo CommitAnalysis falhou: %w", err)
	}
	return nil
}

func (s *Store) PutMessageMeta(ctx context.Context, msg core.MessageMeta) error {
	pk := store.SourcePK(msg.TenantID, msg.Channel, msg.SourceKey)
	ts := msg.Timestamp
	if ts.IsZero() {
		ts = s.clock.Now().UTC()
	}

	avDedup, err := attributevalue.MarshalMap(dedupItem{
		PK:        pk,
		SK:        store.MessageDedupSK(msg.MessageID),
		MessageID: msg.MessageID,
		CreatedAt: formatTime(ts),
	})
	if err != nil {
		return err
	}

	avMsg, err := attributevalue.MarshalMap(messageItem{
		PK:        pk,
		SK:        store.MessageSK(ts, msg.MessageID),
		TenantID:  msg.TenantID,
		Channel:   msg.Channel,
		SourceKey: msg.SourceKey,
		MessageID: msg.MessageID,
		Timestamp: formatTime(ts),
		Direction: msg.Direction,
		Type:      msg.Type,
		Origin:    msg.Origin,
		MediaRef:  msg.MediaRef,
		RawRef:    msg.RawRef,
		Sender:    msg.Sender,
		Answered:  msg.Answered,
	})
	if err != nil {
		return err
	}

	avCatalog, err := attributevalue.MarshalMap(sourceCatalogItem{
		PK:        store.TenantPK(msg.TenantID),
		SK:        store.TenantSourceSK(msg.Channel, msg.SourceKey),
		TenantID:  msg.TenantID,
		Channel:   msg.Channel,
		SourceKey: msg.SourceKey,
		Status:    core.SourceStatusActive,
		UpdatedAt: formatTime(ts),
	})
	if err != nil {
		return err
	}

	_, err = s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{
				Put: &types.Put{
					TableName:           aws.String(s.tableName),
					Item:                avDedup,
					ConditionExpression: aws.String("attribute_not_exists(PK)"),
				},
			},
			{
				Put: &types.Put{
					TableName: aws.String(s.tableName),
					Item:      avMsg,
				},
			},
			{
				Put: &types.Put{
					TableName: aws.String(s.tableName),
					Item:      avCatalog,
				},
			},
		},
	})
	if err != nil {
		if isConditionalCheckFailed(err) {
			return fmt.Errorf("%w: %s", core.ErrDuplicateMessage, msg.MessageID)
		}
		return fmt.Errorf("dynamo PutMessageMeta falhou: %w", err)
	}
	return nil
}

func (s *Store) MarkMessagesAnswered(ctx context.Context, tenant, channel, sourceKey string, messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}
	pk := store.SourcePK(tenant, channel, sourceKey)
	out, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: pk},
			":prefix": &types.AttributeValueMemberS{Value: "MSG#"},
		},
		ScanIndexForward: aws.Bool(false),
		ConsistentRead:   aws.Bool(true),
		Limit:            aws.Int32(50),
	})
	if err != nil {
		return fmt.Errorf("dynamo MarkMessagesAnswered query falhou: %w", err)
	}

	idSet := make(map[string]bool, len(messageIDs))
	markAllInbound := false
	for _, mid := range messageIDs {
		if mid == "*" {
			markAllInbound = true
		}
		idSet[mid] = true
	}

	for _, raw := range out.Items {
		var item messageItem
		if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
			continue
		}
		if item.Answered {
			continue
		}
		if (markAllInbound && item.Direction == core.DirectionInbound) || idSet[item.MessageID] {
			_, updErr := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
				TableName: aws.String(s.tableName),
				Key: map[string]types.AttributeValue{
					"PK": &types.AttributeValueMemberS{Value: item.PK},
					"SK": &types.AttributeValueMemberS{Value: item.SK},
				},
				UpdateExpression: aws.String("SET answered = :t"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":t": &types.AttributeValueMemberBOOL{Value: true},
				},
			})
			if updErr != nil {
				return fmt.Errorf("dynamo MarkMessagesAnswered update falhou: %w", updErr)
			}
		}
	}
	return nil
}

func (s *Store) ListRecentMessages(ctx context.Context, tenant, channel, sourceKey string, limit int) ([]core.MessageMeta, error) {
	pk := store.SourcePK(tenant, channel, sourceKey)
	input := &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: pk},
			":prefix": &types.AttributeValueMemberS{Value: "MSG#"},
		},
		ScanIndexForward: aws.Bool(false),
		ConsistentRead:   aws.Bool(true),
	}
	if limit > 0 {
		input.Limit = aws.Int32(int32(limit))
	}

	out, err := s.client.Query(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("dynamo ListRecentMessages falhou: %w", err)
	}

	result := make([]core.MessageMeta, 0, len(out.Items))
	for _, raw := range out.Items {
		var item messageItem
		if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
			return nil, err
		}
		result = append(result, core.MessageMeta{
			TenantID:  item.TenantID,
			Channel:   item.Channel,
			SourceKey: item.SourceKey,
			MessageID: item.MessageID,
			Timestamp: parseTime(item.Timestamp),
			Direction: item.Direction,
			Type:      item.Type,
			Origin:    item.Origin,
			MediaRef:  item.MediaRef,
			RawRef:    item.RawRef,
			Sender:    item.Sender,
			Answered:  item.Answered,
		})
	}
	return result, nil
}

func (s *Store) ListSources(ctx context.Context, tenant string) ([]core.SourceMeta, error) {
	out, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: store.TenantPK(tenant)},
			":prefix": &types.AttributeValueMemberS{Value: "SRC#"},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo ListSources falhou: %w", err)
	}

	result := make([]core.SourceMeta, 0, len(out.Items))
	for _, raw := range out.Items {
		var item sourceCatalogItem
		if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
			return nil, err
		}
		result = append(result, core.SourceMeta{
			TenantID:  item.TenantID,
			Channel:   item.Channel,
			SourceKey: item.SourceKey,
			Status:    item.Status,
			UpdatedAt: parseTime(item.UpdatedAt),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

func (s *Store) ListRuns(ctx context.Context, tenant, channel, sourceKey string, limit int) ([]core.Run, error) {
	pk := store.SourcePK(tenant, channel, sourceKey)
	input := &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: pk},
			":prefix": &types.AttributeValueMemberS{Value: "RUN#"},
		},
		ScanIndexForward: aws.Bool(false),
		ConsistentRead:   aws.Bool(true),
	}
	if limit > 0 {
		input.Limit = aws.Int32(int32(limit))
	}

	out, err := s.client.Query(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("dynamo ListRuns falhou: %w", err)
	}

	result := make([]core.Run, 0, len(out.Items))
	for _, raw := range out.Items {
		var item runItem
		if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
			return nil, err
		}
		result = append(result, core.Run{
			ID:               item.ID,
			TenantID:         item.TenantID,
			Channel:          item.Channel,
			SourceKey:        item.SourceKey,
			MessageID:        item.MessageID,
			Stage:            item.Stage,
			Status:           item.Status,
			Model:            item.Model,
			PromptTokens:     item.PromptTokens,
			CompletionTokens: item.CompletionTokens,
			DurationMs:       item.DurationMs,
			TraceBlobRef:     item.TraceBlobRef,
			TTL:              item.TTL,
			CreatedAt:        parseTime(item.CreatedAt),
		})
	}
	return result, nil
}

func taskFromItem(item taskItem) core.Task {
	t := core.Task{
		ID:           item.ID,
		TenantID:     item.TenantID,
		Channel:      item.Channel,
		SourceKey:    item.SourceKey,
		Kind:         item.Kind,
		Title:        item.Title,
		Type:         item.Type,
		Status:       item.Status,
		DraftBlobRef: item.DraftBlobRef,
		Attempt:      item.Attempt,
		MaxAttempts:  item.MaxAttempts,
		Metadata:     item.Metadata,
		CreatedAt:    parseTime(item.CreatedAt),
	}
	if item.DueAt != "" {
		d := parseTime(item.DueAt)
		t.DueAt = &d
	}
	if item.CompletedAt != "" {
		c := parseTime(item.CompletedAt)
		t.CompletedAt = &c
	}
	return t
}

func (s *Store) SaveTask(ctx context.Context, task core.Task) error {
	if err := task.Validate(); err != nil {
		return err
	}

	item := taskItem{
		PK:           store.SourcePK(task.TenantID, task.Channel, task.SourceKey),
		SK:           store.TaskSK(task.ID),
		ID:           task.ID,
		TenantID:     task.TenantID,
		Channel:      task.Channel,
		SourceKey:    task.SourceKey,
		Kind:         task.Kind,
		Title:        task.Title,
		Type:         task.Type,
		Status:       task.Status,
		DraftBlobRef: task.DraftBlobRef,
		Attempt:      task.Attempt,
		MaxAttempts:  task.MaxAttempts,
		Metadata:     task.Metadata,
		CreatedAt:    formatTime(task.CreatedAt),
	}
	if task.DueAt != nil {
		item.DueAt = formatTime(*task.DueAt)
		if task.Status == core.TaskStatusPending {
			item.GSI2PK = store.DueGSI2PK(task.TenantID)
			item.GSI2SK = store.DueGSI2SK(*task.DueAt, task.Channel, task.SourceKey)
		}
	}
	if task.CompletedAt != nil {
		item.CompletedAt = formatTime(*task.CompletedAt)
	}

	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		return err
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      av,
	})
	return err
}

func (s *Store) GetTask(ctx context.Context, tenant, channel, sourceKey, taskID string) (*core.Task, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: store.SourcePK(tenant, channel, sourceKey)},
			"SK": &types.AttributeValueMemberS{Value: store.TaskSK(taskID)},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo GetTask falhou: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, core.ErrTaskNotFound
	}
	var item taskItem
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return nil, err
	}
	t := taskFromItem(item)
	return &t, nil
}

func (s *Store) ListSourceTasks(ctx context.Context, tenant, channel, sourceKey string) ([]core.Task, error) {
	out, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: store.SourcePK(tenant, channel, sourceKey)},
			":prefix": &types.AttributeValueMemberS{Value: "TASK#"},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo ListSourceTasks falhou: %w", err)
	}

	result := make([]core.Task, 0, len(out.Items))
	for _, raw := range out.Items {
		var item taskItem
		if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
			return nil, err
		}
		result = append(result, taskFromItem(item))
	}
	return result, nil
}

func (s *Store) ListDueTasks(ctx context.Context, tenant string, dueBefore time.Time) ([]core.Task, error) {
	maxSK := store.DueGSI2SK(dueBefore, "\uffff", "\uffff")
	out, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String(GSI2Name),
		KeyConditionExpression: aws.String("GSI2PK = :pk AND GSI2SK <= :maxsk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":    &types.AttributeValueMemberS{Value: store.DueGSI2PK(tenant)},
			":maxsk": &types.AttributeValueMemberS{Value: maxSK},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo ListDueTasks falhou: %w", err)
	}

	result := make([]core.Task, 0, len(out.Items))
	for _, raw := range out.Items {
		var item taskItem
		if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
			return nil, err
		}
		result = append(result, taskFromItem(item))
	}
	return result, nil
}

func (s *Store) SaveRun(ctx context.Context, run core.Run) error {
	av, err := marshalRunItem(run)
	if err != nil {
		return err
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      av,
	})
	return err
}

func (s *Store) LinkSourceToContact(ctx context.Context, tenant, channel, sourceKey, contactID string) error {
	now := s.clock.Now().UTC()
	state, err := s.GetSourceState(ctx, tenant, channel, sourceKey)
	if err != nil {
		return err
	}

	created := state.Meta.CreatedAt
	if created.IsZero() {
		created = now
	}
	status := state.Meta.Status
	if status == "" {
		status = core.SourceStatusActive
	}

	meta := metaItem{
		PK:                   store.SourcePK(tenant, channel, sourceKey),
		SK:                   store.SKMeta,
		GSI1PK:               store.ContactPK(tenant, contactID),
		GSI1SK:               store.TenantSourceSK(channel, sourceKey),
		TenantID:             tenant,
		Channel:              channel,
		SourceKey:            sourceKey,
		ContactID:            contactID,
		Status:               status,
		Version:              state.Meta.Version,
		EncryptedContactInfo: state.Meta.EncryptedContactInfo,
		CreatedAt:            formatTime(created),
		UpdatedAt:            formatTime(now),
	}
	avMeta, err := attributevalue.MarshalMap(meta)
	if err != nil {
		return err
	}

	link := contactLinkItem{
		PK:        store.ContactPK(tenant, contactID),
		SK:        store.TenantSourceSK(channel, sourceKey),
		GSI1PK:    store.ContactPK(tenant, contactID),
		GSI1SK:    store.TenantSourceSK(channel, sourceKey),
		TenantID:  tenant,
		ContactID: contactID,
		Channel:   channel,
		SourceKey: sourceKey,
		LinkedAt:  formatTime(now),
	}
	avLink, err := attributevalue.MarshalMap(link)
	if err != nil {
		return err
	}

	_, err = s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{TableName: aws.String(s.tableName), Item: avMeta}},
			{Put: &types.Put{TableName: aws.String(s.tableName), Item: avLink}},
		},
	})
	return err
}

func (s *Store) ListContactSources(ctx context.Context, tenant, contactID string) ([]core.SourceLink, error) {
	out, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: store.ContactPK(tenant, contactID)},
			":prefix": &types.AttributeValueMemberS{Value: "SRC#"},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo ListContactSources falhou: %w", err)
	}

	result := make([]core.SourceLink, 0, len(out.Items))
	for _, raw := range out.Items {
		var item contactLinkItem
		if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
			return nil, err
		}
		result = append(result, core.SourceLink{
			TenantID:  item.TenantID,
			ContactID: item.ContactID,
			Channel:   item.Channel,
			SourceKey: item.SourceKey,
			LinkedAt:  parseTime(item.LinkedAt),
		})
	}
	return result, nil
}

func (s *Store) SaveTenantConfig(ctx context.Context, tenant, name string, data []byte) error {
	if strings.TrimSpace(tenant) == "" {
		return core.ErrEmptyTenantID
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("config name is required")
	}
	copied := append([]byte(nil), data...)
	item := tenantConfigItem{
		PK:        store.TenantPK(tenant),
		SK:        store.TenantConfigSK(name),
		TenantID:  tenant,
		Name:      name,
		Data:      copied,
		UpdatedAt: formatTime(s.clock.Now().UTC()),
	}
	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		return err
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      av,
	})
	return err
}

func (s *Store) GetTenantConfig(ctx context.Context, tenant, name string) ([]byte, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: store.TenantPK(tenant)},
			"SK": &types.AttributeValueMemberS{Value: store.TenantConfigSK(name)},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo GetTenantConfig falhou: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, core.ErrConfigNotFound
	}
	var item tenantConfigItem
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return nil, err
	}
	return append([]byte(nil), item.Data...), nil
}

func (s *Store) ListSourcesByContactGSI1(ctx context.Context, tenant, contactID string) ([]core.SourceMeta, error) {
	if strings.TrimSpace(contactID) == "" {
		return []core.SourceMeta{}, nil
	}
	out, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String(GSI1Name),
		KeyConditionExpression: aws.String("GSI1PK = :pk"),
		FilterExpression:       aws.String("SK = :meta"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":   &types.AttributeValueMemberS{Value: store.ContactPK(tenant, contactID)},
			":meta": &types.AttributeValueMemberS{Value: store.SKMeta},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamo ListSourcesByContactGSI1 falhou: %w", err)
	}

	result := make([]core.SourceMeta, 0, len(out.Items))
	for _, raw := range out.Items {
		var item metaItem
		if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
			return nil, err
		}
		result = append(result, core.SourceMeta{
			TenantID:             item.TenantID,
			Channel:              item.Channel,
			SourceKey:            item.SourceKey,
			ContactID:            item.ContactID,
			Status:               item.Status,
			JourneyStage:         item.JourneyStage,
			Version:              item.Version,
			EncryptedContactInfo: item.EncryptedContactInfo,
			CreatedAt:            parseTime(item.CreatedAt),
			UpdatedAt:            parseTime(item.UpdatedAt),
		})
	}
	return result, nil
}

func marshalRunItem(run core.Run) (map[string]types.AttributeValue, error) {
	return attributevalue.MarshalMap(runItem{
		PK:               store.SourcePK(run.TenantID, run.Channel, run.SourceKey),
		SK:               store.RunSK(run.CreatedAt, run.ID),
		ID:               run.ID,
		TenantID:         run.TenantID,
		Channel:          run.Channel,
		SourceKey:        run.SourceKey,
		MessageID:        run.MessageID,
		Stage:            run.Stage,
		Status:           run.Status,
		Model:            run.Model,
		PromptTokens:     run.PromptTokens,
		CompletionTokens: run.CompletionTokens,
		DurationMs:       run.DurationMs,
		TraceBlobRef:     run.TraceBlobRef,
		TTL:              run.TTL,
		CreatedAt:        formatTime(run.CreatedAt),
	})
}

func isConditionalCheckFailed(err error) bool {
	var txCanceled *types.TransactionCanceledException
	if errors.As(err, &txCanceled) {
		for _, r := range txCanceled.CancellationReasons {
			if r.Code != nil && *r.Code == "ConditionalCheckFailed" {
				return true
			}
		}
	}
	var condFailed *types.ConditionalCheckFailedException
	if errors.As(err, &condFailed) {
		return true
	}
	return strings.Contains(err.Error(), "ConditionalCheckFailed")
}

func formatTime(t time.Time) string {
	return t.UTC().Format(store.TimestampFormat)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(store.TimestampFormat, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339Nano, s)
	}
	return t.UTC()
}

func formatTimePtr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return formatTime(*t)
}

func parseTimePtr(s string) *time.Time {
	if s == "" {
		return nil
	}
	t := parseTime(s)
	if t.IsZero() {
		return nil
	}
	return &t
}
